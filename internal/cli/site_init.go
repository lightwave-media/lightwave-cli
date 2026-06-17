package cli

import (
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/sitegen"
	"github.com/spf13/cobra"
)

var (
	siteInitDomain    string
	siteInitName      string
	siteInitLocale    string
	siteInitComponent string
	siteInitForce     bool
)

var siteCmd = &cobra.Command{
	Use:   "site",
	Short: "Site scaffolding from the site_config stamp",
}

var siteInitCmd = &cobra.Command{
	Use:   "init --domain <domain>",
	Short: "Scaffold a site_config instance for a new domain",
	Long: `Instantiates the lightwave-core data/ui site_config stamp in the current
directory: writes site.config.yaml, performs the first lw ui add so
ui_release starts min(1)-valid, and seeds lightwave-ui.lock.

The flags are the interview answers; everything else flows from the stamp's
defaults (locale en-GB, copy from src/data/pages.ts, media from
https://media.<domain>).

Example:
  lw site init --domain joelschaeffer.site --name "Joel Schaeffer"`,
	RunE: runSiteInit,
}

func init() {
	siteInitCmd.Flags().StringVar(&siteInitDomain, "domain", "", "primary domain, no scheme (required)")
	siteInitCmd.Flags().StringVar(&siteInitName, "name", "", "site name (default: domain)")
	siteInitCmd.Flags().StringVar(&siteInitLocale, "locale", "", "BCP 47 default locale (default en-GB)")
	siteInitCmd.Flags().StringVar(&siteInitComponent, "first-component", "", "initial component to pin (default Button)")
	siteInitCmd.Flags().BoolVar(&siteInitForce, "force", false, "graduate a pre-existing vendored copy of the first component (overwrites it)")
	_ = siteInitCmd.MarkFlagRequired("domain")

	siteCmd.AddCommand(siteInitCmd)
	rootCmd.AddCommand(siteCmd)
}

func runSiteInit(cmd *cobra.Command, args []string) error {
	uiRepo, err := uiRepoPath()
	if err != nil {
		return err
	}

	version, err := uiRepoVersion(uiRepo)
	if err != nil {
		return err
	}

	siteDir, err := os.Getwd()
	if err != nil {
		return err
	}

	written, err := sitegen.Init(uiRepo, siteDir, version, sitegen.Options{
		Domain:         siteInitDomain,
		SiteName:       siteInitName,
		Locale:         siteInitLocale,
		FirstComponent: siteInitComponent,
		Force:          siteInitForce,
	}, time.Now())
	if err != nil {
		return err
	}

	for _, f := range written {
		color.Green("✓ %s", f)
	}

	color.Green("✓ %s initialized — next: lw ui add <component>, then wire src/data/pages.ts", siteInitDomain)

	return nil
}
