//nolint:testpackage // drives worktreeRoot/managedWorktreeRoots/ExitCode, unexported or package-scoped
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorktreeRootIsTheCanonicalHome pins worktree_home_policy.yaml v2.0.0.
//
// v1.1.0 put the canonical root at <repo>/.worktrees and this file was built on
// that. v2.0.0 (CORE-0051) moved it to ~/.worktrees with layout {repo}/{slug}
// and put the repo-relative form into `forbidden_roots` — so `lw worktree
// create` was allocating into a root the current policy forbids, while the
// worktree hook enforced v2.0.0 and denied everything else. #497.
func TestWorktreeRootIsTheCanonicalHome(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	root, err := worktreeRoot()
	require.NoError(t, err, "these tests run inside a git repo")

	assert.True(t, filepath.IsAbs(root))
	assert.Equal(t, filepath.Join(home, canonicalWorktreeHome), filepath.Dir(root),
		"new worktrees belong under ~/.worktrees/<repo>, not under the repo")

	// The canonical home is itself named ".worktrees"; what v2.0.0 forbids is
	// the REPO-RELATIVE one. So the claim to pin is that the root sits outside
	// the repo, not that the name differs.
	repoRoot := filepath.Dir(root)
	assert.NotEqual(t, filepath.Join(repoRoot, canonicalWorktreeHome), root,
		"the repo-relative .worktrees is forbidden_roots in v2.0.0")
	assert.Equal(t, home, filepath.Dir(filepath.Dir(root)),
		"the canonical root is operator-ruled, directly under $HOME")
}

// TestManagedRootsSeeTheCanonicalHome is the listing half of the same bug.
//
// `lw worktree list` reported "no worktrees found" in a repo where
// `git worktree list` showed three, because every managed root was
// repo-relative and every real worktree lives under ~/.worktrees/<repo>.
func TestManagedRootsSeeTheCanonicalHome(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	const repoRoot = "/Users/someone/dev/lightwave-plugin"
	roots := managedWorktreeRoots(repoRoot)

	canonical := filepath.Join(home, canonicalWorktreeHome, "lightwave-plugin")
	assert.Contains(t, roots, canonical, "the canonical home must be discoverable")

	assert.True(t, isManagedWorktree(repoRoot, filepath.Join(canonical, "some-branch")),
		"a worktree at the canonical path must be recognised as managed")
}

// TestManagedRootsStillSeeDrainingRoots — discovery outlives legality. A root
// leaving the policy is the moment its worktrees most need to stay visible; a
// list that hides them reports a clean estate that is not clean.
func TestManagedRootsStillSeeDrainingRoots(t *testing.T) {
	t.Parallel()

	const repoRoot = "/Users/someone/dev/lightwave-plugin"

	for _, dir := range []string{harnessWorktreeDir, legacyRepoWorktreeDir} {
		assert.True(t,
			isManagedWorktree(repoRoot, filepath.Join(repoRoot, dir, "old-tree")),
			"a tree under %s must still be listed so it can be drained", dir)
	}
}

// TestExitCodeSurvivesWrapping is the silence half.
//
// `create` documents exit 2 and 3 in its own --help and produced them with
// os.Exit() inside RunE — which killed the process before cobra returned and
// before main printed anything. Measured: `lw worktree create 26` exited 2 with
// no output at all, which the reporter read as "exited 0 and did nothing".
func TestExitCodeSurvivesWrapping(t *testing.T) {
	t.Parallel()

	cause := errors.New("branch violates naming convention")
	err := error(exitCodeError{code: 2, err: cause})

	code, ok := ExitCode(err)
	require.True(t, ok, "the code must be recoverable by main")
	assert.Equal(t, 2, code)

	assert.Equal(t, cause.Error(), err.Error(), "the REASON survives, which is the point")
	assert.ErrorIs(t, err, cause, "and the cause stays inspectable")
}

// TestPlainErrorsCarryNoExitCode — without this, ExitCode could return a code
// unconditionally and every failure would exit with it.
func TestPlainErrorsCarryNoExitCode(t *testing.T) {
	t.Parallel()

	code, ok := ExitCode(errors.New("something ordinary"))

	assert.False(t, ok)
	assert.Zero(t, code)
}
