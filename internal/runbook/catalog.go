// Package runbook is the agent execution kernel for published Gruntwork
// runbooks (ADR-0039, brief 2). It looks up a stamped edition, refuses to
// apply on main, and records instance state under .tasks/.
package runbook

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// readIndex parses the registry file. LoadIndex and LoadCatalog both need it,
// and two copies of the parse would be two places to get ErrCatalogUnreachable
// wrong.
func readIndex(coreRepo string) (indexFileShape, error) {
	path := filepath.Join(RunbooksDir(coreRepo), indexFile)

	raw, err := os.ReadFile(path)
	if err != nil {
		return indexFileShape{}, fmt.Errorf("%w: %s: %w", ErrCatalogUnreachable, path, err)
	}

	var idx indexFileShape
	if err := yaml.Unmarshal(raw, &idx); err != nil {
		return indexFileShape{}, fmt.Errorf("%w: parse %s: %w", ErrCatalogUnreachable, path, err)
	}

	return idx, nil
}

// LoadIndex reads the published runbook registry. Every current print in
// the index is returned (print_census); tests must not name one slug as
// the shape contract.
func LoadIndex(coreRepo string) (map[string]Entry, error) {
	idx, err := readIndex(coreRepo)
	if err != nil {
		return nil, err
	}

	out := make(map[string]Entry)

	for _, slugs := range idx.Categories {
		for slug, dir := range slugs {
			out[slug] = Entry{Slug: slug, Dir: dir}
		}
	}

	return out, nil
}

// Record is one catalog entry enriched for discovery: the category the registry
// files it under, the description from its own front matter, and whether it can
// actually be run.
//
// Reachable is not decoration. The registry and the filesystem disagree: on
// lightwave-core@1426a0d, 56 slugs are indexed and 51 have a runbook.mdx. A
// listing that reported registry membership would advertise five runbooks whose
// `start` fails on a missing edition — the caller would go debug their checkout
// for a runbook that was never written. So the catalog reports what can run, and
// the five are surfaced deliberately rather than silently filtered.
type Record struct {
	Slug        string `json:"slug"`
	Category    string `json:"category"`
	Dir         string `json:"dir"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	Reachable   bool   `json:"reachable"`
}

// frontMatter is the subset of a runbook.mdx header the catalog surfaces.
type frontMatter struct {
	Description string `yaml:"description"`
	Status      string `yaml:"status"`
}

// LoadCatalog returns every registered runbook, sorted by category then slug.
//
// Unlike LoadIndex this stats each entry and reads its front matter, so it is
// the discovery path rather than the execution path — start/apply keep using
// LoadIndex and pay neither cost.
func LoadCatalog(coreRepo string) ([]Record, error) {
	idx, err := readIndex(coreRepo)
	if err != nil {
		return nil, err
	}

	root := RunbooksDir(coreRepo)

	var out []Record

	for category, slugs := range idx.Categories {
		for slug, dir := range slugs {
			rec := Record{Slug: slug, Category: category, Dir: dir}

			mdx := filepath.Join(root, dir, runbookMDX)
			if raw, readErr := os.ReadFile(mdx); readErr == nil {
				rec.Reachable = true
				fm := parseFrontMatter(raw)
				rec.Description = fm.Description
				rec.Status = fm.Status
			}

			out = append(out, rec)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}

		return out[i].Slug < out[j].Slug
	})

	return out, nil
}

// parseFrontMatter reads the leading `---` YAML block of a runbook.mdx.
//
// A runbook with no front matter, or malformed front matter, yields a zero
// value rather than an error: the file's presence is what makes the runbook
// reachable, and refusing to list the whole catalog because one description is
// unparsable would trade a small gap for a total outage.
func parseFrontMatter(raw []byte) frontMatter {
	const marker = "---"

	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, marker+"\n") {
		return frontMatter{}
	}

	end := strings.Index(text[len(marker)+1:], "\n"+marker)
	if end < 0 {
		return frontMatter{}
	}

	var fm frontMatter
	if err := yaml.Unmarshal([]byte(text[len(marker)+1:len(marker)+1+end]), &fm); err != nil {
		return frontMatter{}
	}

	return fm
}

// Search returns the records whose slug, category, description or status
// contains query, case-insensitively. An empty query matches everything, so
// `search ""` degrades to `list` rather than to nothing.
func Search(records []Record, query string) []Record {
	q := strings.ToLower(strings.TrimSpace(query))

	var hits []Record

	for _, r := range records {
		haystack := strings.ToLower(strings.Join(
			[]string{r.Slug, r.Category, r.Description, r.Status}, "\x00"))
		if strings.Contains(haystack, q) {
			hits = append(hits, r)
		}
	}

	return hits
}

// Lookup returns the published entry for slug, or ErrNoMatch.
func Lookup(index map[string]Entry, slug string) (Entry, error) {
	entry, ok := index[slug]
	if !ok {
		return Entry{}, fmt.Errorf("%w: %q", ErrNoMatch, slug)
	}

	return entry, nil
}
