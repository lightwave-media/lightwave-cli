package docsfactory

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// SpecLintViolation is one validation failure on one spec/ file.
type SpecLintViolation struct {
	Path   string
	Kind   string
	Reason string
}

// SpecLintResult bundles a lint pass result. Total / Clean lets the caller
// print "N files, M clean" without reiterating the violations slice.
type SpecLintResult struct {
	Total      int
	Clean      int
	Violations []SpecLintViolation
}

// LintSpec walks <repoRoot>/spec/, validates every .md file against its
// kind's contract in spec_artifact_kinds.yaml, and returns the result.
//
// Kind discovery (in order):
//  1. `kind:` value in the file's frontmatter
//  2. Parent directory name (spec/<kind>/...) as fallback
//
// Missing-kind / unknown-kind files are reported as violations rather than
// silently skipped — silent-skip is the slop mode this verb exists to
// prevent.
func LintSpec(repoRoot string, schemas *Schemas) (*SpecLintResult, error) {
	specDir := filepath.Join(repoRoot, "spec")
	info, err := os.Stat(specDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &SpecLintResult{}, nil
		}
		return nil, fmt.Errorf("stat %s: %w", specDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", specDir)
	}

	result := &SpecLintResult{}

	walkErr := filepath.WalkDir(specDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		// Only lint .md content files; README.md is allowed but does not
		// carry a kind contract, so it's exempt.
		if !strings.HasSuffix(name, ".md") {
			return nil
		}
		if name == "README.md" {
			return nil
		}
		result.Total++

		viol := lintOne(path, specDir, schemas)
		if viol == nil {
			result.Clean++
			return nil
		}
		result.Violations = append(result.Violations, *viol)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return result, nil
}

// lintOne validates one spec/ file. Returns nil on clean; otherwise a single
// consolidated violation (multiple missing keys / sections concatenated)
// so the caller's report shows one line per file.
func lintOne(path, specDir string, schemas *Schemas) *SpecLintViolation {
	rel, _ := filepath.Rel(specDir, path)
	fm, err := ReadFrontmatter(path)
	if err != nil {
		return &SpecLintViolation{Path: rel, Reason: "malformed frontmatter: " + err.Error()}
	}

	kind := kindFromFrontmatter(fm.Map)
	if kind == "" {
		// Fallback: spec/<kind>/<file>.md
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) >= 2 {
			kind = parts[0]
		}
	}
	if kind == "" {
		return &SpecLintViolation{Path: rel, Reason: "no kind: frontmatter and no parent-dir fallback"}
	}

	spec, ok := schemas.SpecKindByName(kind)
	if !ok {
		return &SpecLintViolation{
			Path:   rel,
			Kind:   kind,
			Reason: fmt.Sprintf("unknown kind %q (not in spec_artifact_kinds.yaml)", kind),
		}
	}

	// Extension check
	ext := filepath.Ext(path)
	if spec.Extension != "" && ext != spec.Extension {
		return &SpecLintViolation{
			Path:   rel,
			Kind:   kind,
			Reason: fmt.Sprintf("extension %q does not match kind extension %q", ext, spec.Extension),
		}
	}

	// Frontmatter required keys
	missingKeys := MissingFrontmatterKeys(spec.FrontmatterRequired, fm.Map)

	// Status enum check (if the schema declares one inline; the _ref variant
	// requires loading the enum schema, deferred to a future pass).
	var badStatus string
	if len(spec.FrontmatterStatusEnum) > 0 {
		if statusVal, ok := fm.Map["status"]; ok {
			status := strings.TrimSpace(fmt.Sprintf("%v", statusVal))
			if !slices.Contains(spec.FrontmatterStatusEnum, status) {
				badStatus = fmt.Sprintf("status %q not in [%s]", status, strings.Join(spec.FrontmatterStatusEnum, ", "))
			}
		}
	}

	// Required sections
	headings := SectionHeadings(fm.Body)
	missingSections := MissingSections(spec.RequiredSections, headings)

	if len(missingKeys) == 0 && len(missingSections) == 0 && badStatus == "" {
		return nil
	}

	var reasons []string
	if len(missingKeys) > 0 {
		reasons = append(reasons, "missing frontmatter: "+strings.Join(missingKeys, ", "))
	}
	if badStatus != "" {
		reasons = append(reasons, badStatus)
	}
	if len(missingSections) > 0 {
		reasons = append(reasons, "missing sections: "+strings.Join(missingSections, ", "))
	}
	return &SpecLintViolation{
		Path:   rel,
		Kind:   kind,
		Reason: strings.Join(reasons, "; "),
	}
}
