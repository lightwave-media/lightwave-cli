// Package uicatalog lists and searches lightwave-ui components so a
// developer can match a need against existing variants before scaffolding.
package uicatalog

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Entry is one catalog record. Covers / DoesNotCover come from an optional
// sibling contract YAML (the component_contract print); they are empty when
// the print has not been authored yet. Search still matches name and path.
type Entry struct {
	Category     string   `json:"category" yaml:"category"`
	Name         string   `json:"name" yaml:"name"`
	Path         string   `json:"path" yaml:"path"`
	Provenance   string   `json:"provenance,omitempty" yaml:"provenance,omitempty"`
	Covers       []string `json:"covers,omitempty" yaml:"covers,omitempty"`
	DoesNotCover []string `json:"does_not_cover,omitempty" yaml:"does_not_cover,omitempty"`
}

type contractPrint struct {
	Covers       []string `yaml:"covers"`
	DoesNotCover []string `yaml:"does_not_cover"`
}

// ComponentsDir is <uiRepo>/src/components.
func ComponentsDir(uiRepo string) string {
	return filepath.Join(uiRepo, "src", "components")
}

// List walks every current component source under uiRepo (print_census).
// Tests, demos, stories, and internal/ helpers are not components.
func List(uiRepo string) ([]Entry, error) {
	root := ComponentsDir(uiRepo)
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, os.ErrNotExist
	}

	var entries []Entry
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() == "internal" {
				return filepath.SkipDir
			}
			return nil
		}
		if !isComponentSource(rel) {
			return nil
		}
		entry := entryFromFile(root, rel)
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// Search returns entries whose name, path, category, or covers match every
// token in need. Folder names alone are not enough: covers is the need axis.
func Search(entries []Entry, need string) []Entry {
	tokens := strings.Fields(strings.ToLower(need))
	if len(tokens) == 0 {
		return nil
	}
	var hits []Entry
	for _, entry := range entries {
		haystack := searchHaystack(entry)
		matched := true
		for _, token := range tokens {
			if !strings.Contains(haystack, token) {
				matched = false
				break
			}
		}
		if matched {
			hits = append(hits, entry)
		}
	}
	return hits
}

// Duplicate returns the first catalog entry whose export name matches name
// (case-insensitive). Used to refuse an app-local clone of an upstream
// component.
func Duplicate(entries []Entry, name string) *Entry {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return nil
	}
	for i := range entries {
		if strings.ToLower(entries[i].Name) == want {
			return &entries[i]
		}
	}
	return nil
}

func isComponentSource(rel string) bool {
	if strings.Contains(rel, "/internal/") || strings.HasPrefix(rel, "internal/") {
		return false
	}
	base := filepath.Base(rel)
	if strings.HasSuffix(base, ".test.tsx") || strings.HasSuffix(base, ".test.ts") ||
		strings.HasSuffix(base, ".demo.tsx") || strings.HasSuffix(base, ".story.tsx") {
		return false
	}
	return strings.HasSuffix(base, ".tsx") || strings.HasSuffix(base, ".ts")
}

func entryFromFile(root, rel string) Entry {
	base := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	category, _, _ := strings.Cut(rel, "/")
	entry := Entry{
		Category: category,
		Name:     pascal(base),
		Path:     rel,
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	entry.Provenance = readProvenance(abs)
	printPath := strings.TrimSuffix(abs, filepath.Ext(abs)) + ".contract.yaml"
	if raw, err := os.ReadFile(printPath); err == nil {
		var print contractPrint
		if yaml.Unmarshal(raw, &print) == nil {
			entry.Covers = print.Covers
			entry.DoesNotCover = print.DoesNotCover
		}
	}
	return entry
}

func readProvenance(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(raw), "\n")
	first = strings.TrimSpace(first)
	if strings.HasPrefix(first, "//") && strings.Contains(first, "vendored:") {
		return strings.TrimSpace(strings.TrimPrefix(first, "//"))
	}
	if strings.HasPrefix(first, "//") && strings.Contains(first, "locally authored") {
		return strings.TrimSpace(strings.TrimPrefix(first, "//"))
	}
	return "unregistered"
}

func searchHaystack(entry Entry) string {
	parts := []string{entry.Name, entry.Path, entry.Category, entry.Provenance}
	parts = append(parts, entry.Covers...)
	parts = append(parts, entry.DoesNotCover...)
	return strings.ToLower(strings.Join(parts, " "))
}

func pascal(kebab string) string {
	parts := strings.FieldsFunc(kebab, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "")
}
