package docsfactory_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/docsfactory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSchemas builds a minimal Schemas value sufficient for spec-lint tests
// without depending on a checked-out lightwave-core. Keeps the test
// portable across CI/dev machines per CLAUDE.md test conventions.
func fakeSchemas() *docsfactory.Schemas {
	return &docsfactory.Schemas{
		SpecKinds: []docsfactory.SpecArtifactKind{
			{
				Kind:                  "prd",
				Extension:             ".md",
				FrontmatterRequired:   []string{"kind", "status", "owner", "created_at"},
				FrontmatterStatusEnum: []string{"draft", "accepted", "superseded", "archived"},
				RequiredSections:      []string{"Problem", "Audience", "Success Metrics", "Out of Scope"},
			},
			{
				Kind:                "adr",
				Extension:           ".md",
				FrontmatterRequired: []string{"kind", "status", "decided_at"},
				RequiredSections:    []string{"Context", "Decision", "Consequences"},
			},
		},
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func TestLintSpec_KnownGood_IsSilent(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "spec", "prd", "0001-good.md"), `---
kind: prd
status: draft
owner: joel
created_at: 2026-06-11
---

# PRD-0001

## Problem
A.

## Audience
B.

## Success Metrics
C.

## Out of Scope
D.
`)

	res, err := docsfactory.LintSpec(repo, fakeSchemas())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Total)
	assert.Equal(t, 1, res.Clean)
	assert.Empty(t, res.Violations)
}

func TestLintSpec_KnownBad_Fires(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	// Missing `owner` + `created_at` frontmatter, missing `Out of Scope`
	// section, invalid status. Three violations rolled into one report line.
	writeFile(t, filepath.Join(repo, "spec", "prd", "0001-bad.md"), `---
kind: prd
status: bogus
---

# PRD-0001

## Problem
A.

## Audience
B.

## Success Metrics
C.
`)

	res, err := docsfactory.LintSpec(repo, fakeSchemas())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Total)
	assert.Equal(t, 0, res.Clean)
	require.Len(t, res.Violations, 1)
	v := res.Violations[0]
	assert.Equal(t, "prd", v.Kind)
	assert.Contains(t, v.Reason, "missing frontmatter")
	assert.Contains(t, v.Reason, "owner")
	assert.Contains(t, v.Reason, "created_at")
	assert.Contains(t, v.Reason, "status \"bogus\"")
	assert.Contains(t, v.Reason, "missing sections")
	assert.Contains(t, v.Reason, "Out of Scope")
}

func TestLintSpec_UnknownKind_Fires(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "spec", "mystery", "0001-x.md"), `---
kind: mystery
---

# X
`)
	res, err := docsfactory.LintSpec(repo, fakeSchemas())
	require.NoError(t, err)
	require.Len(t, res.Violations, 1)
	assert.Contains(t, res.Violations[0].Reason, "unknown kind")
}

func TestLintSpec_KindFallsBackToDir(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	// No frontmatter at all — parent dir says adr/, but ADR requires
	// kind+status+decided_at + Context/Decision/Consequences. So lint
	// fires with reasons referencing the inferred kind.
	writeFile(t, filepath.Join(repo, "spec", "adr", "0001-x.md"), "# ADR with no frontmatter\n")
	res, err := docsfactory.LintSpec(repo, fakeSchemas())
	require.NoError(t, err)
	require.Len(t, res.Violations, 1)
	assert.Equal(t, "adr", res.Violations[0].Kind)
}

func TestLintSpec_ReadmesAreSkipped(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "spec", "README.md"), "# spec/\n")
	writeFile(t, filepath.Join(repo, "spec", "prd", "README.md"), "# prd/\n")
	res, err := docsfactory.LintSpec(repo, fakeSchemas())
	require.NoError(t, err)
	assert.Equal(t, 0, res.Total)
	assert.Empty(t, res.Violations)
}

func TestLintSpec_NoSpecDir_IsClean(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	res, err := docsfactory.LintSpec(repo, fakeSchemas())
	require.NoError(t, err)
	assert.Equal(t, 0, res.Total)
	assert.Empty(t, res.Violations)
}
