package docsfactory_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/docsfactory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDocSchemas builds enough Schemas to drive CheckDocs + SyncDocs tests
// without depending on a checked-out lightwave-core.
func fakeDocSchemas() *docsfactory.Schemas {
	architecture := docsfactory.DocArtifactKind{
		Kind:                "architecture",
		Extension:           ".md",
		FrontmatterRequired: []string{"kind", "generator_version", "source_commit", "generated_at"},
		RequiredSections:    []string{"Components", "Boundaries", "Cross-Repo Dependencies"},
		RefreshSource:       []docsfactory.RefreshEntry{{Kind: "file_tree", Roots: []string{"internal/"}}},
	}
	contract := docsfactory.DocArtifactKind{
		Kind:                   "contract",
		Extension:              ".yaml",
		HeaderCommentsRequired: []string{"kind", "generator_version", "source_commit", "generated_at"},
		RefreshSource:          []docsfactory.RefreshEntry{{Kind: "go_exports"}},
	}
	runbook := docsfactory.DocArtifactKind{
		Kind:                "runbook",
		Extension:           ".md",
		FrontmatterRequired: []string{"kind", "owner"},
		RequiredSections:    []string{"Symptoms", "Diagnosis", "Remediation", "Escalation"},
	}
	depGraph := docsfactory.DocArtifactKind{
		Kind:                   "dependency-graph",
		Extension:              ".json",
		HeaderCommentsRequired: []string{"kind", "source_commit"},
		RefreshSource:          []docsfactory.RefreshEntry{{Kind: "package_managers"}},
	}
	return &docsfactory.Schemas{
		DocKinds: []docsfactory.DocArtifactKind{architecture, contract, runbook, depGraph},
		Manifest: docsfactory.RepoDocManifest{
			// Defaults so ResolveForTier has something to return when no tier
			// matches. CLI tier requires the three generated kinds; runbook
			// is authored-only and gets shape-linted but not freshness-checked.
			Freshness: docsfactory.FreshnessPolicy{MaxAgeDays: 0},
		},
	}
}

// initGit makes repoRoot a git repo with one commit so gitHead() resolves.
// Returns the short SHA of that commit.
func initGit(t *testing.T, repoRoot string) string {
	t.Helper()
	for _, c := range [][]string{
		{"init"},
		{"config", "user.email", "test@lightwave.test"},
		{"config", "user.name", "Test"},
		{"commit", "-m", "init", "--allow-empty"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repoRoot}, c...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", c, string(out))
	}
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--short", "HEAD").Output()
	require.NoError(t, err)
	return string(out[:len(out)-1])
}

// writeLwdocs makes the override declare a tier with one required kind so
// CheckDocs has predictable expectations independent of manifest tier table.
func writeLwdocs(t *testing.T, repo, tier string, required []string) {
	t.Helper()
	body := "tier: " + tier + "\n"
	if len(required) > 0 {
		body += "docs_required: [" + joinQuoted(required) + "]\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".lwdocs.yaml"), []byte(body), 0o644))
}

func joinQuoted(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func TestCheckDocs_KnownGood_NoDrift(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	sha := initGit(t, repo)

	writeLwdocs(t, repo, "cli", []string{"architecture"})
	writeFile(t, filepath.Join(repo, "docs", "architecture.md"), `---
generated_at: 2026-06-11T00:00:00Z
generator_version: v0.1.0
kind: architecture
source_commit: `+sha+`
---

# Architecture

## Components
A.

## Boundaries
B.

## Cross-Repo Dependencies
C.
`)

	res, err := docsfactory.CheckDocs(repo, fakeDocSchemas())
	require.NoError(t, err)
	assert.True(t, res.Clean(), "expected clean, got %+v", res)
}

func TestCheckDocs_MissingRequired_Fires(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	initGit(t, repo)
	writeLwdocs(t, repo, "cli", []string{"architecture", "contract"})
	// docs/ exists but is empty.
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "docs"), 0o755))

	res, err := docsfactory.CheckDocs(repo, fakeDocSchemas())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"architecture", "contract"}, res.MissingRequired)
	assert.False(t, res.Clean())
}

func TestCheckDocs_StaleSourceCommit_Fires(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	sha := initGit(t, repo)
	writeLwdocs(t, repo, "cli", []string{"architecture"})
	writeFile(t, filepath.Join(repo, "docs", "architecture.md"), `---
generated_at: 2026-06-11T00:00:00Z
generator_version: v0.1.0
kind: architecture
source_commit: deadbeef
---

# Architecture

## Components
.

## Boundaries
.

## Cross-Repo Dependencies
.
`)
	res, err := docsfactory.CheckDocs(repo, fakeDocSchemas())
	require.NoError(t, err)
	require.Len(t, res.StaleByCommit, 1)
	assert.Equal(t, "architecture", res.StaleByCommit[0].Kind)
	assert.Equal(t, "deadbeef", res.StaleByCommit[0].SourceCommit)
	assert.Equal(t, sha, res.StaleByCommit[0].CurrentCommit)
}

func TestCheckDocs_AuthoredRunbook_ShapeLinted(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	initGit(t, repo)
	writeLwdocs(t, repo, "cli", []string{"runbook"})
	// Missing 'owner' frontmatter + 'Escalation' section.
	writeFile(t, filepath.Join(repo, "docs", "runbook", "outage.md"), `---
kind: runbook
---

# Outage

## Symptoms
A.

## Diagnosis
B.

## Remediation
C.
`)
	res, err := docsfactory.CheckDocs(repo, fakeDocSchemas())
	require.NoError(t, err)
	// runbook has no refresh_source, so no freshness check fires —
	// only the two shape violations (one per failing rule, on purpose;
	// a combined message would obscure which rule failed).
	assert.Empty(t, res.StaleByCommit)
	require.Len(t, res.ShapeViolations, 2)
	var reasons string
	for _, v := range res.ShapeViolations {
		reasons += v.Reason + "\n"
	}
	assert.Contains(t, reasons, "owner")
	assert.Contains(t, reasons, "Escalation")
}

func TestCheckDocs_ContractYAML_HeaderCommentsChecked(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	sha := initGit(t, repo)
	writeLwdocs(t, repo, "cli", []string{"contract"})
	// Good contract.yaml — header comments include every required key.
	writeFile(t, filepath.Join(repo, "docs", "contract.yaml"), `# kind: contract
# generator_version: v0.1.0
# source_commit: `+sha+`
# generated_at: 2026-06-11T00:00:00Z

cli_verbs: []
`)
	res, err := docsfactory.CheckDocs(repo, fakeDocSchemas())
	require.NoError(t, err)
	assert.True(t, res.Clean(), "expected clean, got %+v", res)
}

func TestSyncDocs_RefreshesSourceCommit(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	sha := initGit(t, repo)
	writeLwdocs(t, repo, "cli", []string{"architecture"})
	writeFile(t, filepath.Join(repo, "docs", "architecture.md"), `---
generated_at: bootstrap
generator_version: bootstrap
kind: architecture
source_commit: bootstrap
---

# Architecture

## Components
.

## Boundaries
.

## Cross-Repo Dependencies
.
`)

	res, err := docsfactory.SyncDocs(repo, fakeDocSchemas(), docsfactory.SyncOptions{})
	require.NoError(t, err)
	assert.Equal(t, sha, res.HeadCommit)
	require.Len(t, res.Updated, 1)

	// Verify the file was actually rewritten.
	updated, err := os.ReadFile(filepath.Join(repo, "docs", "architecture.md"))
	require.NoError(t, err)
	assert.Contains(t, string(updated), "source_commit: "+sha)
	assert.NotContains(t, string(updated), "source_commit: bootstrap")

	// Re-running is idempotent — second pass skips the now-fresh file.
	res2, err := docsfactory.SyncDocs(repo, fakeDocSchemas(), docsfactory.SyncOptions{})
	require.NoError(t, err)
	assert.Empty(t, res2.Updated)
	require.Len(t, res2.Skipped, 1)
}

func TestSyncDocs_DryRunDoesNotWrite(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	initGit(t, repo)
	writeLwdocs(t, repo, "cli", []string{"architecture"})
	writeFile(t, filepath.Join(repo, "docs", "architecture.md"), `---
generated_at: bootstrap
generator_version: bootstrap
kind: architecture
source_commit: bootstrap
---

# A
`)
	res, err := docsfactory.SyncDocs(repo, fakeDocSchemas(), docsfactory.SyncOptions{DryRun: true})
	require.NoError(t, err)
	require.Len(t, res.Updated, 1)

	// File body unchanged.
	updated, err := os.ReadFile(filepath.Join(repo, "docs", "architecture.md"))
	require.NoError(t, err)
	assert.Contains(t, string(updated), "source_commit: bootstrap")
}

func TestSyncDocs_AuthoredFilesUntouched(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	initGit(t, repo)
	writeLwdocs(t, repo, "cli", []string{"runbook"})
	writeFile(t, filepath.Join(repo, "docs", "runbook", "outage.md"), `---
kind: runbook
owner: joel
---

# Outage
`)
	res, err := docsfactory.SyncDocs(repo, fakeDocSchemas(), docsfactory.SyncOptions{})
	require.NoError(t, err)
	assert.Empty(t, res.Updated)
	require.Len(t, res.Authored, 1)
}
