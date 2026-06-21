package cli_test

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
	assert.Contains(t, out, "v1.3.0")
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
}
