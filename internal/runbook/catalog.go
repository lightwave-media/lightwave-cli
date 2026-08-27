// Package runbook is the agent execution kernel for published Gruntwork
// runbooks (ADR-0039, brief 2). It looks up a stamped edition, refuses to
// apply on main, and records instance state under .tasks/.
package runbook

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	indexFile   = "__index.yaml"
	runbookMDX  = "runbook.mdx"
	worktreeDot = ".lw-worktree.yaml"
)

// Entry is one published catalog record: slug → directory under src/runbooks.
type Entry struct {
	Slug string
	Dir  string
}

type indexFileShape struct {
	Categories map[string]map[string]string `yaml:"categories"`
}

// RunbooksDir is <coreRepo>/src/runbooks.
func RunbooksDir(coreRepo string) string {
	return filepath.Join(coreRepo, "src", "runbooks")
}

// LoadIndex reads the published runbook registry. Every current print in
// the index is returned (print_census); tests must not name one slug as
// the shape contract.
func LoadIndex(coreRepo string) (map[string]Entry, error) {
	root := RunbooksDir(coreRepo)
	path := filepath.Join(root, indexFile)

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrCatalogUnreachable, path, err)
	}

	var idx indexFileShape
	if err := yaml.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("%w: parse %s: %w", ErrCatalogUnreachable, path, err)
	}

	out := make(map[string]Entry)

	for _, slugs := range idx.Categories {
		for slug, dir := range slugs {
			out[slug] = Entry{Slug: slug, Dir: dir}
		}
	}

	return out, nil
}

// Lookup returns the published entry for slug, or ErrNoMatch.
func Lookup(index map[string]Entry, slug string) (Entry, error) {
	entry, ok := index[slug]
	if !ok {
		return Entry{}, fmt.Errorf("%w: %q", ErrNoMatch, slug)
	}

	return entry, nil
}
