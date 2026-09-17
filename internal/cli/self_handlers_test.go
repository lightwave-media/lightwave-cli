//nolint:testpackage // needs internal access to rootCmd
package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// AssembleSurface already applies the decommission policy (root.go), so this
// used to call applyDecommissions a second time — from a parallel test body,
// against the process-global rootCmd. disableSubtree writes to the commands it
// walks, so that redundant call raced every other parallel test reading the
// same tree. Going through shippedSurface instead gets the same policy applied
// once, under the assembleOnce happens-before edge.
func TestSelfSyncCmd_Registered(t *testing.T) {
	t.Parallel()

	// AssembleSurface already applies decommissions (root.go), so calling
	// applyDecommissions here re-ran it on the process-global rootCmd — a
	// second writer of a tree every other test reads in parallel. `-race
	// -shuffle=on` caught it racing IsAvailableCommand about one run in
	// eighteen. Going through assembleOnce keeps the assertion (that `self
	// sync` survives decommissioning) and makes this a pure reader.
	require.NoError(t, assembleOnce(), "assembling the shipped surface")

	self := findChild(rootCmd, "self")
	require.NotNil(t, self, "self command should be registered")
	sync := findChild(self, "sync")
	require.NotNil(t, sync, "self sync subcommand should be registered")
	require.NotNil(t, sync.RunE)
}

// gitIn runs a git command in dir and fails the test on error, returning
// trimmed stdout for callers that need it (e.g. a resolved SHA).
func gitTestIn(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), "git", args...) //nolint:gosec // test fixture, fixed argv
	cmd.Dir = dir
	// A git hook run to reach this test itself exports GIT_DIR/GIT_WORK_TREE;
	// without dropping them every git call below targets the process's real
	// repo instead of the fixture (the same defect class release-ship-
	// destination-test.sh guards against). Overriding with an EMPTY value
	// rather than removing the key is not equivalent: git treats an empty
	// GIT_WORK_TREE as still present, and refused every call here with
	// "GIT_WORK_TREE ... not allowed without specifying GIT_DIR" once GIT_DIR
	// was cleared the same way. The keys must be absent, not blank.
	cmd.Env = filterOutGitEnv(os.Environ()) // shared with the production code below

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)

	return strings.TrimSpace(string(out))
}

// TestCheckoutOriginMainWorktree_IgnoresDirtyLocalBranch is the regression
// case: `lw self sync` used to `go build` directly inside cliRoot, so it
// built whatever branch that shared checkout happened to have out — a
// feature branch, mid-edit, on any concurrent session's turn. This proves the
// isolated-worktree helper pins origin/main regardless, and leaves the
// caller's own working tree — including its UNCOMMITTED changes — untouched.
func TestCheckoutOriginMainWorktree_IgnoresDirtyLocalBranch(t *testing.T) {
	t.Parallel()

	origin := t.TempDir()
	gitTestIn(t, origin, "init", "--quiet", "--bare")

	repo := t.TempDir()
	gitTestIn(t, repo, "clone", "--quiet", origin, ".")
	gitTestIn(t, repo, "config", "user.email", "test@example.com")
	gitTestIn(t, repo, "config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "marker.txt"), []byte("main\n"), 0o600))
	gitTestIn(t, repo, "add", "marker.txt")
	gitTestIn(t, repo, "commit", "--quiet", "-m", "on main")
	gitTestIn(t, repo, "push", "--quiet", "origin", "HEAD:main")
	wantSHA := gitTestIn(t, repo, "rev-parse", "--short", "HEAD")

	// The state a live session leaves cliRoot in: a different branch, at a
	// DIFFERENT, committed HEAD — not just uncommitted dirt on top of main's
	// tip. `git describe` with no explicit ref resolves whatever cliRoot's
	// OWN HEAD is; describing bare HEAD here would silently pass a test where
	// the feature branch happens to share main's tip, exactly as this fixture
	// did on the first version of this test — which is why it did not catch
	// `describe` shipping without an explicit `origin/main` argument. Then
	// pile uncommitted dirt on top too, so both failure modes are covered.
	gitTestIn(t, repo, "checkout", "--quiet", "-b", "someones-feature-branch")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "marker.txt"), []byte("feature branch\n"), 0o600))
	gitTestIn(t, repo, "commit", "--quiet", "-am", "unrelated feature work")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "marker.txt"), []byte("UNCOMMITTED WIP\n"), 0o600))

	worktreePath, sha, describe, err := checkoutOriginMainWorktree(repo)
	require.NoError(t, err)
	t.Cleanup(func() { removeWorktreeQuiet(repo, worktreePath) })

	require.Equal(t, wantSHA, sha, "must pin the SHA actually on origin/main, not cliRoot's checked-out branch")
	// The fixture repo carries no tags, so `git describe --always` falls back
	// to the bare SHA — proving the fallback path, not just that describe ran.
	require.Equal(t, sha, describe, "describe must resolve against origin/main too, not cliRoot's bare HEAD")

	built, err := os.ReadFile(filepath.Join(worktreePath, "marker.txt"))
	require.NoError(t, err)
	require.Equal(t, "main\n", string(built),
		"worktree content must come from origin/main, not the feature branch checked out in cliRoot")

	branch := gitTestIn(t, repo, "branch", "--show-current")
	require.Equal(t, "someones-feature-branch", branch, "cliRoot's own checkout must be left exactly as found")

	dirty, err := os.ReadFile(filepath.Join(repo, "marker.txt"))
	require.NoError(t, err)
	require.Equal(t, "UNCOMMITTED WIP\n", string(dirty), "cliRoot's uncommitted change must survive untouched")
}

// TestRemoveWorktreeQuiet_CleansUpFullyAndIsIdempotent proves the teardown
// half: the worktree is gone from `git worktree list`, its directory no
// longer exists, and calling it twice — the shape a failed build followed by
// a deferred cleanup produces — does not error or leave anything behind.
func TestRemoveWorktreeQuiet_CleansUpFullyAndIsIdempotent(t *testing.T) {
	t.Parallel()

	origin := t.TempDir()
	gitTestIn(t, origin, "init", "--quiet", "--bare")

	repo := t.TempDir()
	gitTestIn(t, repo, "clone", "--quiet", origin, ".")
	gitTestIn(t, repo, "config", "user.email", "test@example.com")
	gitTestIn(t, repo, "config", "user.name", "test")
	gitTestIn(t, repo, "commit", "--quiet", "--allow-empty", "-m", "root")
	gitTestIn(t, repo, "push", "--quiet", "origin", "HEAD:main")

	worktreePath, _, _, err := checkoutOriginMainWorktree(repo)
	require.NoError(t, err)
	require.DirExists(t, worktreePath)

	removeWorktreeQuiet(repo, worktreePath)
	require.NoDirExists(t, worktreePath)
	require.NotContains(t, gitTestIn(t, repo, "worktree", "list"), worktreePath)

	require.NotPanics(t, func() { removeWorktreeQuiet(repo, worktreePath) },
		"a second removal (the deferred-cleanup shape after an already-handled failure) must not panic or hang")
}
