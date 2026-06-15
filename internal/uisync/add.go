package uisync

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	dirPerm  = 0o755
	filePerm = 0o644
)

// Add copies a component's directory from the lightwave-ui checkout into the
// consuming site and records its provenance pin. ref accepts a path under
// src/components or a PascalCase name (see ResolveComponentDir). The
// destination existing without force is an error: updates go through Sync
// (three-way), never through blind re-copy — blind re-copy is exactly the
// clobbering failure mode this tool exists to end.
//
// Unless noDeps is set, Add then walks the copied files' imports and pulls in
// every transitive dependency — sibling components (pinned too) and shared src
// files like @/utils/cx — so the copy-in builds in the consumer without the
// manual dependency-chasing that one-component-at-a-time copy used to require.
func Add(uiRepo, siteDir, ref, version string, force, noDeps bool, now time.Time) ([]string, error) {
	unit, err := ResolveComponentDir(uiRepo, ref)
	if err != nil {
		return nil, err
	}

	rel := filepath.Join("src", "components", unit)
	src := filepath.Join(uiRepo, rel)
	dst := filepath.Join(siteDir, rel)

	if _, err := os.Stat(dst); err == nil && !force {
		return nil, fmt.Errorf("%s already exists — use `lw ui sync` to update it (three-way), or --force to overwrite", dst)
	}

	copied, err := copyTree(src, dst)
	if err != nil {
		return nil, err
	}

	lock, err := ReadLock(siteDir)
	if err != nil {
		return nil, err
	}

	lock.Upsert(Pin{
		Kind:               "component",
		Name:               ref,
		LightwaveUIVersion: version,
		SyncedAt:           now.UTC().Format(time.RFC3339),
	})

	if !noDeps {
		w := newDepWalker(uiRepo, siteDir, version, now, lock)
		w.visitedUnit[unit] = true // the named component is already copied + pinned

		if err := w.scanUnit(unit); err != nil {
			return nil, fmt.Errorf("resolving dependencies of %s: %w", ref, err)
		}

		copied = append(copied, w.copied...)
	}

	if err := WriteLock(siteDir, lock); err != nil {
		return nil, err
	}

	return copied, nil
}

// copyTree copies every regular file under src to the same layout under dst,
// returning site-relative paths of the files written.
func copyTree(src, dst string) ([]string, error) {
	var copied []string

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
			return err
		}

		if err := os.WriteFile(target, raw, filePerm); err != nil {
			return err
		}

		copied = append(copied, target)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("copying component tree: %w", err)
	}

	return copied, nil
}

// kebab converts a PascalCase component name to its kebab-case directory
// (DataTable → data-table). Already-kebab input passes through unchanged.
func kebab(name string) string {
	var b strings.Builder

	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}

			b.WriteRune(r - 'A' + 'a')

			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}
