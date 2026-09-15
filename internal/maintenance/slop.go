package maintenance

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxLogBytes is the rotation budget for one append-only log.
const MaxLogBytes = 5_000_000

// StaleArtefactDays is how long an artefacts/ entry may sit untouched. task_done
// archives them; a survivor past this window was abandoned.
const StaleArtefactDays = 30

// logZone is the one zone whose files grow without bound by appending. Archive
// tarballs and artefact dirs are sized by design, so only logs accrue rotation
// debt.
const logZone = "observability"

const artefactsDir = "artefacts"

// allowedRootFiles are files the baseline render or the operator legitimately
// keeps at the top level. `contract` is the per-session validity cwd-marker.
var allowedRootFiles = map[string]bool{
	"index.md": true, "state.json": true, "baseline.lock": true, "contract": true,
}

// skipDirs are vendor or VCS subtrees — never walked, never reported.
var skipDirs = map[string]bool{"node_modules": true, ".git": true}

// isToolManaged reports a directory whose internal layout belongs to another
// tool, so its emptiness is that tool's business and not drift.
//
// Git is the case that forced this. A bare repository keeps `refs/heads`,
// `refs/tags` and `objects/pack` as empty directories by design, and git breaks
// if they are removed. This host's forgejo/ holds 20 bare repos, which produced
// 60 of 74 empty-dir findings — and forgejo/ sits in a wipe_on_reset zone, so
// `prune-empty --yes` would have deleted the refs directories out of every one
// of them. A hygiene verb that bricks a git server is worse than the slop.
//
// Only the enclosing directory is matched, never the contents: a bare repo is
// `<name>.git/`, exactly as `.git/` is for a working checkout.
func isToolManaged(name string) bool {
	return skipDirs[name] || strings.HasSuffix(name, ".git")
}

// knownTransient is staging that is deliberately outside the print shape.
var knownTransient = map[string]bool{"_detached": true}

// OversizeFile is one log past the rotation budget.
type OversizeFile struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// Findings is the drift between the print and its stamp.
type Findings struct {
	UndeclaredTopLevel []string       `json:"undeclared_top_level"`
	EmptyDirs          []string       `json:"empty_dirs"`
	OversizeLogFiles   []OversizeFile `json:"oversize_log_files"`
	BrokenSymlinks     []string       `json:"broken_symlinks"`
	EmptyChannels      []string       `json:"empty_channels"`
	StaleArtefacts     []string       `json:"stale_artefacts"`
	// UnclassifiedDirs and PhantomZoneDirs are drift between the two halves of
	// the stamp itself, not between stamp and print.
	UnclassifiedDirs []string `json:"unclassified_dirs"`
	PhantomZoneDirs  []string `json:"phantom_zone_dirs"`
}

// Total counts every signal, print-side and stamp-side.
func (f *Findings) Total() int {
	return len(f.UndeclaredTopLevel) + len(f.EmptyDirs) + len(f.OversizeLogFiles) +
		len(f.BrokenSymlinks) + len(f.EmptyChannels) + len(f.StaleArtefacts) +
		len(f.UnclassifiedDirs) + len(f.PhantomZoneDirs)
}

// Scan measures one print root against one stamp.
//
// Both are parameters so the whole detector is testable against a fixture tree
// with no reference to the real ~/.lightwave. `now` is a parameter for the same
// reason: staleness that reads the wall clock cannot be tested at a boundary.
func Scan(root string, shape Shape, now time.Time) Findings {
	findings := Findings{
		UndeclaredTopLevel: findUndeclaredTopLevel(root, shape),
		EmptyDirs:          findEmptyDirs(root, shape),
		OversizeLogFiles:   findOversizeLogs(root),
		BrokenSymlinks:     findBrokenSymlinks(root),
		EmptyChannels:      findEmptyChannels(root),
		StaleArtefacts:     findStaleArtefacts(root, now),
		UnclassifiedDirs:   shape.Unclassified(),
		PhantomZoneDirs:    shape.Phantom(),
	}

	return findings
}

func entries(dir string) []os.DirEntry {
	found, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	return found
}

// findUndeclaredTopLevel reports entries the stamp never declared.
func findUndeclaredTopLevel(root string, shape Shape) []string {
	var undeclared []string

	for _, entry := range entries(root) {
		name := entry.Name()
		if knownTransient[name] {
			continue
		}

		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			continue // a dangling link is reported by findBrokenSymlinks, not here
		}

		switch {
		case info.IsDir() && !shape.TopLevel[name]:
			undeclared = append(undeclared, name+"/")
		case !info.IsDir() && !allowedRootFiles[name]:
			undeclared = append(undeclared, name)
		}
	}

	sortStrings(undeclared)

	return undeclared
}

// findEmptyDirs walks the print for directories holding nothing.
//
// A declared top-level dir that is legitimately idle-empty is not slop, so the
// declared names are excluded rather than reported every scan.
func findEmptyDirs(root string, shape Shape) []string {
	var empty []string

	var walk func(dir string)

	walk = func(dir string) {
		found := entries(dir)

		// Emptiness is judged on EVERY entry, and the tool-managed filter only
		// decides what to descend into. Judging on the filtered list conflates
		// "nothing here worth walking" with "nothing here": this host's
		// forgejo/data/repos/lightwave-media holds 20 bare repos and every one
		// is filtered, so the filtered count read 0 and the directory was
		// reported empty. Prune uses os.Remove, which would have refused — but
		// a report that names 20 live repositories as empty is wrong whether or
		// not the next step catches it.
		rel := relative(root, dir)
		if len(found) == 0 && !shape.TopLevel[rel] {
			empty = append(empty, rel)
		}

		for _, entry := range found {
			if entry.IsDir() && !isToolManaged(entry.Name()) {
				walk(filepath.Join(dir, entry.Name()))
			}
		}
	}

	for _, entry := range entries(root) {
		name := entry.Name()
		if isToolManaged(name) || knownTransient[name] || !entry.IsDir() {
			continue
		}

		walk(filepath.Join(root, name))
	}

	sortStrings(empty)

	return empty
}

// logSuffixes are the append-only shapes that accrue rotation debt.
//
// An extension test, not a size test, because "large file in observability/"
// is not the same claim as "log that needs rotating". On this host the
// unrestricted version flagged a 15MB git bundle under observability/archive/
// — the rescued-commits backup for the retired null* clones, which exists
// nowhere else. Rotating it would have gzipped it and TRUNCATED THE ORIGINAL
// to zero. A hygiene verb must not be able to destroy the archive it writes to.
var logSuffixes = []string{".jsonl", ".log", ".ndjson"}

func isAppendOnlyLog(name string) bool {
	for _, suffix := range logSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}

	return false
}

// findOversizeLogs reports append-only logs past the rotation budget.
func findOversizeLogs(root string) []OversizeFile {
	var oversize []OversizeFile

	var walk func(dir string)

	walk = func(dir string) {
		for _, entry := range entries(dir) {
			name := entry.Name()
			if isToolManaged(name) {
				continue
			}

			abs := filepath.Join(dir, name)

			info, err := entry.Info()
			if err != nil {
				continue
			}

			if entry.IsDir() {
				// Never descend into the archive: it is where rotation writes,
				// so scanning it makes rotation feed on its own output.
				if relative(root, abs) != ArchiveDir {
					walk(abs)
				}

				continue
			}

			if isAppendOnlyLog(name) && info.Size() > MaxLogBytes {
				oversize = append(oversize, OversizeFile{Path: relative(root, abs), Bytes: info.Size()})
			}
		}
	}

	zone := filepath.Join(root, logZone)
	if info, err := os.Stat(zone); err == nil && info.IsDir() {
		walk(zone)
	}

	sort.Slice(oversize, func(i, j int) bool {
		if oversize[i].Bytes != oversize[j].Bytes {
			return oversize[i].Bytes > oversize[j].Bytes // biggest first: that is the one to act on
		}

		return oversize[i].Path < oversize[j].Path
	})

	return oversize
}

// findBrokenSymlinks reports links whose target no longer resolves.
func findBrokenSymlinks(root string) []string {
	var broken []string

	var walk func(dir string)

	walk = func(dir string) {
		for _, entry := range entries(dir) {
			name := entry.Name()
			if isToolManaged(name) || knownTransient[name] {
				continue
			}

			abs := filepath.Join(dir, name)

			if entry.Type()&os.ModeSymlink != 0 {
				if _, err := os.Stat(abs); err != nil {
					broken = append(broken, relative(root, abs))
				}

				continue // never descend through a symlink
			}

			if entry.IsDir() {
				walk(abs)
			}
		}
	}

	walk(root)
	sortStrings(broken)

	return broken
}

// findEmptyChannels reports 0-byte jsonl in the log zone.
//
// An observability channel that was declared and never written is the silence
// CLAUDE.md §15 names as the blind spot: a check that never ran is
// indistinguishable from a clean run.
func findEmptyChannels(root string) []string {
	var empty []string

	zone := filepath.Join(root, logZone)

	for _, entry := range entries(zone) {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		info, err := entry.Info()
		if err == nil && info.Size() == 0 {
			empty = append(empty, logZone+"/"+entry.Name())
		}
	}

	sortStrings(empty)

	return empty
}

// findStaleArtefacts reports artefacts past the staleness window, or named as
// stale outright.
func findStaleArtefacts(root string, now time.Time) []string {
	var stale []string

	cutoff := now.AddDate(0, 0, -StaleArtefactDays)

	for _, entry := range entries(filepath.Join(root, artefactsDir)) {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		if strings.Contains(entry.Name(), ".stale-") || info.ModTime().Before(cutoff) {
			stale = append(stale, artefactsDir+"/"+entry.Name())
		}
	}

	sortStrings(stale)

	return stale
}

// relative renders a path for reporting: relative to the print root, and the
// root itself as ".".
func relative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}

	return rel
}

func sortStrings(s []string) { sort.Strings(s) }
