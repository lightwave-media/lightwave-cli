package git_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/git"
	"github.com/stretchr/testify/require"
)

// newRepoWithWorktree builds a repo with one linked worktree at the canonical
// <repo>/.worktrees mount, named in the stamped {YYYY-MM-DD}-{ticket-slug} form
// and deliberately carrying no .lw-worktree.yaml marker — the exact shape the
// old directory scan could not see.
func newRepoWithWorktree(t *testing.T, slug, branch string) (repoRoot, worktreePath string) {
	t.Helper()

	repoRoot = t.TempDir()

	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = repoRoot

		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	runGit("init", "-q", "-b", "main")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "test")
	runGit("commit", "-q", "--allow-empty", "-m", "init")

	worktreePath = filepath.Join(repoRoot, ".worktrees", slug)
	runGit("worktree", "add", "-q", "-b", branch, worktreePath)

	return repoRoot, worktreePath
}

// The bug: inside a linked worktree, `rev-parse --show-toplevel` answers with the
// worktree, so `<repo>/.worktrees` resolved to `<worktree>/.worktrees` — a path
// that never exists. That is why `lw worktree status --current` reported "not
// inside a worktree" from inside every real worktree (#339).
func TestMainRepoRoot_IsStableFromInsideAWorktree(t *testing.T) {
	t.Parallel()

	repoRoot, worktreePath := newRepoWithWorktree(t, "2026-08-28-example", "feature/example")

	fromMain := git.NewGit(repoRoot).MainRepoRoot()
	fromWorktree := git.NewGit(worktreePath).MainRepoRoot()

	require.NotEmpty(t, fromMain, "MainRepoRoot must resolve in the primary checkout")
	require.Equal(t, fromMain, fromWorktree,
		"MainRepoRoot must answer with the primary checkout from anywhere in the repo")

	// RepoRoot is the contrast: it follows the current worktree. Asserting the
	// difference documents why MainRepoRoot has to exist at all.
	require.NotEqual(t, fromWorktree, git.NewGit(worktreePath).RepoRoot(),
		"RepoRoot differs inside a worktree — that difference is the bug MainRepoRoot fixes")
}

func TestMainRepoRoot_EmptyOutsideARepository(t *testing.T) {
	t.Parallel()

	require.Empty(t, git.NewGit(t.TempDir()).MainRepoRoot())
}

// Discovery now enumerates WorktreeList instead of scanning a directory for an
// `issue-` prefix plus a marker file. It must report a worktree that has neither.
func TestWorktreeList_FindsUnmarkedStampNamedWorktree(t *testing.T) {
	t.Parallel()

	repoRoot, _ := newRepoWithWorktree(t, "2026-08-28-arm-drift-gate", "fix/arm-drift-gate")

	worktrees, err := git.NewGit(repoRoot).WorktreeList()
	require.NoError(t, err)

	branches := make([]string, 0, len(worktrees))
	for _, w := range worktrees {
		branches = append(branches, w.Branch)
	}

	require.Contains(t, branches, "fix/arm-drift-gate",
		"a stamp-named, unmarked worktree must be discoverable — the old directory scan skipped it")
}
