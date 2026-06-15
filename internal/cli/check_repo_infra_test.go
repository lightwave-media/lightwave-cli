package cli_test

// All tests are intentionally serial — RunHandler swaps os.Stdout globally.
// Same constraint as check_schema_test.go. No t.Parallel() here.

import (
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

// scaffoldConformantRepo writes the minimum required files/dirs per repo-infra.yaml v1.2.0.
func scaffoldConformantRepo(t *testing.T, dir string) {
	t.Helper()
	for _, f := range []string{"AGENTS.md", "CLAUDE.md", "README.md", "mise.toml", ".gitignore"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte("# placeholder\n"), 0o644))
	}
	for _, d := range []string{".github", "dev", "docs"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, d), 0o755))
	}
}
