//nolint:testpackage // exercises unexported propagate helpers
package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPropagateRepo builds a throwaway repo with one commit, for the dirty-tree
// guard. Hermetic: global and system git config are neutralised so a
// developer's ~/.gitconfig cannot change the result.
func newPropagateRepo(t *testing.T, ctx context.Context) string { //nolint:revive // ctx after t is the testing idiom here
	t.Helper()

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()

		c := exec.CommandContext(ctx, "git", args...)
		c.Dir = dir
		c.Env = append(c.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)

		out, err := c.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	run("init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o644))
	run("add", "-A")
	run("commit", "-qm", "chore: seed")

	return dir
}

func TestWorktreeIsDirtyCleanTree(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	dirty, err := worktreeIsDirty(ctx, newPropagateRepo(t, ctx))
	require.NoError(t, err)
	assert.False(t, dirty, "a freshly committed tree is not dirty")
}

// TestWorktreeIsDirtyCountsUntracked pins the case that matters most: a whole
// new package sitting untracked in a worktree is uncommitted work, and it is
// the easiest kind to destroy. One such worktree in this repo carries ~3.2k
// untracked lines today.
func TestWorktreeIsDirtyCountsUntracked(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := newPropagateRepo(t, ctx)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.go"), []byte("package x\n"), 0o644))

	dirty, err := worktreeIsDirty(ctx, dir)
	require.NoError(t, err)
	assert.True(t, dirty,
		"untracked files must count as dirty — propagate would otherwise rebase over them")
}

func TestWorktreeIsDirtyCountsModified(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := newPropagateRepo(t, ctx)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("edited"), 0o644))

	dirty, err := worktreeIsDirty(ctx, dir)
	require.NoError(t, err)
	assert.True(t, dirty)
}

// TestPropagateWorktreeSkipsDirtyBeforeDryRun pins that the guard runs BEFORE
// the dry-run exit. A preview whose job is to say what a real run would do must
// report the skip; reporting "would rebase" for a tree that will be skipped is
// the misleading half of the bug.
//
// It also pins the message: git refuses to rebase a dirty tree on its own, but
// that refusal used to surface as "rebase conflict — resolve manually", sending
// the reader hunting for conflicts that do not exist.
func TestPropagateWorktreeSkipsDirtyBeforeDryRun(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := newPropagateRepo(t, ctx)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("wip"), 0o644))

	res := propagateWorktree(ctx, dir, "some-branch", "", false /* apply */)

	assert.True(t, res.Blocked, "a dirty worktree must be blocked, not silently rebased")
	assert.Contains(t, res.Detail, "uncommitted changes")
	assert.NotContains(t, res.Detail, "rebase conflict",
		"the reason is a dirty tree, not a conflict — saying conflict sends the "+
			"reader looking for something that is not there")
}
