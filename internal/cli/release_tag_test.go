//nolint:testpackage // exercises unexported git helpers
package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/release"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitInRepo runs git in dir and fails the test on a non-zero exit. Hermetic:
// global and system config are neutralised so a developer's ~/.gitconfig
// (signing, hooksPath, default branch) cannot change the outcome.
func gitInRepo(t *testing.T, ctx context.Context, dir string, args ...string) { //nolint:revive // ctx after t is the testing idiom here
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

// newTagRepo builds a throwaway repo with one commit. Running against real git
// rather than a stub is deliberate: the `--sort=-v:refname` ordering and the
// --format separators are the parts most likely to break, and a stub would
// catch neither.
func newTagRepo(t *testing.T, ctx context.Context) string { //nolint:revive // ctx after t is the testing idiom here
	t.Helper()

	dir := t.TempDir()

	gitInRepo(t, ctx, dir, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed"), 0o644))
	gitInRepo(t, ctx, dir, "add", "-A")
	gitInRepo(t, ctx, dir, "commit", "-qm", "chore: seed")

	return dir
}

func TestLastMatchingTagPicksHighestVersionNotNewest(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := newTagRepo(t, ctx)

	// v2.0.0 is tagged before v1.11.0, so a creation-order sort would answer
	// v1.11.0. Version sort must not.
	for _, tag := range []string{"v1.9.0", "v1.10.0", "v2.0.0", "v1.11.0"} {
		gitInRepo(t, ctx, dir, "tag", tag)
	}

	got, err := lastMatchingTag(ctx, dir, "v")
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0", got,
		"tags must sort by version, not creation order — otherwise a late "+
			"backport tag would rewrite what 'latest' means")
}

func TestLastMatchingTagIsPrefixScoped(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := newTagRepo(t, ctx)

	gitInRepo(t, ctx, dir, "tag", "v9.9.9")
	gitInRepo(t, ctx, dir, "tag", "nulltickets/v1.0.0")

	mod, err := lastMatchingTag(ctx, dir, release.TagPrefix("nulltickets"))
	require.NoError(t, err)
	assert.Equal(t, "nulltickets/v1.0.0", mod,
		"a module release must not be dragged forward by the repo-wide tag")

	root, err := lastMatchingTag(ctx, dir, release.TagPrefix(""))
	require.NoError(t, err)
	assert.Equal(t, "v9.9.9", root)
}

func TestLastMatchingTagEmptyWhenNeverReleased(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	got, err := lastMatchingTag(ctx, newTagRepo(t, ctx), "v")
	require.NoError(t, err)
	assert.Empty(t, got, "an unreleased artifact must report no tag, not an error")
}

// TestCommitsSinceSplitsSubjectAndBody pins the record/field separators. Every
// conventional BREAKING CHANGE footer puts newlines in the body, which must not
// be mistaken for additional commits.
func TestCommitsSinceSplitsSubjectAndBody(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := newTagRepo(t, ctx)

	gitInRepo(t, ctx, dir, "tag", "v1.0.0")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	gitInRepo(t, ctx, dir, "add", "-A")
	gitInRepo(t, ctx, dir, "commit", "-qm", "feat: add a\n\nSome body.\n\nBREAKING CHANGE: dropped b")

	commits, err := commitsSince(ctx, dir, "v1.0.0")
	require.NoError(t, err)
	require.Len(t, commits, 1, "a multi-line body must stay one commit")

	assert.Equal(t, "feat: add a", commits[0].Subject)
	assert.Contains(t, commits[0].Body, "BREAKING CHANGE: dropped b")
	assert.Equal(t, release.BumpMajor,
		release.ClassifyCommit(commits[0].Subject, commits[0].Body),
		"the footer must survive the round trip and drive a major bump")
}

func TestTagExists(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := newTagRepo(t, ctx)

	gitInRepo(t, ctx, dir, "tag", "v1.2.3")

	found, err := tagExists(ctx, dir, "v1.2.3")
	require.NoError(t, err)
	assert.True(t, found)

	missing, err := tagExists(ctx, dir, "v9.9.9")
	require.NoError(t, err)
	assert.False(t, missing, "a prefix match must not count as the tag existing")
}

// newClonedRepo builds a repo with a real `origin` remote, so the tag-on-main
// guard runs against actual `git ls-remote` output rather than a stub. Returns
// the working clone and the upstream path.
func newClonedRepo(t *testing.T, ctx context.Context) (clone, upstream string) { //nolint:revive // ctx after t is the testing idiom here
	t.Helper()

	upstream = t.TempDir()
	gitInRepo(t, ctx, upstream, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(upstream, "seed.txt"), []byte("seed"), 0o644))
	gitInRepo(t, ctx, upstream, "add", "-A")
	gitInRepo(t, ctx, upstream, "commit", "-qm", "chore: seed")

	parent := t.TempDir()
	clone = filepath.Join(parent, "clone")
	gitInRepo(t, ctx, parent, "clone", "-q", upstream, clone)

	return clone, upstream
}

// TestVerifyHeadIsOriginMainAcceptsMatchingHead pins that the guard compares
// SHAs, not branch names: a branch sitting exactly on origin/main's tip releases
// identical content, so refusing it would be noise.
func TestVerifyHeadIsOriginMainAcceptsMatchingHead(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo, _ := newClonedRepo(t, ctx)

	require.NoError(t, verifyHeadIsOriginMain(ctx, repo),
		"HEAD equals origin/main, so tagging here is safe")

	gitInRepo(t, ctx, repo, "checkout", "-q", "-b", "some-branch")
	assert.NoError(t, verifyHeadIsOriginMain(ctx, repo),
		"a branch whose tip IS origin/main must be accepted — the guard is about "+
			"the commit being released, not the branch name it is reached by")
}

// TestVerifyHeadIsOriginMainRefusesAheadOfOrigin pins the case the plane rejects
// server-side. Without the guard the tag is created and pushed, the pipeline
// then refuses it, and a published tag with no release is left behind for
// someone to delete by hand.
func TestVerifyHeadIsOriginMainRefusesAheadOfOrigin(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo, _ := newClonedRepo(t, ctx)

	require.NoError(t, os.WriteFile(filepath.Join(repo, "local.txt"), []byte("local"), 0o644))
	gitInRepo(t, ctx, repo, "add", "-A")
	gitInRepo(t, ctx, repo, "commit", "-qm", "feat: unpushed work")

	err := verifyHeadIsOriginMain(ctx, repo)
	require.Error(t, err, "an unpushed commit must not be taggable")
	assert.Contains(t, err.Error(), "is not origin/main")
}

// TestVerifyHeadIsOriginMainRefusesBehindOrigin pins the quieter mistake:
// tagging a stale checkout releases older code under a newer version.
func TestVerifyHeadIsOriginMainRefusesBehindOrigin(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo, upstream := newClonedRepo(t, ctx)

	// Move origin/main forward, leaving the clone's HEAD behind.
	require.NoError(t, os.WriteFile(filepath.Join(upstream, "newer.txt"), []byte("newer"), 0o644))
	gitInRepo(t, ctx, upstream, "add", "-A")
	gitInRepo(t, ctx, upstream, "commit", "-qm", "feat: newer upstream work")

	err := verifyHeadIsOriginMain(ctx, repo)
	require.Error(t, err, "a stale checkout must not be taggable")
	assert.Contains(t, err.Error(), "is not origin/main")
}
