package git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/git"
)

// newRescueRepo builds a repo with one commit and a linked worktree, which is
// the only shape this code runs against.
func newRescueRepo(t *testing.T) (repo, worktree string) {
	t.Helper()

	repo = t.TempDir()

	run := func(dir string, args ...string) string {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)

		return string(out)
	}

	run(repo, "init", "-q", "-b", "main")
	run(repo, "config", "user.email", "t@t")
	run(repo, "config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("one\n"), 0o600))
	run(repo, "add", "-A")
	run(repo, "commit", "-qm", "first")

	// --detach, because `main` is checked out in the primary worktree and git
	// refuses to check one branch out twice. Real worktrees carry their own
	// branch; detached is the simplest shape that exercises the same paths.
	worktree = filepath.Join(t.TempDir(), "wt")
	run(repo, "worktree", "add", "-q", "--detach", worktree, "main")

	return repo, worktree
}

func TestRescueReturnsNilOnACleanWorktree(t *testing.T) {
	t.Parallel()

	repo, wt := newRescueRepo(t)

	rescue, err := git.NewGit(repo).RescueUncommitted(wt, "test")
	require.NoError(t, err)
	assert.Nil(t, rescue, "a clean worktree has nothing to rescue, and must not leave a ref behind")
}

func TestRescueCapturesModifiedTrackedFiles(t *testing.T) {
	t.Parallel()

	repo, wt := newRescueRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(wt, "tracked.txt"), []byte("two\n"), 0o600))

	g := git.NewGit(repo)

	rescue, err := g.RescueUncommitted(wt, "test")
	require.NoError(t, err)
	require.NotNil(t, rescue)

	cmd := exec.CommandContext(t.Context(), "git", "show", rescue.SHA+":tracked.txt")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Equal(t, "two\n", string(out), "the rescue must hold the WORKTREE content, not HEAD's")
}

func TestRescueCapturesUntrackedFiles(t *testing.T) {
	t.Parallel()

	repo, wt := newRescueRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(wt, "brand-new.txt"), []byte("only here\n"), 0o600))

	g := git.NewGit(repo)

	rescue, err := g.RescueUncommitted(wt, "test")
	require.NoError(t, err)
	require.NotNil(t, rescue)

	// The half that matters most and the half `git stash create` drops. An
	// untracked file exists in exactly one place; losing it loses it entirely.
	cmd := exec.CommandContext(t.Context(), "git", "show", rescue.SHA+":brand-new.txt")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "untracked file missing from rescue: %s", out)
	assert.Equal(t, "only here\n", string(out))
}

func TestRescueNeverTouchesTheSharedStashStack(t *testing.T) {
	t.Parallel()

	repo, wt := newRescueRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(wt, "tracked.txt"), []byte("two\n"), 0o600))

	_, err := git.NewGit(repo).RescueUncommitted(wt, "test")
	require.NoError(t, err)

	// The stash is shared across every worktree of a repo, and concurrent
	// sessions pop it. Growing it here would make rescue a way to steal work.
	cmd := exec.CommandContext(t.Context(), "git", "stash", "list")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Empty(t, strings.TrimSpace(string(out)), "rescue must not push onto the shared stash stack")
}

func TestRescueLeavesTheWorktreeIndexAlone(t *testing.T) {
	t.Parallel()

	repo, wt := newRescueRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(wt, "tracked.txt"), []byte("two\n"), 0o600))

	before := exec.CommandContext(t.Context(), "git", "status", "--porcelain")
	before.Dir = wt
	beforeOut, err := before.CombinedOutput()
	require.NoError(t, err, "%s", beforeOut)

	_, err = git.NewGit(repo).RescueUncommitted(wt, "test")
	require.NoError(t, err)

	after := exec.CommandContext(t.Context(), "git", "status", "--porcelain")
	after.Dir = wt
	afterOut, err := after.CombinedOutput()
	require.NoError(t, err, "%s", afterOut)

	// Staging into the real index would change what a later commit captures.
	assert.Equal(t, string(beforeOut), string(afterOut),
		"rescue must not stage anything in the worktree's own index")
}

func TestRescueAnchorsARefSoGcCannotCollectIt(t *testing.T) {
	t.Parallel()

	repo, wt := newRescueRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(wt, "tracked.txt"), []byte("two\n"), 0o600))

	g := git.NewGit(repo)

	rescue, err := g.RescueUncommitted(wt, "test")
	require.NoError(t, err)
	require.NotNil(t, rescue)
	assert.True(t, strings.HasPrefix(rescue.Ref, git.RescueRefPrefix), "ref: %s", rescue.Ref)

	// An unreferenced commit is reachable only by a SHA someone wrote down,
	// and gc is free to collect it. Prove it survives an aggressive prune.
	gc := exec.CommandContext(t.Context(), "git", "gc", "--prune=now", "--aggressive", "-q")
	gc.Dir = repo
	gcOut, err := gc.CombinedOutput()
	require.NoError(t, err, "%s", gcOut)

	check := exec.CommandContext(t.Context(), "git", "cat-file", "-e", rescue.SHA)
	check.Dir = repo
	require.NoError(t, check.Run(), "rescue commit was garbage-collected — the ref did not hold it")

	listed, err := g.ListRescues()
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, rescue.SHA, listed[0].SHA)
}
