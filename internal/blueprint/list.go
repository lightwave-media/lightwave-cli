package blueprint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// CatalogEntry is one discoverable blueprint or template.
type CatalogEntry struct {
	Kind string // "blueprint" or "template"
	Slug string
	Dir  string // path relative to the registry root
}

// List returns all active entries from blueprints/__index.yaml and
// templates/__index.yaml under lightwaveRoot.
func List(lightwaveRoot string) ([]CatalogEntry, error) {
	boilerplate := filepath.Join(lightwaveRoot, "lightwave-core", "src", "boilerplate")

	entries := make([]CatalogEntry, 0)

	// Blueprints: flat map slug → dir.
	bpData, err := os.ReadFile(filepath.Join(boilerplate, "blueprints", "__index.yaml"))
	if err != nil {
		return nil, fmt.Errorf("blueprint: read blueprints/__index.yaml: %w", err)
	}

	var bpIdx struct {
		Blueprints map[string]string `yaml:"blueprints"`
	}
	if err := yaml.Unmarshal(bpData, &bpIdx); err != nil {
		return nil, fmt.Errorf("blueprint: parse blueprints/__index.yaml: %w", err)
	}

	for _, slug := range sortedKeys(bpIdx.Blueprints) {
		entries = append(entries, CatalogEntry{Kind: "blueprint", Slug: slug, Dir: bpIdx.Blueprints[slug]})
	}

	// Templates: nested map family → slug → path.
	tmplData, err := os.ReadFile(filepath.Join(boilerplate, "templates", "__index.yaml"))
	if err != nil {
		return nil, fmt.Errorf("blueprint: read templates/__index.yaml: %w", err)
	}

	var tmplIdx struct {
		Templates map[string]map[string]string `yaml:"templates"`
	}
	if err := yaml.Unmarshal(tmplData, &tmplIdx); err != nil {
		return nil, fmt.Errorf("blueprint: parse templates/__index.yaml: %w", err)
	}

	for _, family := range sortedKeys(tmplIdx.Templates) {
		slugMap := tmplIdx.Templates[family]
		for _, slug := range sortedKeys(slugMap) {
			entries = append(entries, CatalogEntry{Kind: "template", Slug: family + "/" + slug, Dir: slugMap[slug]})
		}
	}

	return entries, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
