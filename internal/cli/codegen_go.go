package cli

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/codegen/gogen"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	codegenGoOut   string
	codegenGoOnly  string
	codegenGoCheck bool
	codegenGoRef   string
	codegenGoScope string
)

// migrationFile is the single combined DDL output. One file (not per-table) so
// the platform's lexical db.Migrate applies tables in FK order (lightwave-cli#227).
const migrationFile = "schema.sql"

var codegenGoCmd = &cobra.Command{
	Use:   "go [family]",
	Short: "Generate Go structs + a multi-tenant SQL migration from SST entity schemas",
	Long: `Reads entity schemas from lightwave-core and emits:
  {table}.go   — Go struct (id, tenant_id, then schema fields; db/json tags)
  schema.sql   — ONE migration: all tables in FK-topological order, each with
                 tenant_id + an RLS tenant-isolation policy + FORCE (ADR-0010).
                 Depends on 001_init.sql (tenants table); apply after it.

family selects a schema sub-directory (default: data/agile_artifacts). A
parent directory works too — "data" reads every family beneath it.

Schemas are read from a git REF, not the working tree (default origin/main).
That checkout is shared: measured 2026-09-13 it sat on a feature branch while
origin/main was three commits behind, with nine worktrees attached, so
"generated from the stamp" named no particular stamp. The resolved sha is
printed with the summary. Use --ref worktree to iterate on an uncommitted
schema.

--scope selects by storage scope, so a local print can exclude the
tenant-scoped platform tables instead of the generator hardcoding a list.

Emits a table for table_kind entity AND document. A document table indexes
files (source_path, content_sha256, indexed_at) rather than replacing them —
the file stays truth (ADR-0049).

Examples:
  lw codegen go                          # agile_artifacts at origin/main
  lw codegen go data --scope local       # every local family, one migration
  lw codegen go --ref worktree           # read the working tree instead
  lw codegen go --ref v0.6.5             # generate from a released stamp
  lw codegen go --only epic              # generate only the epic struct
  lw codegen go --dry-run                # print to stdout, write nothing
  lw codegen go --check                  # exit 1 if generated output is stale`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCodegenGo,
}

func init() {
	codegenGoCmd.Flags().StringVar(&codegenGoOut, "out", "", "output directory (default: <lightwave_root>/lightwave-platform/backend/foundation/store/generated)")
	codegenGoCmd.Flags().StringVar(&codegenGoOnly, "only", "", "generate only the named entity (e.g. --only epic)")
	codegenGoCmd.Flags().BoolVar(&codegenGoCheck, "check", false, "exit 1 if output is stale (drift gate)")
	codegenGoCmd.Flags().StringVar(&codegenGoRef, "ref", "origin/main",
		"git ref to read schemas from; 'worktree' reads the working tree")
	codegenGoCmd.Flags().StringVar(&codegenGoScope, "scope", "",
		"only schemas declaring this storage scope (platform|local|both); empty means no filter")
	codegenCmd.AddCommand(codegenGoCmd)
}

func runCodegenGo(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cfg := config.Get()

	root := cfg.Paths.LightwaveRoot
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "dev")
	}

	family := "data/agile_artifacts"
	if len(args) > 0 {
		family = args[0]
	}

	coreRoot := filepath.Join(root, "lightwave-core")
	// Repo-relative, because a Source may resolve it through git rather than
	// through the filesystem.
	entityDir := path.Join("src", "schemas", family)

	src, err := gogen.NewSource(ctx, coreRoot, codegenGoRef)
	if err != nil {
		return err
	}

	outDir := codegenGoOut
	if outDir == "" {
		outDir = filepath.Join(root, "lightwave-platform", "backend", "foundation", "store", "generated")
	}

	entities, skipped, err := gogen.LoadFrom(ctx, src, entityDir, codegenGoScope)
	if err != nil {
		return err
	}

	if len(entities) == 0 {
		color.Yellow("no tabled schemas in %s at %s (%d skipped)", entityDir, src.Describe(), len(skipped))

		for _, s := range skipped {
			fmt.Printf("  skip %s\n", s)
		}

		return nil
	}

	files, err := buildOutputs(entities)
	if err != nil {
		return err
	}

	if codegenGoCheck {
		return checkOutputs(outDir, files)
	}

	if codegenDryRun {
		for _, name := range sortedFileNames(files) {
			fmt.Printf("── %s ──\n%s\n", name, files[name])
		}

		return nil
	}

	if err := os.MkdirAll(outDir, codegenDirPerm); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	for _, name := range sortedFileNames(files) {
		dest := filepath.Join(outDir, name)
		if err := os.WriteFile(dest, []byte(files[name]), codegenFilePerm); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}

		color.Green("✓ %s", dest)
	}

	fmt.Printf("\nGenerated %s entities from %s → %s\n",
		color.CyanString("%d", len(entities)), color.CyanString("%s", src.Describe()), outDir)

	return nil
}

// buildOutputs renders the per-entity Go structs plus, unless --only is set,
// the single combined migration. Under --only only the matching struct is
// emitted and the migration is skipped — a one-entity migration would silently
// drop cross-table FKs (lightwave-cli#227).
func buildOutputs(entities []*gogen.EntitySchema) (map[string]string, error) {
	files := make(map[string]string, len(entities)+1)

	matched := 0

	for _, e := range entities {
		if codegenGoOnly != "" && entityShortName(e) != codegenGoOnly {
			continue
		}

		goSrc, err := gogen.GenerateGo(e, "store")
		if err != nil {
			return nil, err
		}

		files[e.Meta.TableName+".go"] = goSrc
		matched++
	}

	if codegenGoOnly != "" {
		if matched == 0 {
			return nil, fmt.Errorf("no entity named %q", codegenGoOnly)
		}

		color.Yellow("--only: emitting struct(s) only; %s needs the full entity set", migrationFile)

		return files, nil
	}

	migration, err := gogen.EmitMigration(entities)
	if err != nil {
		return nil, fmt.Errorf("emitting migration: %w", err)
	}

	files[migrationFile] = migration

	return files, nil
}

// entityShortName is the trailing segment of the schema_id (…/agile_artifacts/
// epic → "epic"), the token --only matches against.
func entityShortName(e *gogen.EntitySchema) string {
	parts := strings.Split(e.Meta.SchemaID, "/")

	return parts[len(parts)-1]
}

// checkOutputs compares every rendered file against disk, reporting drift.
func checkOutputs(outDir string, files map[string]string) error {
	stale := 0

	for _, name := range sortedFileNames(files) {
		got, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			color.Red("MISSING %s", name)

			stale++

			continue
		}

		if string(got) != files[name] {
			color.Red("STALE   %s", name)

			stale++
		}
	}

	if stale > 0 {
		return fmt.Errorf("%d generated file(s) are stale — run: lw codegen go", stale)
	}

	color.Green("✓ all generated files are up to date")

	return nil
}

func sortedFileNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}
