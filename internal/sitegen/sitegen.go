// Package sitegen implements lw site init: instantiate the site_config
// stamp (lightwave-core data/ui) for a new domain. It writes the
// site.config.yaml instance and performs the first component add so
// ui_release starts min(1)-valid (a scaffolded site with zero pinned
// components is semantically incoherent, per the stamp).
package sitegen

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/uisync"
	"gopkg.in/yaml.v3"
)

const filePerm = 0o644

// ConfigFile is the site_config instance at the site root.
const ConfigFile = "site.config.yaml"

// Options are the interview answers lw site init collects (flags today;
// richer prompting can layer on top without changing this shape).
type Options struct {
	Domain         string
	SiteName       string
	Locale         string
	FirstComponent string // initial lw ui add ref; keeps ui_release min(1)-valid
}

// SiteConfig mirrors the data/ui site_config stamp's instance shape
// field-for-field. ui_release is a snapshot of lightwave-ui.lock taken at
// init; the lock is the operational source between syncs (reconciliation
// lands with the Run 4 data layer).
type SiteConfig struct {
	Domain      string      `yaml:"domain"`
	SiteName    string      `yaml:"site_name"`
	Tenant      *string     `yaml:"tenant"`
	Locale      string      `yaml:"locale"`
	Brand       Brand       `yaml:"brand"`
	Nav         []NavItem   `yaml:"nav"`
	Pages       []string    `yaml:"pages"`
	DataSources DataSources `yaml:"data_sources"`
	UIRelease   uisync.Lock `yaml:"ui_release"`
}

// Brand mirrors the Brand sub-schema.
type Brand struct {
	LogoLight *string           `yaml:"logo_light"`
	LogoDark  *string           `yaml:"logo_dark"`
	Tokens    map[string]string `yaml:"tokens"`
}

// NavItem mirrors the NavItem sub-schema.
type NavItem struct {
	Label string `yaml:"label"`
	Href  string `yaml:"href"`
}

// DataSources mirrors the DataSources sub-schema.
type DataSources struct {
	CopySource string `yaml:"copy_source"`
	MediaBase  string `yaml:"media_base"`
}

// Init scaffolds a site_config instance in siteDir: first component add
// (creating lightwave-ui.lock), then the config embedding that lock as the
// ui_release snapshot. Refuses to overwrite an existing config — re-running
// init over a configured site is almost certainly a mistake.
func Init(uiRepo, siteDir, uiVersion string, opts Options, now time.Time) ([]string, error) {
	cfgPath := filepath.Join(siteDir, ConfigFile)
	if _, err := os.Stat(cfgPath); err == nil {
		return nil, fmt.Errorf("%s already exists — this site is already initialized", ConfigFile)
	}

	if opts.SiteName == "" {
		opts.SiteName = opts.Domain
	}

	if opts.Locale == "" {
		opts.Locale = "en-GB"
	}

	if opts.FirstComponent == "" {
		opts.FirstComponent = "Button"
	}

	copied, err := uisync.Add(uiRepo, siteDir, opts.FirstComponent, uiVersion, false, now)
	if err != nil {
		return nil, fmt.Errorf("pinning first component %s: %w", opts.FirstComponent, err)
	}

	lock, err := uisync.ReadLock(siteDir)
	if err != nil {
		return nil, err
	}

	cfg := SiteConfig{
		Domain:   opts.Domain,
		SiteName: opts.SiteName,
		Tenant:   nil,
		Locale:   opts.Locale,
		Brand:    Brand{Tokens: map[string]string{}},
		Nav:      []NavItem{},
		Pages:    []string{""},
		DataSources: DataSources{
			CopySource: "src/data/pages.ts",
			MediaBase:  "https://media." + opts.Domain,
		},
		UIRelease: *lock,
	}

	body, err := yaml.Marshal(&cfg)
	if err != nil {
		return nil, fmt.Errorf("marshalling %s: %w", ConfigFile, err)
	}

	header := "# site_config instance — shape: lightwave-core data/ui site_config (ADR-0006).\n" +
		"# Written by `lw site init`. ui_release is a snapshot of lightwave-ui.lock\n" +
		"# (the operational pin source, updated by lw ui add/sync). Copy and imagery\n" +
		"# flow from data_sources — never hardcoded in components.\n"

	if err := os.WriteFile(cfgPath, append([]byte(header), body...), filePerm); err != nil {
		return nil, err
	}

	written := append([]string{cfgPath}, copied...)
	written = append(written, filepath.Join(siteDir, uisync.LockFile))

	return written, nil
}
