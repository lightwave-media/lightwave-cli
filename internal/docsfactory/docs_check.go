package docsfactory

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DocsCheckResult is the outcome of `lw docs check` for one repo.
type DocsCheckResult struct {
	Tier            string
	HeadCommit      string
	MissingRequired []string             // required kinds with no file present
	StaleByCommit   []DocsStaleEntry     // source_commit < HEAD
	StaleByAge      []DocsStaleEntry     // file mtime older than max_age_days
	ShapeViolations []DocsShapeViolation // frontmatter / sections wrong
}

// DocsStaleEntry names one stale generated doc and its current/desired SHA.
type DocsStaleEntry struct {
	Path          string
	Kind          string
	SourceCommit  string
	CurrentCommit string
	AgeDays       int
}

// DocsShapeViolation is one missing-key / missing-section / unknown-kind
// fault on a docs/ file.
type DocsShapeViolation struct {
	Path   string
	Kind   string
	Reason string
}

// Clean reports whether the result represents zero drift.
func (r *DocsCheckResult) Clean() bool {
	return len(r.MissingRequired) == 0 &&
		len(r.StaleByCommit) == 0 &&
		len(r.StaleByAge) == 0 &&
		len(r.ShapeViolations) == 0
}

// CheckDocs validates <repoRoot>/docs/ against the tier's resolved manifest
// + each present file's kind contract. It does NOT write — `lw docs sync`
// is the writer.
func CheckDocs(repoRoot string, schemas *Schemas) (*DocsCheckResult, error) {
	tier, override, err := readTier(repoRoot)
	if err != nil {
		return nil, err
	}
	resolved := schemas.ResolveForTier(tier, override)

	head, _ := gitHead(repoRoot) // empty if not a git repo — checks still run

	result := &DocsCheckResult{
		Tier:       resolved.Tier,
		HeadCommit: head,
	}

	docsDir := filepath.Join(repoRoot, "docs")
	if _, err := os.Stat(docsDir); err != nil {
		if os.IsNotExist(err) {
			// No docs/ at all — every required kind is missing.
			result.MissingRequired = append([]string{}, resolved.DocsRequired...)
			sort.Strings(result.MissingRequired)
			return result, nil
		}
		return nil, err
	}

	present := map[string]string{} // kind → path
	var allFiles []string
	walkErr := filepath.WalkDir(docsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		base := d.Name()
		if base == "README.md" || base == ".lwdocs.yaml" {
			return nil
		}
		rel, _ := filepath.Rel(docsDir, path)
		if matchesAny(rel, resolved.IgnoreGlobs) {
			return nil
		}
		allFiles = append(allFiles, path)
		kind := KindFromDocsPath(path, docsDir, schemas)
		if kind != "" {
			present[kind] = path
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	// Missing required kinds
	for _, req := range resolved.DocsRequired {
		if _, ok := present[req]; !ok {
			result.MissingRequired = append(result.MissingRequired, req)
		}
	}
	sort.Strings(result.MissingRequired)

	// For every present file: shape + freshness check
	for _, path := range allFiles {
		rel, _ := filepath.Rel(docsDir, path)
		kind := KindFromDocsPath(path, docsDir, schemas)
		if kind == "" {
			result.ShapeViolations = append(result.ShapeViolations, DocsShapeViolation{
				Path:   rel,
				Reason: "unknown kind (no matching entry in doc_artifact_kinds.yaml)",
			})
			continue
		}
		dk, ok := schemas.DocKindByName(kind)
		if !ok {
			continue
		}

		shapeViols := lintDocShape(path, rel, kind, dk)
		result.ShapeViolations = append(result.ShapeViolations, shapeViols...)

		if !dk.Generated() {
			continue
		}
		// Freshness for generated kinds
		srcCommit := readSourceCommit(path)
		if head != "" && srcCommit != "" && srcCommit != head {
			result.StaleByCommit = append(result.StaleByCommit, DocsStaleEntry{
				Path:          rel,
				Kind:          kind,
				SourceCommit:  srcCommit,
				CurrentCommit: head,
			})
		}
		if resolved.Freshness.MaxAgeDays > 0 {
			if info, err := os.Stat(path); err == nil {
				age := int(time.Since(info.ModTime()).Hours() / 24)
				if age > resolved.Freshness.MaxAgeDays {
					result.StaleByAge = append(result.StaleByAge, DocsStaleEntry{
						Path:    rel,
						Kind:    kind,
						AgeDays: age,
					})
				}
			}
		}
	}

	return result, nil
}

// KindFromDocsPath returns the kind for a docs/ file:
//  1. frontmatter `kind:` for .md/.mdx
//  2. header comment `# kind:` / `%% kind:` for .yaml/.mmd
//  3. JSON `_kind` field for .json
//  4. filename-without-extension match against a known doc kind (sad fallback)
//
// Empty result = "don't know" which the caller treats as a shape violation.
func KindFromDocsPath(path, docsDir string, schemas *Schemas) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)

	data, err := os.ReadFile(path)
	if err == nil {
		switch ext {
		case ".md", ".mdx":
			if fm, err := ParseFrontmatter(data); err == nil {
				if k := kindFromFrontmatter(fm.Map); k != "" {
					return k
				}
			}
		case ".yaml", ".yml":
			if m := HeaderCommentMap(data); m["kind"] != "" {
				return m["kind"]
			}
		case ".mmd":
			if m := HeaderCommentMap(data); m["kind"] != "" {
				return m["kind"]
			}
		case ".json":
			if m, err := ParseHeaderCommentJSON(data); err == nil && m["kind"] != "" {
				return m["kind"]
			}
		}
	}

	// Filename fallback for kinds whose canonical filename is unambiguous
	// (architecture.md, contract.yaml, dependency-graph.json, …).
	if _, ok := schemas.DocKindByName(base); ok {
		return base
	}
	// Sibling case: data-flow.mmd + data-flow.md both → data-flow.
	for _, dk := range schemas.DocKinds {
		if dk.SiblingExtension != "" && base == dk.Kind {
			return dk.Kind
		}
	}

	// Runbook subdir fallback: docs/runbook/<anything>.md → runbook.
	if rel, err := filepath.Rel(docsDir, filepath.Dir(path)); err == nil {
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) > 0 {
			if _, ok := schemas.DocKindByName(parts[0]); ok {
				return parts[0]
			}
		}
	}
	return ""
}

func lintDocShape(path, rel, kind string, dk *DocArtifactKind) []DocsShapeViolation {
	var out []DocsShapeViolation
	ext := filepath.Ext(path)
	if dk.Extension != "" && ext != dk.Extension && ext != dk.SiblingExtension {
		out = append(out, DocsShapeViolation{
			Path:   rel,
			Kind:   kind,
			Reason: fmt.Sprintf("extension %q does not match kind %q (want %s)", ext, dk.Kind, dk.Extension),
		})
	}
	data, err := os.ReadFile(path)
	if err != nil {
		out = append(out, DocsShapeViolation{Path: rel, Kind: kind, Reason: "read: " + err.Error()})
		return out
	}
	// Frontmatter checks apply only to md/mdx with a frontmatter contract.
	if (ext == ".md" || ext == ".mdx") && len(dk.FrontmatterRequired) > 0 {
		fm, err := ParseFrontmatter(data)
		if err != nil {
			out = append(out, DocsShapeViolation{Path: rel, Kind: kind, Reason: "frontmatter parse: " + err.Error()})
			return out
		}
		missing := MissingFrontmatterKeys(dk.FrontmatterRequired, fm.Map)
		if len(missing) > 0 {
			out = append(out, DocsShapeViolation{
				Path:   rel,
				Kind:   kind,
				Reason: "missing frontmatter: " + strings.Join(missing, ", "),
			})
		}
		if len(dk.RequiredSections) > 0 {
			missingSecs := MissingSections(dk.RequiredSections, SectionHeadings(fm.Body))
			if len(missingSecs) > 0 {
				out = append(out, DocsShapeViolation{
					Path:   rel,
					Kind:   kind,
					Reason: "missing sections: " + strings.Join(missingSecs, ", "),
				})
			}
		}
	}
	// Header-comment contract for non-md kinds (yaml/mmd/json).
	if len(dk.HeaderCommentsRequired) > 0 {
		var m map[string]string
		switch ext {
		case ".yaml", ".yml", ".mmd":
			m = HeaderCommentMap(data)
		case ".json":
			m, _ = ParseHeaderCommentJSON(data)
		}
		var missing []string
		for _, k := range dk.HeaderCommentsRequired {
			if v, ok := m[k]; !ok || strings.TrimSpace(v) == "" {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			out = append(out, DocsShapeViolation{
				Path:   rel,
				Kind:   kind,
				Reason: "missing header comments: " + strings.Join(missing, ", "),
			})
		}
	}
	return out
}

// readSourceCommit pulls `source_commit:` from a docs/ file's frontmatter or
// header-comment block. Returns "" when not found — distinct from "stale"
// per the freshness rules.
func readSourceCommit(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	ext := filepath.Ext(path)
	switch ext {
	case ".md", ".mdx":
		if fm, err := ParseFrontmatter(data); err == nil {
			if v, ok := fm.Map["source_commit"]; ok {
				return strings.TrimSpace(fmt.Sprintf("%v", v))
			}
		}
	case ".yaml", ".yml", ".mmd":
		m := HeaderCommentMap(data)
		return m["source_commit"]
	case ".json":
		if m, err := ParseHeaderCommentJSON(data); err == nil {
			return m["source_commit"]
		}
	}
	return ""
}

// readTier resolves the repo's tier. Order: .lwdocs.yaml `tier:` field, then
// CLAUDE.md `tier:` declaration, then "cli" as a safe default for unknown
// repos (matches the most permissive manifest).
func readTier(repoRoot string) (string, *LWDocsOverride, error) {
	ov, err := LoadOverride(repoRoot)
	if err != nil {
		return "", nil, err
	}
	if ov != nil && ov.Tier != "" {
		return ov.Tier, ov, nil
	}
	if tier := tierFromClaudeMD(repoRoot); tier != "" {
		return tier, ov, nil
	}
	return "cli", ov, nil
}

func tierFromClaudeMD(repoRoot string) string {
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			t := strings.TrimSpace(line)
			if rest, ok := strings.CutPrefix(t, "tier:"); ok {
				return strings.TrimSpace(rest)
			}
		}
	}
	return ""
}

// matchesAny reports whether rel matches any glob in patterns. We try both
// the full path and the basename — so `*.md` works for nested files and
// `vendor/**` works for whole subtrees (via path/match semantics; Go's
// path/match doesn't support `**`, so we ALSO match against any path
// component as a prefix to approximate it).
func matchesAny(rel string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, rel); ok {
			return true
		}
		if ok, _ := filepath.Match(p, filepath.Base(rel)); ok {
			return true
		}
		// Prefix approximation of `**/foo` and `foo/**`.
		if strings.HasSuffix(p, "/**") {
			prefix := strings.TrimSuffix(p, "/**")
			if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
				return true
			}
		}
	}
	return false
}

// gitHead returns the abbreviated HEAD SHA of repoRoot. Empty string when
// not a git repo or git is missing — callers treat this as "skip commit
// freshness check" rather than as an error.
func gitHead(repoRoot string) (string, error) {
	c := exec.Command("git", "-C", repoRoot, "rev-parse", "--short", "HEAD")
	out, err := c.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
