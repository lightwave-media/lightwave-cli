package uisync

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// importSpecRe captures the module specifier of every ES import/export/dynamic
// import in a TS/JS source file: the quoted string following `from`, a bare
// `import "x"` side-effect import, or `import("x")`. RE2 has no backtracking, so
// a leading `import {` simply fails this branch and the engine matches the
// trailing `from "x"` instead — no duplicate, no miss.
var importSpecRe = regexp.MustCompile(`(?:\bfrom|\bimport|\bexport)\s*\(?\s*['"]([^'"]+)['"]`)

// sourceExts are the import-bearing file extensions whose imports we follow.
var sourceExts = []string{".ts", ".tsx", ".jsx", ".js", ".mts", ".cts"}

// indexFiles are the directory-index module resolution targets, tried after
// extension candidates.
var indexFiles = []string{"index.ts", "index.tsx", "index.jsx", "index.js"}

// componentUnitDepth is the segment count of a component unit path under
// src/components: category/name (e.g. base/tooltip).
const componentUnitDepth = 2

// parseImports returns the de-duplicated module specifiers imported by a source
// file's contents, in first-seen order.
func parseImports(content []byte) []string {
	matches := importSpecRe.FindAllSubmatch(content, -1)

	seen := map[string]bool{}
	out := make([]string, 0, len(matches))

	for _, m := range matches {
		s := string(m[1])
		if s == "" || seen[s] {
			continue
		}

		seen[s] = true

		out = append(out, s)
	}

	return out
}

func isSourceFile(path string) bool {
	ext := filepath.Ext(path)
	for _, e := range sourceExts {
		if ext == e {
			return true
		}
	}

	return false
}

// resolveFileUnder maps a module path (relative to root, extension optional) to
// the slash-form relative path of the actual file on disk, honouring the
// TypeScript resolution order (exact, then extensions, then directory index).
// Returns "" when nothing resolves.
func resolveFileUnder(root, rel string) string {
	rel = filepath.ToSlash(rel)

	candidates := make([]string, 0, 1+len(sourceExts)+len(indexFiles))
	if filepath.Ext(rel) != "" {
		candidates = append(candidates, rel)
	}

	for _, ext := range sourceExts {
		candidates = append(candidates, rel+ext)
	}

	for _, idx := range indexFiles {
		candidates = append(candidates, rel+"/"+idx)
	}

	for _, c := range candidates {
		p := filepath.Join(root, filepath.FromSlash(c))
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return c
		}
	}

	return ""
}

// depWalker copies a component's transitive dependencies — sibling components
// and shared src files (utils, hooks, icons) reachable through its imports —
// into the consuming site, so a copied-in component builds without the manual
// dependency-chasing that `lw ui add` used to demand.
type depWalker struct {
	uiRepo  string
	siteDir string
	version string
	now     time.Time
	lock    *Lock

	visitedUnit map[string]bool // component unit dir (slash) → handled
	visitedFile map[string]bool // src-relative file (slash) → handled
	copied      []string        // site paths written this walk
}

func newDepWalker(uiRepo, siteDir, version string, now time.Time, lock *Lock) *depWalker {
	return &depWalker{
		uiRepo:      uiRepo,
		siteDir:     siteDir,
		version:     version,
		now:         now,
		lock:        lock,
		visitedUnit: map[string]bool{},
		visitedFile: map[string]bool{},
	}
}

// scanUnit follows the imports of every source file in a component unit that is
// already present in the lightwave-ui checkout, pulling in anything missing.
func (w *depWalker) scanUnit(unit string) error {
	unitDir := filepath.Join(w.uiRepo, "src", "components", filepath.FromSlash(unit))

	return filepath.WalkDir(unitDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !isSourceFile(path) {
			return nil
		}

		return w.scanFile(path)
	})
}

// scanFile resolves every import of a single source file at the given absolute
// path within the lightwave-ui checkout.
func (w *depWalker) scanFile(absPath string) error {
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}

	for _, spec := range parseImports(raw) {
		if err := w.resolveSpec(spec, absPath); err != nil {
			return err
		}
	}

	return nil
}

// resolveSpec turns one import specifier into a copy action. `@/x` maps to
// `src/x` (the consumer's `@/` → `src` alias); a relative specifier is resolved
// against the importer's directory. Bare specifiers (node_modules) are skipped.
func (w *depWalker) resolveSpec(spec, importerPath string) error {
	var modRel string // module path relative to src/, slash-form, no extension assumed

	switch {
	case strings.HasPrefix(spec, "@/"):
		modRel = strings.TrimPrefix(spec, "@/")
	case strings.HasPrefix(spec, "./"), strings.HasPrefix(spec, "../"):
		abs := filepath.Join(filepath.Dir(importerPath), filepath.FromSlash(spec))

		rel, err := filepath.Rel(filepath.Join(w.uiRepo, "src"), abs)
		if err != nil {
			return nil //nolint:nilerr // unresolvable relative path → not our dependency
		}

		modRel = filepath.ToSlash(rel)
		if strings.HasPrefix(modRel, "../") {
			return nil // escapes the src tree → external, leave it
		}
	default:
		return nil // bare specifier → node_modules, skip
	}

	if strings.HasPrefix(modRel, "components/") {
		return w.resolveComponent(strings.TrimPrefix(modRel, "components/"))
	}

	if fileRel := resolveFileUnder(filepath.Join(w.uiRepo, "src"), modRel); fileRel != "" {
		return w.ensureLooseFile(fileRel)
	}

	return nil
}

// resolveComponent maps a components-relative import to the right granularity:
// a directory component (category/name/…) is copied and pinned as a unit; a
// loose single-file component (category/name.tsx, e.g. foundations/dot-icon) is
// copied as a file without a pin, since it has no directory to three-way sync.
func (w *depWalker) resolveComponent(compRel string) error {
	componentsRoot := filepath.Join(w.uiRepo, "src", "components")

	if info, err := os.Stat(filepath.Join(componentsRoot, filepath.FromSlash(compRel))); err == nil && info.IsDir() {
		return w.ensureUnit(compRel)
	}

	fileRel := resolveFileUnder(componentsRoot, compRel)
	if fileRel == "" {
		return nil // unresolved → likely a type-only or external re-export
	}

	dirParts := strings.Split(filepath.ToSlash(filepath.Dir(fileRel)), "/")
	if len(dirParts) >= componentUnitDepth {
		// File lives inside a category/name unit (possibly nested deeper):
		// copy the whole unit so its internals come along.
		return w.ensureUnit(dirParts[0] + "/" + dirParts[1])
	}

	// File sits directly under a category → standalone component file.
	return w.ensureLooseFile("components/" + fileRel)
}

// ensureUnit copies a component unit directory into the site, pins it, and
// recurses into its imports. A unit already present in the site is assumed
// complete and left untouched (its own deps came in when it was added).
func (w *depWalker) ensureUnit(unit string) error {
	if w.visitedUnit[unit] {
		return nil
	}

	w.visitedUnit[unit] = true

	src := filepath.Join(w.uiRepo, "src", "components", filepath.FromSlash(unit))
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return nil //nolint:nilerr // not a real unit dir → nothing to copy
	}

	dst := filepath.Join(w.siteDir, "src", "components", filepath.FromSlash(unit))
	if info, err := os.Stat(dst); err == nil && info.IsDir() {
		return nil // already present — leave local copy as-is
	}

	copied, err := copyTree(src, dst)
	if err != nil {
		return err
	}

	w.copied = append(w.copied, copied...)

	w.lock.Upsert(Pin{
		Kind:               "component",
		Name:               unit,
		LightwaveUIVersion: w.version,
		SyncedAt:           w.now.UTC().Format(time.RFC3339),
	})

	return w.scanUnit(unit)
}

// ensureLooseFile copies one shared src file (a util, hook, icon, or
// category-level component file) into the site and recurses into its imports.
// srcRel is relative to src/. Loose files are not pinned: the lock tracks
// component units, and these come along as their dependencies.
func (w *depWalker) ensureLooseFile(srcRel string) error {
	srcRel = filepath.ToSlash(srcRel)

	if w.visitedFile[srcRel] {
		return nil
	}

	w.visitedFile[srcRel] = true

	dst := filepath.Join(w.siteDir, "src", filepath.FromSlash(srcRel))
	if _, err := os.Stat(dst); err == nil {
		return nil // already present
	}

	src := filepath.Join(w.uiRepo, "src", filepath.FromSlash(srcRel))
	if err := copyFile(src, dst); err != nil {
		return err
	}

	w.copied = append(w.copied, dst)

	if isSourceFile(src) {
		return w.scanFile(src)
	}

	return nil
}

// copyFile copies a single regular file, creating parent directories.
func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return err
	}

	return os.WriteFile(dst, raw, filePerm)
}

// pluralize returns the naive English plural of a kebab segment, enough to
// bridge singular component names to their pluralized directories
// (badge → badges, box → boxes, category → categories).
func pluralize(s string) string {
	switch {
	case s == "":
		return s
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "z"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	case strings.HasSuffix(s, "y") && len(s) >= 2 && !isVowel(s[len(s)-2]):
		return s[:len(s)-1] + "ies"
	default:
		return s + "s"
	}
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}

	return false
}

// componentUnit returns the category/name unit prefix of a components-relative
// path, or "" when the path has fewer than two segments.
func componentUnit(compRel string) string {
	parts := strings.Split(filepath.ToSlash(compRel), "/")
	if len(parts) < componentUnitDepth {
		return ""
	}

	return strings.Join(parts[:componentUnitDepth], "/")
}

// findExportedSymbol scans the component tree for a file exporting the named
// PascalCase symbol and returns its unit dir — the last-resort resolver so a
// name resolves even when it matches neither a directory nor a file basename
// (Badge → base/badges, whose file is badges.tsx).
func findExportedSymbol(componentsRoot, symbol string) string {
	if symbol == "" {
		return ""
	}

	q := regexp.QuoteMeta(symbol)
	declRe := regexp.MustCompile(`(?m)^\s*export\s+(?:default\s+)?(?:async\s+)?(?:const|let|var|function|class|interface|type|enum)\s+` + q + `\b`)
	listRe := regexp.MustCompile(`export\s*(?:type\s*)?\{[^}]*\b` + q + `\b[^}]*\}`)

	var found string

	_ = filepath.WalkDir(componentsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}

		if d.IsDir() || !isSourceFile(path) {
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil //nolint:nilerr // unreadable file → keep scanning
		}

		if !declRe.Match(raw) && !listRe.Match(raw) {
			return nil
		}

		rel, _ := filepath.Rel(componentsRoot, path)

		if unit := componentUnit(rel); unit != "" {
			found = unit
		} else {
			found = filepath.ToSlash(filepath.Dir(rel))
		}

		return fs.SkipAll
	})

	return found
}
