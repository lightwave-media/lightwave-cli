package uisync

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BaseProvider returns the content a file had at the pinned lightwave-ui
// version (the three-way merge base), or ok=false when the base is
// unavailable (e.g. the version tag predates the file or is missing).
// Production uses GitBase; tests inject maps.
type BaseProvider func(version, relPath string) (content []byte, ok bool, err error)

// FileOutcome classifies one file during sync.
type FileOutcome string

const (
	OutcomeUnchanged   FileOutcome = "unchanged"    // local == upstream
	OutcomeFastForward FileOutcome = "fast-forward" // only upstream changed since base → applied
	OutcomeLocalKept   FileOutcome = "local-kept"   // only local changed since base → preserved
	OutcomeConflict    FileOutcome = "conflict"     // both changed → .upstream written alongside
	OutcomeNew         FileOutcome = "new"          // upstream file absent locally → applied
)

// FileResult is one file's sync outcome.
type FileResult struct {
	Path    string
	Outcome FileOutcome
}

// Report summarizes a component's sync.
type Report struct {
	Component string
	From, To  string
	Files     []FileResult
	Conflicts int
}

// SyncComponent three-way-syncs one pinned component from the lightwave-ui
// checkout into the site. componentRel is "<category>/<kebab>" (resolve via
// ResolveComponentDir). Local customizations are never overwritten: when
// both sides changed a file since the pinned base, the upstream version is
// written alongside as <file>.upstream and the pin stays at the old version
// so a re-sync retries after manual resolution.
func SyncComponent(uiRepo, siteDir string, pin Pin, componentRel, toVersion string, base BaseProvider, dryRun bool, now time.Time) (*Report, error) {
	rel := filepath.Join("src", "components", componentRel)
	upstreamDir := filepath.Join(uiRepo, rel)
	localDir := filepath.Join(siteDir, rel)

	report := &Report{Component: pin.Name, From: pin.LightwaveUIVersion, To: toVersion}

	err := filepath.WalkDir(upstreamDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			return nil
		}

		fileRel, err := filepath.Rel(upstreamDir, path)
		if err != nil {
			return err
		}

		upstream, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		localPath := filepath.Join(localDir, fileRel)

		local, err := os.ReadFile(localPath)
		if os.IsNotExist(err) {
			report.Files = append(report.Files, FileResult{Path: localPath, Outcome: OutcomeNew})

			return writeUnlessDry(localPath, upstream, dryRun)
		}

		if err != nil {
			return err
		}

		outcome, err := classify(local, upstream, pin.LightwaveUIVersion, filepath.ToSlash(filepath.Join(rel, fileRel)), base)
		if err != nil {
			return err
		}

		report.Files = append(report.Files, FileResult{Path: localPath, Outcome: outcome})

		switch outcome {
		case OutcomeFastForward:
			return writeUnlessDry(localPath, upstream, dryRun)
		case OutcomeConflict:
			report.Conflicts++

			return writeUnlessDry(localPath+".upstream", upstream, dryRun)
		case OutcomeUnchanged, OutcomeLocalKept, OutcomeNew:
			return nil
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("syncing %s: %w", pin.Name, err)
	}

	// Pin advances only on a conflict-free sync; conflicts keep the old
	// base so re-sync after resolution still three-ways correctly.
	if report.Conflicts == 0 && !dryRun {
		lock, err := ReadLock(siteDir)
		if err != nil {
			return nil, err
		}

		lock.Upsert(Pin{Kind: pin.Kind, Name: pin.Name, LightwaveUIVersion: toVersion, SyncedAt: now.UTC().Format(time.RFC3339)})

		if err := WriteLock(siteDir, lock); err != nil {
			return nil, err
		}
	}

	return report, nil
}

// classify decides a file's outcome from local/upstream/base contents.
func classify(local, upstream []byte, baseVersion, baseRelPath string, base BaseProvider) (FileOutcome, error) {
	if bytes.Equal(local, upstream) {
		return OutcomeUnchanged, nil
	}

	baseContent, ok, err := base(baseVersion, baseRelPath)
	if err != nil {
		return "", err
	}

	if !ok {
		// No base available: cannot prove the local edit is ours alone —
		// treat as conflict rather than risk clobbering a customization.
		return OutcomeConflict, nil
	}

	localChanged := !bytes.Equal(local, baseContent)
	upstreamChanged := !bytes.Equal(upstream, baseContent)

	switch {
	case localChanged && upstreamChanged:
		return OutcomeConflict, nil
	case upstreamChanged:
		return OutcomeFastForward, nil
	default:
		return OutcomeLocalKept, nil
	}
}

func writeUnlessDry(path string, content []byte, dryRun bool) error {
	if dryRun {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return err
	}

	return os.WriteFile(path, content, filePerm)
}

// ResolveComponentDir maps a user-supplied component reference to its
// directory under src/components/. Accepted forms, tried in order:
//
//  1. an explicit path that exists ("base/buttons")
//  2. a PascalCase name whose kebab matches a directory anywhere in the
//     tree ("DataTable" → application/data-table)
//  3. a PascalCase name whose kebab matches a file — the unit is then the
//     file's directory ("Button" → base/buttons, via buttons/button.tsx,
//     the real lightwave-ui v8 layout)
func ResolveComponentDir(uiRepo, ref string) (string, error) {
	componentsRoot := filepath.Join(uiRepo, "src", "components")

	if info, err := os.Stat(filepath.Join(componentsRoot, ref)); err == nil && info.IsDir() {
		return ref, nil
	}

	want := kebab(ref)

	var found string

	err := filepath.WalkDir(componentsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}

		rel, relErr := filepath.Rel(componentsRoot, path)
		if relErr != nil {
			return relErr
		}

		if d.IsDir() && filepath.Base(rel) == want {
			found = rel

			return fs.SkipAll
		}

		if !d.IsDir() && strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)) == want {
			found = filepath.Dir(rel)

			return fs.SkipAll
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("scanning lightwave-ui components: %w", err)
	}

	if found == "" {
		return "", fmt.Errorf("component %q not found in %s (tried path, directory %q, and file %q.*)", ref, componentsRoot, want, want)
	}

	return found, nil
}
