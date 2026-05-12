package mddocs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotFound is returned when no artefact matches the given ID.
var ErrNotFound = errors.New("artefact not found")

// FindByID locates an artefact by its ID (e.g. "T-0001", "US-003").
// When domain is empty, every domain under DocsRoot is searched.
// When kind is empty, it is inferred from the ID prefix.
//
// Files are matched as `<id>-*.md` or exactly `<id>.md` to allow optional
// kebab-case slugs after the ID (the established convention in
// `lightwave-media/docs/software/`).
//
// Returns ErrNotFound (wrapped) if no file matches.
func FindByID(lightwaveRoot, domain, id string) (*Artefact, error) {
	kind, ok := KindFromID(id)
	if !ok {
		return nil, fmt.Errorf("unrecognised artefact id %q: expected prefix T-/US-/EB-/SPR-/DDD-/IP-", id)
	}

	docs := DocsRoot(lightwaveRoot)
	var domainsToSearch []string
	if domain != "" {
		domainsToSearch = []string{domain}
	} else {
		entries, err := os.ReadDir(docs)
		if err != nil {
			return nil, fmt.Errorf("read docs root %s: %w", docs, err)
		}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				domainsToSearch = append(domainsToSearch, e.Name())
			}
		}
		sort.Strings(domainsToSearch)
	}

	for _, d := range domainsToSearch {
		dir := filepath.Join(docs, d, kind.DirFor())
		path, err := matchOne(dir, id)
		if err != nil {
			return nil, err
		}
		if path != "" {
			return Parse(path)
		}
	}

	return nil, fmt.Errorf("%w: %s (kind=%s, domain=%q)", ErrNotFound, id, kind, domain)
}

// matchOne returns the path to the artefact in `dir` whose filename
// starts with `<id>-` or equals `<id>.md`. Returns "" if none found.
// Multiple matches are reported as an error — IDs are supposed to be
// unique within a (domain, kind) tuple.
func matchOne(dir, id string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", dir, err)
	}

	prefix := id + "-"
	exact := id + ".md"
	var matches []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		if name == exact || strings.HasPrefix(name, prefix) {
			matches = append(matches, filepath.Join(dir, name))
		}
	}

	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("multiple artefacts match id %q in %s: %v", id, dir, matches)
	}
}
