package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/testutil"
)

//nolint:paralleltest
func TestCheckRepoInfra_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)

	out, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir})
	require.NoError(t, err, "conformant repo must exit 0; output:\n%s", out)
	assert.Contains(t, out, "✓")
}

//nolint:paralleltest
func TestCheckRepoInfra_MissingCLAUDEmd(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)
	require.NoError(t, os.Remove(filepath.Join(dir, "CLAUDE.md")))

	out, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir})
	require.Error(t, err, "missing CLAUDE.md must exit non-zero")
	assert.Contains(t, out, "CLAUDE.md")
}

//nolint:paralleltest
func TestCheckRepoInfra_MissingDevDir(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "dev")))

	out, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir})
	require.Error(t, err, "missing dev/ must exit non-zero")
	assert.Contains(t, out, "dev/")
}

//nolint:paralleltest
func TestCheckRepoInfra_FixCreatesCLAUDEmd(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)
	require.NoError(t, os.Remove(filepath.Join(dir, "CLAUDE.md")))

	_, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir, "fix": true})
	require.NoError(t, err, "--fix with fixable-only violations must exit 0")

	content, rerr := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	require.NoError(t, rerr, "CLAUDE.md must be created by --fix")
	assert.Contains(t, string(content), "@AGENTS.md")
}

// #410: `lw create repo --kind generic` emitted two files and `lw check
// repo-infra --fix` then skipped ALL nine violations with one blanket "manual
// fix required". The two tools that exist to create and conform a repo could
// not, together, produce one that passes the check.
//
//nolint:paralleltest
func TestCheckRepoInfra_FixCreatesMissingDirs(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)

	for _, d := range []string{"dev", "docs", "tests"} {
		require.NoError(t, os.RemoveAll(filepath.Join(dir, d)))
	}

	_, err := testutil.RunHandler(t, "check.repo-infra", nil,
		map[string]any{"repo": dir, "fix": true})
	require.NoError(t, err, "missing directories are mechanical and must be fixed")

	for _, d := range []string{"dev", "docs", "tests"} {
		info, serr := os.Stat(filepath.Join(dir, d))
		require.NoError(t, serr, "%s/ must exist after --fix", d)
		assert.True(t, info.IsDir())

		// Git does not track an empty directory. Without this the fix holds on
		// the machine that ran it and the next clone fails the check again —
		// local-green / remote-red, the shape a gate is supposed to prevent.
		assert.FileExists(t, filepath.Join(dir, d, ".gitkeep"),
			"%s/ needs a placeholder or it will not survive a clone", d)
	}
}

// TestCheckRepoInfra_FixDoesNotInventContent is the more important half.
//
// AGENTS.md is the canonical agent contract and mise.toml's [tasks.ci] is what
// `mise run ci` runs. A generated stub for either would turn this check green
// while delivering nothing — and for mise.toml it is worse than nothing, since
// a repo whose ci task does nothing reports success on every push. .gitignore
// is the same trade in miniature. Fixing them would make this gate exactly what
// the repo's own README warns against: a check that reports success without
// covering its surface.
//
//nolint:paralleltest
func TestCheckRepoInfra_FixDoesNotInventContent(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)

	for _, f := range []string{"AGENTS.md", "CLAUDE.md", ".gitignore"} {
		require.NoError(t, os.Remove(filepath.Join(dir, f)))
	}

	// A mise.toml with tools but no [tasks] is the shape that matters most: the
	// file exists, so only its CONTENT is missing, and a stub would satisfy the
	// check while `mise run ci` does nothing.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "mise.toml"), []byte("[tools]\ngo = \"1.26\"\n"), 0o644))

	out, err := testutil.RunHandler(t, "check.repo-infra", nil,
		map[string]any{"repo": dir, "fix": true})
	require.Error(t, err, "content-bearing files must not be auto-satisfied")

	for _, f := range []string{"AGENTS.md", ".gitignore"} {
		assert.NoFileExists(t, filepath.Join(dir, f),
			"%s must not be invented — a stub passes the check and delivers nothing", f)
	}

	// CLAUDE.md is a pointer, so it stays unfixable while its target is gone.
	assert.NoFileExists(t, filepath.Join(dir, "CLAUDE.md"),
		"a pointer to a missing AGENTS.md is worse than an absent one")

	// Each skip says what a person has to supply. One blanket line for nine
	// violations is what #410 reported.
	assert.Contains(t, out, "canonical agent contract")
	assert.Contains(t, out, "mise run ci")
	assert.Contains(t, out, "declares no exclusions")
}

//nolint:paralleltest
func TestCheckRepoInfra_FixIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "docs")))

	flags := map[string]any{"repo": dir, "fix": true}
	_, err := testutil.RunHandler(t, "check.repo-infra", nil, flags)
	require.NoError(t, err)

	_, err = testutil.RunHandler(t, "check.repo-infra", nil, flags)
	require.NoError(t, err, "a second --fix must not fail on what it already created")
}

//nolint:paralleltest
func TestCheckRepoInfra_CLAUDEmdTooLong(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)
	// Write a fat CLAUDE.md (exceeds 32 lines)
	fat := make([]byte, 0, 4000)
	for i := 0; i < 40; i++ {
		fat = append(fat, []byte("line\n")...)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), fat, 0o644))

	out, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir})
	require.Error(t, err, "fat CLAUDE.md must exit non-zero")
	assert.Contains(t, out, "should be ≤30")
}

//nolint:paralleltest
func TestCheckRepoInfra_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte{}, 0o644))

	out, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir})
	require.Error(t, err, "empty file must exit non-zero")
	assert.Contains(t, out, "empty")
}

//nolint:paralleltest
func TestCheckRepoInfra_MiseTomlNoTasks(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mise.toml"), []byte("[tools]\nnode = \"20\"\n"), 0o644))

	out, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir})
	require.Error(t, err, "mise.toml without [tasks] must exit non-zero")
	assert.Contains(t, out, "[tasks]")
}

//nolint:paralleltest
func TestCheckRepoInfra_SchemaVersionPrinted(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)

	out, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir})
	require.NoError(t, err)
	assert.Regexp(t, `schema v\d+\.\d+\.\d+`, out)
}

//nolint:paralleltest
func TestCheckRepoInfra_JSONPreservesFailureStatus(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)
	require.NoError(t, os.Remove(filepath.Join(dir, "CLAUDE.md")))

	out, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir, "json": true})
	require.ErrorContains(t, err, "restore the stamped structure")

	var report struct {
		Violations []struct {
			Missing string `json:"missing"`
		} `json:"violations"`
		HardViolations int `json:"hard_violations"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &report), "failing JSON must remain parseable")
	assert.Equal(t, 1, report.HardViolations)
	assert.Equal(t, "CLAUDE.md", report.Violations[0].Missing)
}

//nolint:paralleltest
func TestCheckRepoInfra_CINodeWarnOnInlineSteps(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)

	// Add a Node footprint + ci.yml with inline steps (no shared workflow delegation).
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"x"}`), 0o644))
	wfDir := filepath.Join(dir, ".github", "workflows")
	require.NoError(t, os.MkdirAll(wfDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte("jobs:\n  ci:\n    steps:\n      - run: npm test\n"), 0o644))

	out, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir})
	require.NoError(t, err) // warn only — must not return an error
	assert.Contains(t, out, "warnings (advisory)")
	assert.Contains(t, out, "ci-node")
}

//nolint:paralleltest
func TestCheckRepoInfra_CINodeCleanWhenDelegates(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"x"}`), 0o644))
	wfDir := filepath.Join(dir, ".github", "workflows")
	require.NoError(t, os.MkdirAll(wfDir, 0o755))
	ciYML := "jobs:\n  ci:\n    uses: lightwave-media/.github/.github/workflows/ci-node.yml@abc123\n"
	require.NoError(t, os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte(ciYML), 0o644))

	out, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir})
	require.NoError(t, err)
	assert.NotContains(t, out, "ci-node")
}

//nolint:paralleltest
func TestCheckRepoInfra_WarnsWhenCloudCIDoesNotInvokeLocalGraph(t *testing.T) {
	dir := t.TempDir()
	scaffoldConformantRepo(t, dir)
	ciPath := filepath.Join(dir, ".github", "workflows", "ci.yml")
	require.NoError(t, os.WriteFile(ciPath, []byte(
		"jobs:\n  ci:\n    steps:\n      - run: echo duplicated-checks\n",
	), 0o644))

	out, err := testutil.RunHandler(t, "check.repo-infra", nil, map[string]any{"repo": dir})
	require.NoError(t, err, "parity starts advisory under the ratchet policy")
	assert.Contains(t, out, "ci-parity")
	assert.Contains(t, out, "mise run ci")
}

// scaffoldConformantRepo writes the minimum required files/dirs per repo-infra.yaml.
func scaffoldConformantRepo(t *testing.T, dir string) {
	t.Helper()
	for _, f := range []string{"AGENTS.md", "CLAUDE.md", "README.md", "mise.toml", ".gitignore"} {
		content := "# placeholder\n"
		if f == "CLAUDE.md" {
			content = "@AGENTS.md\n"
		}
		if f == "mise.toml" {
			content = "[tasks]\nci = \"echo ok\"\n"
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644))
	}
	for _, d := range []string{".github", "dev", "docs", "src", "tests"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, d), 0o755))
	}
	workflowDir := filepath.Join(dir, ".github", "workflows")
	require.NoError(t, os.MkdirAll(workflowDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workflowDir, "ci.yml"),
		[]byte("jobs:\n  ci:\n    steps:\n      - run: mise run ci\n"),
		0o644,
	))
}
