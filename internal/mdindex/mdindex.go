// Package mdindex builds the read-fast cache over the markdown-canonical
// artefact tree under `lightwave-media/docs/<domain>/`.
//
// Phase A is a JSON file at ~/.lightwave/state.json — simple to inspect,
// no native deps, re-runnable from clean slate. Phase B (EB-005) swaps
// this for a Postgres sync in lightwave-platform; the consumer API stays
// (or moves behind a wrapper).
//
// Re-runnability: Build() always rebuilds from scratch. Markdown is
// canonical per documentation-workflow.md §7; the cache MUST never be
// the source of truth for any decision.
package mdindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/mddocs"
)

// Entry is a denormalised artefact record optimised for v_core's
// scheduler queries (status, sprint membership, parent linkage). Body
// is intentionally excluded — point readers at the file path for
// content.
type Entry struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"` // task | user-story | epic-brief | sprint
	Domain         string `json:"domain"`
	Title          string `json:"title"`
	Status         string `json:"status,omitempty"`
	Owner          string `json:"owner,omitempty"`
	AssignedTo     string `json:"assigned_to,omitempty"`
	ParentStory    string `json:"parent_story,omitempty"`
	ParentEpic     string `json:"parent_epic,omitempty"`
	AssignedSprint string `json:"assigned_sprint,omitempty"`
	Priority       string `json:"priority,omitempty"`
	Path           string `json:"path"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

// Index is the on-disk JSON shape. Phase B will keep the consumer
// helpers (Stats, ByID, ByStatus, etc.) and back them with Postgres.
type Index struct {
	Version       int            `json:"version"`
	GeneratedAt   time.Time      `json:"generated_at"`
	LightwaveRoot string         `json:"lightwave_root"`
	Entries       []Entry        `json:"entries"`
	Stats         map[string]int `json:"stats"`
}

// Path returns the canonical on-disk index location.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(home, ".lightwave")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", d, err)
	}
	return filepath.Join(d, "state.json"), nil
}

// kindDirs is the (kind, directory) tuple set walked by Build. Kept in
// sync with mddocs.Kind.DirFor() — duplicated here so the index walker
// doesn't need a kind-to-dir reverse lookup on every iteration.
var kindDirs = []struct {
	kind string
	dir  string
}{
	{"task", "tasks"},
	{"user-story", "user-stories"},
	{"epic-brief", "epic-briefs"},
	{"sprint", "sprints"},
}

// Build walks every domain under <lightwaveRoot>/docs/ and parses every
// markdown artefact whose path matches one of the four canonical kind
// directories. Returns a fresh Index — does NOT write to disk; callers
// call Write() when they want to persist.
func Build(lightwaveRoot string) (*Index, error) {
	if lightwaveRoot == "" {
		return nil, errors.New("lightwaveRoot is required")
	}
	docs := mddocs.DocsRoot(lightwaveRoot)
	domains, err := os.ReadDir(docs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Index{Version: 1, GeneratedAt: time.Now().UTC(),
				LightwaveRoot: lightwaveRoot, Stats: map[string]int{}}, nil
		}
		return nil, err
	}

	idx := &Index{
		Version:       1,
		GeneratedAt:   time.Now().UTC(),
		LightwaveRoot: lightwaveRoot,
		Stats:         map[string]int{},
	}

	for _, dom := range domains {
		if !dom.IsDir() || strings.HasPrefix(dom.Name(), ".") {
			continue
		}
		for _, kd := range kindDirs {
			kdDir := filepath.Join(docs, dom.Name(), kd.dir)
			files, err := os.ReadDir(kdDir)
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
					continue
				}
				path := filepath.Join(kdDir, f.Name())
				a, err := mddocs.Parse(path)
				if err != nil {
					// Surface as a stats counter rather than failing the
					// whole index — a single malformed file shouldn't
					// hide healthy siblings.
					idx.Stats["parse_errors"]++
					continue
				}
				e := Entry{
					ID:             a.Frontmatter.ID,
					Kind:           kd.kind,
					Domain:         a.Frontmatter.Domain,
					Title:          a.Frontmatter.Title,
					Status:         a.Frontmatter.Status,
					Owner:          a.Frontmatter.Owner,
					AssignedTo:     a.Frontmatter.AssignedTo,
					ParentStory:    a.Frontmatter.ParentStory,
					ParentEpic:     a.Frontmatter.ParentEpic,
					AssignedSprint: a.Frontmatter.AssignedSprint,
					Path:           path,
					UpdatedAt:      a.Frontmatter.UpdatedAt,
				}
				if e.Domain == "" {
					e.Domain = dom.Name()
				}
				// priority lives in Extra (unknown field) per mddocs.
				if p, ok := a.Frontmatter.Extra["priority"]; ok {
					if s, ok := p.(string); ok {
						e.Priority = s
					}
				}
				idx.Entries = append(idx.Entries, e)
				idx.Stats[kd.kind]++
			}
		}
	}

	// Stable order — sort by (kind, ID) so diffs of state.json are clean.
	sort.Slice(idx.Entries, func(i, j int) bool {
		if idx.Entries[i].Kind != idx.Entries[j].Kind {
			return idx.Entries[i].Kind < idx.Entries[j].Kind
		}
		return idx.Entries[i].ID < idx.Entries[j].ID
	})

	idx.Stats["total"] = len(idx.Entries)
	return idx, nil
}

// Write persists the index to disk atomically (tmp + rename).
func (idx *Index) Write() (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// Load reads the persisted index. Returns (nil, nil) when no index has
// been built yet — callers can run `lw task index` to bootstrap.
func Load() (*Index, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &idx, nil
}
