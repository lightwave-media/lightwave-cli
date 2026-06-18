package docsfactory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SyncOptions controls how `lw docs sync` rewrites generated docs.
type SyncOptions struct {
	GeneratorVersion string // stamped into generator_version frontmatter
	DryRun           bool   // compute changes without writing
	RegenerateBodies bool   // rewrite body sections from refresh_source
}

// SyncResult names everything that did (or would) change.
type SyncResult struct {
	HeadCommit string
	Updated    []string // relative paths whose source_commit was refreshed
	Skipped    []string // generated files already at HEAD
	Authored   []string // non-generated files (untouched)
	Ignored    []string // files matched by .lwdocs.yaml ignore_globs
}

// SyncDocs refreshes the `source_commit` + `generated_at` frontmatter on
// every generated doc in <repoRoot>/docs/. v1: this is the only mutation —
// body regeneration from refresh_source is a later pass. The header refresh
// is the determinism anchor that makes `lw docs check`'s drift detection
// actionable: a CI failure on `source_commit != HEAD` is cured by running
// `lw docs sync` and committing the result.
func SyncDocs(repoRoot string, schemas *Schemas, opts SyncOptions) (*SyncResult, error) {
	head, err := gitHead(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("git rev-parse: %w (sync requires a git repo)", err)
	}
	docsDir := filepath.Join(repoRoot, "docs")
	if _, err := os.Stat(docsDir); err != nil {
		return nil, fmt.Errorf("stat %s: %w", docsDir, err)
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	if opts.GeneratorVersion == "" {
		opts.GeneratorVersion = "v0.1.0"
	}

	// Resolve override so sync honors the same ignore_globs as check —
	// otherwise sync would touch files that check ignores.
	_, override, err := readTier(repoRoot)
	if err != nil {
		return nil, err
	}
	resolved := schemas.ResolveForTier("", override)

	result := &SyncResult{HeadCommit: head}

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
			result.Ignored = append(result.Ignored, rel)
			return nil
		}

		kind := KindFromDocsPath(path, docsDir, schemas)
		if kind == "" {
			result.Authored = append(result.Authored, rel)
			return nil
		}
		dk, ok := schemas.DocKindByName(kind)
		if !ok {
			result.Authored = append(result.Authored, rel)
			return nil
		}
		if !dk.Generated() {
			result.Authored = append(result.Authored, rel)
			return nil
		}

		// Already at HEAD? Skip without writing — keeps mtime stable so
		// max_age_days isn't artificially reset.
		if readSourceCommit(path) == head {
			result.Skipped = append(result.Skipped, rel)
			return nil
		}

		if opts.DryRun {
			result.Updated = append(result.Updated, rel)
			return nil
		}

		if err := refreshHeaders(path, head, opts.GeneratorVersion, now); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		if opts.RegenerateBodies {
			if err := regenerateBody(repoRoot, path, *dk); err != nil {
				return fmt.Errorf("%s body regen: %w", rel, err)
			}
		}
		result.Updated = append(result.Updated, rel)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return result, nil
}

// refreshHeaders rewrites source_commit / generated_at / generator_version
// on path. Format dispatch on extension matches KindFromDocsPath's contract.
func refreshHeaders(path, head, genVersion, generatedAt string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	ext := filepath.Ext(path)

	switch ext {
	case ".md", ".mdx":
		fm, err := ParseFrontmatter(data)
		if err != nil {
			return err
		}
		fm.Map["source_commit"] = head
		fm.Map["generated_at"] = generatedAt
		fm.Map["generator_version"] = genVersion
		out, err := fm.Render()
		if err != nil {
			return err
		}
		return os.WriteFile(path, out, 0o644)

	case ".yaml", ".yml", ".mmd":
		return rewriteHeaderComments(path, data, ext, head, genVersion, generatedAt)

	case ".json":
		return rewriteJSONUnderscoreFields(path, data, head, genVersion, generatedAt)
	}
	return nil
}

// rewriteHeaderComments rewrites the leading `# key: value` block at the top
// of a YAML/mermaid file. Lines starting with `# ` or `%% ` whose `key:`
// matches one of the refresh keys are replaced in place; other lines pass
// through. New keys would be appended above the first non-comment line, but
// in practice every blueprint we ship already declares all three.
func rewriteHeaderComments(path string, data []byte, ext, head, genVersion, generatedAt string) error {
	commentPrefix := "# "
	if ext == ".mmd" {
		commentPrefix = "%% "
	}
	want := map[string]string{
		"source_commit":     head,
		"generator_version": genVersion,
		"generated_at":      generatedAt,
	}
	lines := strings.Split(string(data), "\n")
	inHeader := true
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inHeader && strings.HasPrefix(trimmed, commentPrefix) {
			rest := strings.TrimPrefix(trimmed, commentPrefix)
			if idx := strings.Index(rest, ":"); idx > 0 {
				key := strings.TrimSpace(rest[:idx])
				if v, ok := want[key]; ok {
					lines[i] = commentPrefix + key + ": " + v
				}
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		inHeader = false
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// rewriteJSONUnderscoreFields rewrites the `_source_commit`, `_generator_version`,
// `_generated_at` underscore-prefixed top-level fields in a JSON file. We do
// this textually (not via json.Marshal) to preserve any field order +
// formatting the file already has.
func rewriteJSONUnderscoreFields(path string, data []byte, head, genVersion, generatedAt string) error {
	want := map[string]string{
		"_source_commit":     head,
		"_generator_version": genVersion,
		"_generated_at":      generatedAt,
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		for k, v := range want {
			prefix := `"` + k + `"`
			if strings.HasPrefix(trimmed, prefix) {
				indent := line[:len(line)-len(trimmed)]
				// Preserve trailing comma if present.
				comma := ""
				if strings.HasSuffix(strings.TrimRight(line, " \t"), ",") {
					comma = ","
				}
				lines[i] = indent + prefix + ": " + `"` + v + `"` + comma
				break
			}
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}
