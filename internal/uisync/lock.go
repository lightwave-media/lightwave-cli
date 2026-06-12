// Package uisync implements lw ui add/sync: copy-in distribution of
// lightwave-ui components with a provenance manifest, per the UI factory
// initiative (decisions 1, 4, 5) and the site_config stamp's
// ui_release/ComponentPin shape (lightwave-core data/ui). The manifest is
// the site↔lightwave-ui version mapping; sync three-way-diffs upstream
// changes against local edits so customizations are never clobbered.
package uisync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// LockFile is the provenance manifest at a consuming site's root. The shape
// mirrors site_config.yaml's UiRelease/ComponentPin stamp field-for-field so
// the manifest validates against the emitted SiteConfig Zod.
const LockFile = "lightwave-ui.lock"

// Pin is one copied-in component or section (ComponentPin stamp).
type Pin struct {
	Kind               string `yaml:"kind"` // "component" | "section"
	Name               string `yaml:"name"`
	LightwaveUIVersion string `yaml:"lightwave_ui_version"`
	SyncedAt           string `yaml:"synced_at"`
}

// Lock is the manifest document (UiRelease stamp).
type Lock struct {
	LightwaveUIVersion string `yaml:"lightwave_ui_version"`
	Components         []Pin  `yaml:"components"`
}

// ReadLock loads the manifest from siteDir; a missing file is an empty lock,
// not an error (first lw ui add creates it).
func ReadLock(siteDir string) (*Lock, error) {
	raw, err := os.ReadFile(filepath.Join(siteDir, LockFile))
	if os.IsNotExist(err) {
		return &Lock{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", LockFile, err)
	}

	var l Lock
	if err := yaml.Unmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", LockFile, err)
	}

	return &l, nil
}

// WriteLock persists the manifest with stable pin ordering so diffs stay
// readable in review.
func WriteLock(siteDir string, l *Lock) error {
	sort.Slice(l.Components, func(i, j int) bool {
		if l.Components[i].Kind != l.Components[j].Kind {
			return l.Components[i].Kind < l.Components[j].Kind
		}

		return l.Components[i].Name < l.Components[j].Name
	})

	header := "# lightwave-ui provenance manifest — written by `lw ui add`/`lw ui sync`.\n" +
		"# Shape: site_config.yaml ui_release (lightwave-core data/ui). Commit this file.\n"

	body, err := yaml.Marshal(l)
	if err != nil {
		return fmt.Errorf("marshalling %s: %w", LockFile, err)
	}

	return os.WriteFile(filepath.Join(siteDir, LockFile), append([]byte(header), body...), filePerm)
}

// Upsert records a pin, replacing any existing entry for the same kind+name,
// and keeps the lock-level version at the most recent sync's version.
func (l *Lock) Upsert(p Pin) {
	for i := range l.Components {
		if l.Components[i].Kind == p.Kind && l.Components[i].Name == p.Name {
			l.Components[i] = p
			l.LightwaveUIVersion = p.LightwaveUIVersion

			return
		}
	}

	l.Components = append(l.Components, p)
	l.LightwaveUIVersion = p.LightwaveUIVersion
}

// Find returns the pin for kind+name, if recorded.
func (l *Lock) Find(kind, name string) (Pin, bool) {
	for _, p := range l.Components {
		if p.Kind == kind && p.Name == name {
			return p, true
		}
	}

	return Pin{}, false
}
