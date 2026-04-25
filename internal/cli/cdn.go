package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var cdnCmd = &cobra.Command{
	Use:   "cdn",
	Short: "CDN operations",
	Long: `Manage CDN assets (S3 sync).

Examples:
  lw cdn push                   # Push static assets to CDN
  lw cdn pull media             # Pull media from CDN
  lw cdn push media             # Push media to CDN
  lw cdn reconcile --dry-run    # Show legacy bucket prefixes vs SST allowlist
  lw cdn reconcile --yes        # Delete legacy prefixes (CI/agent use)`,
}

var cdnPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push static assets to CDN",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "cdn-push")
	},
}

var cdnPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull assets from CDN",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var cdnPullMediaCmd = &cobra.Command{
	Use:   "media",
	Short: "Pull media files from CDN",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "cdn-pull-media")
	},
}

var cdnPushMediaCmd = &cobra.Command{
	Use:   "media",
	Short: "Push media files to CDN",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "cdn-push-media")
	},
}

var (
	cdnReconcileDryRun bool
	cdnReconcileYes    bool
)

var cdnReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Reconcile CDN bucket against the SST prefix allowlist",
	Long: `Compare top-level prefixes in the CDN origin bucket to the canonical
allowlist defined in SST (assets.yaml: cdn.paths). Anything in the bucket
that isn't on the allowlist is "drift" and can be removed.

Source of truth:
  packages/lightwave-core/lightwave/schema/definitions/data/assets/assets.yaml
  packages/lightwave-core/lightwave/schema/definitions/data/models/domains.yaml

Examples:
  lw cdn reconcile --dry-run    # Show drift only, exit
  lw cdn reconcile              # Interactive: prompt before deleting
  lw cdn reconcile --yes        # Skip confirmation (CI/agent use)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCdnReconcile(context.Background())
	},
}

// cdnConfig is the subset of assets.yaml we care about.
type cdnConfig struct {
	CDN struct {
		Origin struct {
			Bucket string `yaml:"bucket"`
			Region string `yaml:"region"`
		} `yaml:"origin"`
		Paths map[string]string `yaml:"paths"`
	} `yaml:"cdn"`
}

// domainsConfig is the subset of domains.yaml we care about.
type domainsConfig struct {
	InfrastructureDomain string `yaml:"infrastructure_domain"`
}

// loadCdnSST reads assets.yaml + domains.yaml and returns the resolved
// bucket name, region, and the set of legitimate prefix names.
func loadCdnSST() (bucket, region string, allowed map[string]struct{}, err error) {
	cfg := config.Get()
	defs := filepath.Join(cfg.Paths.LightwaveRoot,
		"packages/lightwave-core/lightwave/schema/definitions")

	assetsPath := filepath.Join(defs, "data/assets/assets.yaml")
	domainsPath := filepath.Join(defs, "data/models/domains.yaml")

	assetsRaw, err := os.ReadFile(assetsPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("read %s: %w", assetsPath, err)
	}
	var assets cdnConfig
	if err := yaml.Unmarshal(assetsRaw, &assets); err != nil {
		return "", "", nil, fmt.Errorf("parse assets.yaml: %w", err)
	}

	domainsRaw, err := os.ReadFile(domainsPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("read %s: %w", domainsPath, err)
	}
	// domains.yaml has nested structure — locate infrastructure_domain at
	// the top level by scanning the raw YAML map.
	var domainsMap map[string]any
	if err := yaml.Unmarshal(domainsRaw, &domainsMap); err != nil {
		return "", "", nil, fmt.Errorf("parse domains.yaml: %w", err)
	}
	infraDomain := findInfraDomain(domainsMap)
	if infraDomain == "" {
		return "", "", nil, fmt.Errorf("infrastructure_domain not found in domains.yaml")
	}

	bucket = strings.ReplaceAll(assets.CDN.Origin.Bucket,
		"{{ infrastructure_domain }}", infraDomain)
	region = assets.CDN.Origin.Region
	if region == "" {
		region = "us-east-1"
	}

	allowed = make(map[string]struct{}, len(assets.CDN.Paths))
	for _, p := range assets.CDN.Paths {
		name := strings.Trim(p, "/")
		if name == "" {
			continue
		}
		allowed[name] = struct{}{}
	}
	if len(allowed) == 0 {
		return "", "", nil, fmt.Errorf("cdn.paths is empty in assets.yaml")
	}
	return bucket, region, allowed, nil
}

// findInfraDomain walks the domains.yaml map shape looking for the
// infrastructure_domain key at any nesting level (it lives under a
// product key in practice but we don't want to hardcode that).
func findInfraDomain(m map[string]any) string {
	if v, ok := m["infrastructure_domain"]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	for _, v := range m {
		if child, ok := v.(map[string]any); ok {
			if s := findInfraDomain(child); s != "" {
				return s
			}
		}
	}
	return ""
}

// computeDrift returns the set of bucket prefixes NOT in the allowlist.
// Pure function — no AWS calls, easy to unit test.
func computeDrift(bucketPrefixes []string, allowed map[string]struct{}) []string {
	var drift []string
	for _, p := range bucketPrefixes {
		name := strings.Trim(p, "/")
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; ok {
			continue
		}
		drift = append(drift, name)
	}
	sort.Strings(drift)
	return drift
}

// listTopLevelPrefixes returns top-level prefixes in the bucket (one
// "directory" deep, using "/" as delimiter).
func listTopLevelPrefixes(ctx context.Context, client *s3.Client, bucket string) ([]string, error) {
	out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Delimiter: aws.String("/"),
	})
	if err != nil {
		return nil, fmt.Errorf("list bucket %s: %w", bucket, err)
	}
	prefixes := make([]string, 0, len(out.CommonPrefixes))
	for _, p := range out.CommonPrefixes {
		if p.Prefix != nil {
			prefixes = append(prefixes, *p.Prefix)
		}
	}
	return prefixes, nil
}

// prefixStats counts objects + total bytes under a given prefix.
type prefixStats struct {
	objects int64
	bytes   int64
}

func statPrefix(ctx context.Context, client *s3.Client, bucket, prefix string) (prefixStats, error) {
	var stats prefixStats
	var token *string
	for {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return stats, err
		}
		for _, o := range out.Contents {
			stats.objects++
			if o.Size != nil {
				stats.bytes += *o.Size
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	return stats, nil
}

// deletePrefix removes every object under a prefix in batches of 1000.
func deletePrefix(ctx context.Context, client *s3.Client, bucket, prefix string) (int64, error) {
	var deleted int64
	var token *string
	for {
		listOut, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return deleted, fmt.Errorf("list %s: %w", prefix, err)
		}
		if len(listOut.Contents) == 0 {
			break
		}
		ids := make([]s3types.ObjectIdentifier, 0, len(listOut.Contents))
		for _, o := range listOut.Contents {
			ids = append(ids, s3types.ObjectIdentifier{Key: o.Key})
		}
		// DeleteObjects caps at 1000 keys, ListObjectsV2 caps at 1000 by default,
		// so one delete call per list page.
		_, err = client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return deleted, fmt.Errorf("delete batch under %s: %w", prefix, err)
		}
		deleted += int64(len(ids))
		if listOut.IsTruncated == nil || !*listOut.IsTruncated {
			break
		}
		token = listOut.NextContinuationToken
	}
	return deleted, nil
}

func humanBytes(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func runCdnReconcile(ctx context.Context) error {
	bucket, region, allowed, err := loadCdnSST()
	if err != nil {
		return err
	}

	allowedList := make([]string, 0, len(allowed))
	for k := range allowed {
		allowedList = append(allowedList, k)
	}
	sort.Strings(allowedList)

	fmt.Printf("Bucket: %s (%s)\n", color.CyanString(bucket), region)
	fmt.Printf("SST allowlist (%d): %s\n\n",
		len(allowedList), strings.Join(allowedList, ", "))

	awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(region))
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg)

	bucketPrefixes, err := listTopLevelPrefixes(ctx, client, bucket)
	if err != nil {
		return err
	}

	drift := computeDrift(bucketPrefixes, allowed)
	if len(drift) == 0 {
		fmt.Println(color.GreenString("✓ No drift — bucket matches SST allowlist"))
		return nil
	}

	fmt.Printf("Drift (%d prefixes not in SST):\n", len(drift))
	type driftRow struct {
		name  string
		stats prefixStats
	}
	rows := make([]driftRow, 0, len(drift))
	for _, name := range drift {
		stats, err := statPrefix(ctx, client, bucket, name+"/")
		if err != nil {
			return err
		}
		rows = append(rows, driftRow{name: name, stats: stats})
		fmt.Printf("  %s  %d objects  %s\n",
			color.YellowString("%-20s", name+"/"),
			stats.objects, humanBytes(stats.bytes))
	}

	if cdnReconcileDryRun {
		fmt.Println(color.CyanString("\nDry run — nothing deleted"))
		return nil
	}

	if !cdnReconcileYes {
		fmt.Printf("\nDelete %d legacy prefixes from %s? [y/N] ",
			len(drift), bucket)
		var confirm string
		fmt.Scanln(&confirm)
		if confirm != "y" && confirm != "Y" && confirm != "yes" {
			fmt.Println("Cancelled")
			return nil
		}
	}

	var totalDeleted int64
	for _, row := range rows {
		fmt.Printf("Deleting %s ...\n", row.name+"/")
		n, err := deletePrefix(ctx, client, bucket, row.name+"/")
		if err != nil {
			fmt.Printf("  %s %s: %v\n", color.RedString("FAIL"), row.name, err)
			continue
		}
		fmt.Printf("  %s %s (%d objects)\n",
			color.GreenString("DELETED"), row.name, n)
		totalDeleted += n
	}

	fmt.Printf("\nDeleted %s objects across %d prefixes\n",
		color.GreenString("%d", totalDeleted), len(rows))
	return nil
}

func init() {
	cdnPullCmd.AddCommand(cdnPullMediaCmd)
	cdnPushCmd.AddCommand(cdnPushMediaCmd)

	cdnReconcileCmd.Flags().BoolVar(&cdnReconcileDryRun, "dry-run", false,
		"Show drift without deleting")
	cdnReconcileCmd.Flags().BoolVar(&cdnReconcileYes, "yes", false,
		"Skip confirmation prompt (for CI/agent use)")

	cdnCmd.AddCommand(cdnPushCmd)
	cdnCmd.AddCommand(cdnPullCmd)
	cdnCmd.AddCommand(cdnReconcileCmd)
}
