//nolint:testpackage // exercises unexported installHooks / verifyHooks
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/testutil/gitfixture"
)

// TestMain isolates the process because handlers under test shell out to git
// with the process environment. Under a hook's GIT_DIR, installHooks wrote
// core.hooksPath into the hook's repo instead of the fixture.
func TestMain(m *testing.M) {
	gitfixture.Isolate()
	os.Exit(m.Run())
}

// #411. `lw hooks install` shelled out to `pre-commit install`, which was wrong
// in both directions a repo can be in, and measurably so:
//
//   - core.hooksPath UNSET: wrote .git/hooks, left core.hooksPath unset, exited
//     0. The result violates repo-infra.yaml v1.4.0, which puts hooks in
//     dev/hooks — so the command reported success for a repo it had just made
//     non-conforming.
//   - core.hooksPath SET, the normal state for six of seven first-party repos:
//     "Cowardly refusing to install hooks with core.hooksPath set", exit 1. The
//     verb could not work in precisely the repos that already conform.
//
// The sharper part is internal: repoStatusFor, in the same file, already
// resolved hooks THROUGH core.hooksPath. install and doctor disagreed about
// where hooks live.

func hookGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitfixture.Env()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func newHookRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	hookGit(t, dir, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".pre-commit-config.yaml"), []byte("repos: []\n"), 0o600))

	return dir
}

func hooksPathOf(t *testing.T, repo string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "-C", repo, "config", "core.hooksPath")
	cmd.Env = gitfixture.Env()
	out, _ := cmd.Output()

	return strings.TrimSpace(string(out))
}

func TestInstallHooks_WiresTheStampedPath(t *testing.T) {
	t.Parallel()

	repo := newHookRepo(t)
	require.NoError(t, installHooks(t.Context(), repo))

	assert.Equal(t, stampedHooksDir, hooksPathOf(t, repo),
		"repo-infra.yaml v1.4.0 puts hooks in dev/hooks, wired via core.hooksPath")

	for _, name := range []string{hookPreCommit, hookPrePush} {
		info, err := os.Stat(filepath.Join(repo, stampedHooksDir, name))
		require.NoError(t, err, "%s must exist after install", name)
		assert.NotZero(t, info.Mode().Perm()&0o111,
			"a hook git cannot execute is a hook git silently skips")
	}
}

// TestInstallHooks_WritesNothingToGitHooks pins the wrong destination. Anything
// in .git/hooks is ignored outright once core.hooksPath is set, so writing
// there is not a harmless extra — it is the appearance of an install.
func TestInstallHooks_WritesNothingToGitHooks(t *testing.T) {
	t.Parallel()

	repo := newHookRepo(t)
	require.NoError(t, installHooks(t.Context(), repo))

	entries, err := os.ReadDir(filepath.Join(repo, ".git", "hooks"))
	require.NoError(t, err)

	for _, e := range entries {
		assert.True(t, strings.HasSuffix(e.Name(), ".sample"),
			"%s was written to .git/hooks, which git ignores under core.hooksPath", e.Name())
	}
}

// TestInstallHooks_WorksWhereItUsedToRefuse is the regression. With
// core.hooksPath already set, `pre-commit install` refused and the command
// exited 1 — in the repos that already conform to the stamp.
func TestInstallHooks_WorksWhereItUsedToRefuse(t *testing.T) {
	t.Parallel()

	repo := newHookRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, stampedHooksDir), 0o755))
	hookGit(t, repo, "config", "core.hooksPath", stampedHooksDir)

	require.NoError(t, installHooks(t.Context(), repo),
		"a repo that already conforms must be installable")
}

// TestInstallHooks_PreservesExistingHooks is why this fills gaps instead of
// rewriting. conform.sh writes a richer pre-push with opt-in act and review
// phases; clobbering it would make this the second installer fighting the
// first, which #411 names as worse than one.
func TestInstallHooks_PreservesExistingHooks(t *testing.T) {
	t.Parallel()

	repo := newHookRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, stampedHooksDir), 0o755))

	bespoke := filepath.Join(repo, stampedHooksDir, hookPrePush)
	require.NoError(t, os.WriteFile(bespoke, []byte("#!/bin/sh\n# BESPOKE\n"), 0o755)) //nolint:gosec // a hook must be executable

	require.NoError(t, installHooks(t.Context(), repo))

	body, err := os.ReadFile(bespoke) //nolint:gosec // path built in this test
	require.NoError(t, err)
	assert.Contains(t, string(body), "BESPOKE", "an existing hook must never be overwritten")
}

func TestInstallHooks_IsIdempotent(t *testing.T) {
	t.Parallel()

	repo := newHookRepo(t)
	require.NoError(t, installHooks(t.Context(), repo))
	require.NoError(t, installHooks(t.Context(), repo), "a second run must not fail")
}

// TestInstallHooks_RespectsAnOperatorsHooksPath — a path already chosen is a
// deliberate choice, not drift to correct.
func TestInstallHooks_RespectsAnOperatorsHooksPath(t *testing.T) {
	t.Parallel()

	repo := newHookRepo(t)
	hookGit(t, repo, "config", "core.hooksPath", "custom/hooks")

	require.NoError(t, installHooks(t.Context(), repo))

	assert.Equal(t, "custom/hooks", hooksPathOf(t, repo))
	assert.FileExists(t, filepath.Join(repo, "custom/hooks", hookPreCommit))
}

// TestVerifyHooks_RejectsANonExecutableHook drives the verification step
// against a known-bad subject.
//
// Without this the verify path is only ever run on input it accepts, and a
// check that cannot fail is the defect CLAUDE.md section 15 records for the
// multi-repo self-heal: it "reported installed for hooks it had not installed".
// Printing an action is not evidence the action happened.
func TestVerifyHooks_RejectsANonExecutableHook(t *testing.T) {
	t.Parallel()

	repo := newHookRepo(t)
	require.NoError(t, installHooks(t.Context(), repo))

	// git silently skips a hook it cannot execute, so this must not read as
	// installed.
	require.NoError(t, os.Chmod(filepath.Join(repo, stampedHooksDir, hookPrePush), 0o644))

	err := verifyHooks(t.Context(), repo, stampedHooksDir)
	require.Error(t, err, "a non-executable hook must not report as installed")
	assert.Contains(t, err.Error(), "not executable")
}

// TestVerifyHooks_RejectsAMissingHook is the other half: the file simply is not
// there.
func TestVerifyHooks_RejectsAMissingHook(t *testing.T) {
	t.Parallel()

	repo := newHookRepo(t)
	require.NoError(t, installHooks(t.Context(), repo))
	require.NoError(t, os.Remove(filepath.Join(repo, stampedHooksDir, hookPreCommit)))

	require.Error(t, verifyHooks(t.Context(), repo, stampedHooksDir),
		"a missing hook must not report as installed")
}
