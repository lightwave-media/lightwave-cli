//nolint:testpackage // exercises the unexported workspace-git helpers directly
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The defect these pin (#444): every one of these helpers used to be asked a
// question in a directory where git cannot answer — paths.lightwave_root, which
// is the PARENT of the repos in the flat sibling layout, not a repo. git wrote
// a usage error to stderr, the old runGitDiff merged stderr into stdout and
// discarded the error, and the callers read that text as a diff. `lw check
// locks` therefore reported uv.lock and pnpm-lock.yaml dirty on every machine —
// including machines where no repository has ever tracked uv.lock — and `lw
// check git` reported the tree dirty from git's exit 129.
//
// The tests marked REGRESSION fail against the pre-fix code. They are the point
// of this file; the rest keep the corrected behaviour honest.

// newGitRepo creates an initialised repository with one commit and returns its
// path. Identity and hooks are set locally so the test does not depend on the
// developer's global git config, and so the repo-tracked hooks these repos
// install (core.hooksPath=dev/hooks) cannot be inherited into a fixture.
func newGitRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir

		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	run("init", "--initial-branch=main")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "test")
	run("config", "commit.gpgsign", "false")
	run("config", "core.hooksPath", filepath.Join(dir, ".no-hooks"))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("fixture\n"), 0o600))
	run("add", "README.md")
	run("commit", "-m", "initial")

	return dir
}

// commitFile writes and commits a file in repo.
func commitFile(t *testing.T, repo, name, body string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(body), 0o600))

	for _, args := range [][]string{{"add", name}, {"commit", "-m", "add " + name}} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = repo

		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
}

// TestRunGitDiff_OutsideAWorkTreeErrorsAndReturnsNoOutput is THE regression pin.
//
// Pre-fix this returned git's stderr with a nil error, which is the entire
// defect: the caller's `TrimSpace(out) != ""` test then saw "warning: Not a git
// repository…" and called it a diff.
func TestRunGitDiff_OutsideAWorkTreeErrorsAndReturnsNoOutput(t *testing.T) {
	t.Parallel()

	out, err := runGitDiff(t.Context(), t.TempDir(), "uv.lock")

	require.Error(t, err, "git cannot diff outside a work tree; saying so is the fix")
	assert.Empty(t, out,
		"stderr must never be returned as diff output — that is how an error became a finding (#444)")
}

func TestRunGitDiff_CleanFileHasEmptyDiff(t *testing.T) {
	t.Parallel()

	repo := newGitRepo(t)
	commitFile(t, repo, "uv.lock", "locked\n")

	out, err := runGitDiff(t.Context(), repo, "uv.lock")

	require.NoError(t, err)
	assert.Empty(t, out, "a committed, unmodified file has no diff")
}

func TestRunGitDiff_ModifiedFileHasDiff(t *testing.T) {
	t.Parallel()

	repo := newGitRepo(t)
	commitFile(t, repo, "uv.lock", "locked\n")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "uv.lock"), []byte("changed\n"), 0o600))

	out, err := runGitDiff(t.Context(), repo, "uv.lock")

	require.NoError(t, err)
	assert.Contains(t, out, "uv.lock", "a real modification must produce a real diff")
}

// TestFileIsTracked_UntrackedAndAbsentAreNotDrift covers the second half of the
// false positive: uv.lock is tracked by no repository in the fleet, so it must
// never appear in a finding.
func TestFileIsTracked_UntrackedAndAbsentAreNotDrift(t *testing.T) {
	t.Parallel()

	repo := newGitRepo(t)

	t.Run("absent file is untracked", func(t *testing.T) {
		t.Parallel()

		tracked, err := fileIsTracked(t.Context(), repo, "uv.lock")

		require.NoError(t, err)
		assert.False(t, tracked, "a file that does not exist is not tracked, and absence is not drift")
	})

	t.Run("present but unstaged file is untracked", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, os.WriteFile(filepath.Join(repo, "pnpm-lock.yaml"), []byte("x\n"), 0o600))

		tracked, err := fileIsTracked(t.Context(), repo, "pnpm-lock.yaml")

		require.NoError(t, err)
		assert.False(t, tracked, "an untracked lock file is not the workspace's to report")
	})
}

func TestFileIsTracked_CommittedFileIsTracked(t *testing.T) {
	t.Parallel()

	repo := newGitRepo(t)
	commitFile(t, repo, "pnpm-lock.yaml", "lockfileVersion: 9\n")

	tracked, err := fileIsTracked(t.Context(), repo, "pnpm-lock.yaml")

	require.NoError(t, err)
	assert.True(t, tracked)
}

// TestHasUncommittedChanges_SeparatesDirtyFromUnmeasurable is the `lw check git`
// regression pin. Pre-fix, ANY non-zero exit meant "dirty", so git's exit 129
// outside a work tree was reported as uncommitted work.
func TestHasUncommittedChanges_SeparatesDirtyFromUnmeasurable(t *testing.T) {
	t.Parallel()

	t.Run("clean repo", func(t *testing.T) {
		t.Parallel()

		changed, err := hasUncommittedChanges(t.Context(), newGitRepo(t))

		require.NoError(t, err)
		assert.False(t, changed)
	})

	t.Run("dirty repo", func(t *testing.T) {
		t.Parallel()

		repo := newGitRepo(t)
		require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("edited\n"), 0o600))

		changed, err := hasUncommittedChanges(t.Context(), repo)

		require.NoError(t, err, "exit 1 is an answer, not a failure")
		assert.True(t, changed)
	})

	t.Run("not a repository is an error, not dirt", func(t *testing.T) {
		t.Parallel()

		changed, err := hasUncommittedChanges(t.Context(), t.TempDir())

		require.Error(t, err,
			"git exits 129 for usage here; reporting that as uncommitted work is the defect (#444)")
		assert.False(t, changed)
	})
}

// TestWorkspaceGitRepos_ResolvesTheFlatSiblingLayout pins the shape the fleet
// actually has: a parent directory holding repositories, which is precisely the
// shape the old code assumed was itself a repository.
func TestWorkspaceGitRepos_ResolvesTheFlatSiblingLayout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Two repos, one plain directory that must not be mistaken for one.
	for _, name := range []string{"alpha", "beta"} {
		repo := newGitRepo(t)
		require.NoError(t, os.Symlink(repo, filepath.Join(root, name)))
	}

	require.NoError(t, os.MkdirAll(filepath.Join(root, "not-a-repo"), 0o750))

	repos, err := workspaceGitRepos(root)

	require.NoError(t, err)
	require.Len(t, repos, 2, "only the git work trees count")

	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, filepath.Base(r))
	}

	assert.ElementsMatch(t, []string{"alpha", "beta"}, names)
}

func TestWorkspaceGitRepos_ARootThatIsItselfARepoResolvesToItself(t *testing.T) {
	t.Parallel()

	repo := newGitRepo(t)

	repos, err := workspaceGitRepos(repo)

	require.NoError(t, err)
	assert.Equal(t, []string{repo}, repos,
		"pointing lightwave_root at a single checkout must keep working")
}

// TestWorkspaceGitRepos_EmptyWorkspaceIsAnErrorNotASilentPass guards the failure
// mode that would replace one bug with a worse one: a check that examines
// nothing and prints a tick reports a guarantee it never established.
func TestWorkspaceGitRepos_EmptyWorkspaceIsAnErrorNotASilentPass(t *testing.T) {
	t.Parallel()

	repos, err := workspaceGitRepos(t.TempDir())

	require.Error(t, err)
	assert.Empty(t, repos)
	assert.Contains(t, err.Error(), "neither a git repository nor a workspace",
		"the message must say what was wrong with the root, not just fail")
}

// TestWorkspaceGitRepos_MissingRootErrors — a root that does not exist cannot be
// swept, and must not read as "no drift".
func TestWorkspaceGitRepos_MissingRootErrors(t *testing.T) {
	t.Parallel()

	_, err := workspaceGitRepos(filepath.Join(t.TempDir(), "does-not-exist"))

	require.Error(t, err)
}
