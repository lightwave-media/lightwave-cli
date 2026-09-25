//nolint:testpackage // drives the unexported porcelain parser and the run() wrapper
package git

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/testutil/gitfixture"
)

// sweepFixture is a clone with a REAL bare origin. Every claim the sweeper
// makes is about things only a real remote produces — a [gone] upstream, a
// squash merge, a worktree holding a branch — so mocking git here would test
// the mock. The cost is a few hundred milliseconds of subprocess; the benefit
// is that the git invocations themselves are under test.
//
// That mattered immediately: the first version of scanBranches passed a literal
// NUL as the field separator, which exec rejects outright ("fork/exec: invalid
// argument"). No parser test would have seen it. Anything that walks a real
// repo does.
type sweepFixture struct {
	t      *testing.T
	Dir    string
	Remote string
}

func newSweepFixture(t *testing.T) *sweepFixture {
	t.Helper()

	root := realDir(t, t.TempDir())
	f := &sweepFixture{t: t, Dir: filepath.Join(root, "clone"), Remote: filepath.Join(root, "origin.git")}

	gitAt(t, root, "init", "--bare", "--initial-branch=main", f.Remote)
	gitAt(t, root, "init", "--initial-branch=main", f.Dir)
	// The operator's global config sets branch.autoSetupMerge, which silently
	// gave every `checkout -b` an upstream of local main and turned the
	// no-upstream control into a tracked branch. A fixture that inherits ambient
	// config is measuring the machine, not the code.
	f.run("config", "branch.autoSetupMerge", "false")
	f.run("remote", "add", "origin", f.Remote)

	f.commit("README.md", "# fixture\n", "initial")
	f.run("push", "-u", "origin", "main")

	return f
}

// run executes git in the clone as a fixture call; see gitAt.
func (f *sweepFixture) run(args ...string) string {
	f.t.Helper()

	return gitAt(f.t, f.Dir, args...)
}

// realDir resolves symlinks, because git reports resolved paths and macOS puts
// every temp dir behind /var -> /private/var. Comparing an unresolved fixture
// path against git's output fails on a difference that is not one.
func realDir(t *testing.T, dir string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	return resolved
}

// gitAt runs git in dir under gitfixture.Env, so an outer hook's GIT_DIR or
// GIT_CONFIG cannot redirect the fixture and the commit identity never has to
// be written into config. It returns trimmed stdout, as run() does.
func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = gitfixture.Env()

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), stderr.String())

	return strings.TrimSpace(string(out))
}

func (f *sweepFixture) commit(name, body, message string) {
	f.t.Helper()

	require.NoError(f.t, os.WriteFile(filepath.Join(f.Dir, name), []byte(body), 0o600))
	f.run("add", name)
	f.run("commit", "-m", message)
}

// branchWithWork creates branch, adds one file on it, and returns to main.
func (f *sweepFixture) branchWithWork(branch, file string) {
	f.t.Helper()

	f.run("checkout", "-q", "-b", branch)
	f.commit(file, "work on "+branch+"\n", "feat: "+branch)
	// A SECOND commit, because one is the unrepresentative case: a single
	// commit squashed with nothing else in flight keeps its patch-id, so
	// `git cherry` calls it equivalent and the fixture quietly stops
	// reproducing the problem it exists to demonstrate.
	f.commit(file, "more work on "+branch+"\n", "feat: "+branch+" again")
	f.run("checkout", "-q", "main")
}

// squashMergeToMain reproduces what the forge does on "Squash and merge": the
// branch's CONTENT lands as one new commit, and none of its commits do.
func (f *sweepFixture) squashMergeToMain(branch string) {
	f.t.Helper()

	f.run("checkout", "-q", "main")
	f.run("merge", "--squash", branch)
	f.run("commit", "-m", "squash: "+branch)
	f.run("push", "origin", "main")
}

// abandonRemote publishes the branch then deletes it upstream, which is how a
// local branch comes to read [gone] — the state auto-delete-on-merge leaves.
func (f *sweepFixture) abandonRemote(branch string) {
	f.t.Helper()

	f.run("push", "-u", "origin", branch)
	f.run("push", "origin", "--delete", branch)
	f.run("fetch", "origin", "--prune")
}

func (f *sweepFixture) sweeper(lookup MergedPRLookup) *Sweeper {
	f.t.Helper()

	s, err := NewSweeper(NewGit(f.Dir), lookup)
	require.NoError(f.t, err)

	return s
}

func branchNamed(t *testing.T, states []BranchState, name string) BranchState {
	t.Helper()

	for _, b := range states {
		if b.Name == name {
			return b
		}
	}

	t.Fatalf("branch %q missing from classification of %d branches", name, len(states))

	return BranchState{}
}

// TestSquashMergeDefeatsTheObviousTestsButNotTreeOnBase is the measurement the
// whole design rests on, run as a test rather than trusted from a comment.
//
// Both known-bad controls are asserted here on purpose: a squash-merged branch
// is NOT an ancestor of the base, and `git cherry` calls its commit unique — so
// either check, used as the deletion criterion #321 proposed, would classify
// genuinely merged work as unrouted (and, read the other way round, would give
// identical output for work that never merged at all).
func TestSquashMergeDefeatsTheObviousTestsButNotTreeOnBase(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	f.branchWithWork("feat/squashed", "squashed.txt")
	f.squashMergeToMain("feat/squashed")

	g := NewGit(f.Dir)

	ancestor, err := g.IsAncestor("feat/squashed", "origin/main")
	require.NoError(t, err)
	assert.False(t, ancestor,
		"control: a squash-merged branch must NOT look like an ancestor, or this fixture is not reproducing a squash")

	cherry := f.run("cherry", "origin/main", "feat/squashed")
	assert.Contains(t, cherry, "+ ",
		"control: `git cherry` must report the merged branch's commit as unique — that is why it cannot gate deletion")

	s := f.sweeper(nil)

	proof, evidence, err := s.proveMerged("feat/squashed")
	require.NoError(t, err)
	assert.Equal(t, ProofSameTree, proof, "the squash landed this tree verbatim on the base")
	assert.NotEmpty(t, evidence, "the proof must name the base commit that carries the tree")
}

// TestGoneAndProvenIsTheOnlyDeletableShape walks all four combinations of the
// two conditions, because either one alone has been proposed as sufficient and
// either one alone destroys work.
func TestGoneAndProvenIsTheOnlyDeletableShape(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)

	// gone + proven: the one deletable shape.
	f.branchWithWork("feat/landed", "landed.txt")
	f.squashMergeToMain("feat/landed")
	f.abandonRemote("feat/landed")

	// gone + unproven: a PR closed without merging looks exactly like this.
	f.branchWithWork("feat/abandoned", "abandoned.txt")
	f.abandonRemote("feat/abandoned")

	// proven but still published: nothing has said this branch is finished.
	f.branchWithWork("feat/published", "published.txt")
	f.squashMergeToMain("feat/published")
	f.run("push", "-u", "origin", "feat/published")
	f.run("fetch", "origin", "--prune")

	// neither: ordinary local work.
	f.branchWithWork("feat/local", "local.txt")

	states, err := f.sweeper(nil).Branches()
	require.NoError(t, err)

	landed := branchNamed(t, states, "feat/landed")
	assert.True(t, landed.Gone)
	assert.Equal(t, ProofSameTree, landed.Proof)
	assert.True(t, landed.Deletable(), "gone and proven is the deletable shape")
	assert.Empty(t, landed.Why())

	abandoned := branchNamed(t, states, "feat/abandoned")
	assert.True(t, abandoned.Gone)
	assert.Equal(t, ProofNone, abandoned.Proof)
	assert.False(t, abandoned.Deletable(),
		"a deleted upstream is not proof of a merge — a closed-unmerged PR leaves this exact state")
	assert.Contains(t, abandoned.Why(), "unproven")
	assert.Positive(t, abandoned.Unique, "the report must say how much work is at stake")

	published := branchNamed(t, states, "feat/published")
	assert.False(t, published.Gone)
	assert.Equal(t, ProofSameTree, published.Proof)
	assert.False(t, published.Deletable(), "proof alone does not authorise deletion")
	assert.Contains(t, published.Why(), "still exists")

	local := branchNamed(t, states, "feat/local")
	assert.False(t, local.Deletable())
	assert.Contains(t, local.Why(), "local only")
}

// TestForgeProvesWhatTheCloneCannot is the --offline contract in both
// directions, on the one branch shape no local evidence can reach: merged while
// behind the base, so the squash commit's tree matches neither side.
func TestForgeProvesWhatTheCloneCannot(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	f.branchWithWork("feat/behind", "behind.txt")

	// The base moves on before the merge, so the squash tree is the union and
	// is equal to no tree the branch ever had.
	f.commit("unrelated.txt", "moved on\n", "chore: unrelated")
	f.squashMergeToMain("feat/behind")
	f.abandonRemote("feat/behind")

	offline, err := f.sweeper(nil).Branches()
	require.NoError(t, err)

	behind := branchNamed(t, offline, "feat/behind")
	assert.Equal(t, ProofNone, behind.Proof,
		"no local evidence exists for a branch squashed while behind; offline must say so")
	assert.False(t, behind.Deletable(), "offline reports it, and must not delete it")

	asked := 0
	lookup := func(branch string) (int, bool, error) {
		asked++

		return 77, branch == "feat/behind", nil
	}

	online, err := f.sweeper(lookup).Branches()
	require.NoError(t, err)
	assert.Positive(t, asked, "the forge is the only remaining proof; it must actually be consulted")

	proven := branchNamed(t, online, "feat/behind")
	assert.Equal(t, ProofMergedPR, proven.Proof)
	assert.Equal(t, "#77", proven.Evidence, "the receipt must name the PR")
	assert.True(t, proven.Deletable())
}

// TestOfflineNeverReachesTheForge — --offline must be a promise about the
// network, not a hint. A lookup passed but not called would still be a leak.
func TestOfflineNeverReachesTheForge(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	f.branchWithWork("feat/quiet", "quiet.txt")
	f.abandonRemote("feat/quiet")

	_, err := f.sweeper(nil).Branches()
	require.NoError(t, err, "a nil lookup is the offline contract and must not be dereferenced")
}

// TestDeleteRefusesWhatTheClassifierDidNotClear — Delete re-checks rather than
// trusting its caller, so a second code path cannot invent its own safe set.
func TestDeleteRefusesWhatTheClassifierDidNotClear(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	f.branchWithWork("feat/unrouted", "unrouted.txt")
	f.abandonRemote("feat/unrouted")

	s := f.sweeper(nil)

	states, err := s.Branches()
	require.NoError(t, err)

	unrouted := branchNamed(t, states, "feat/unrouted")

	_, err = s.Delete(&unrouted)
	require.Error(t, err, "an unproven branch must not be deletable through any path")
	require.ErrorIs(t, err, ErrNotDeletable)

	// And it is still there — the refusal is the whole point.
	assert.Contains(t, f.run("branch", "--list", "feat/unrouted"), "feat/unrouted")

	// A hand-forged "deletable" state must not work either: the receipt has to
	// come from the classifier, not from the caller's optimism.
	_, err = s.Delete(&BranchState{Name: "feat/unrouted", Gone: true, Proof: ProofMergedPR, HeldBy: "/somewhere"})
	require.Error(t, err, "a held branch is not deletable however it is labelled")
}

// TestDeleteRemovesASquashMergedBranchAndNamesTheTip covers the -D fallback,
// which exists because `git branch -d` refuses every squash-merged branch — a
// -d-only sweep would report cleanup it never performed.
func TestDeleteRemovesASquashMergedBranchAndNamesTheTip(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	f.branchWithWork("feat/done", "done.txt")
	f.squashMergeToMain("feat/done")
	f.abandonRemote("feat/done")

	// Control: git's own check cannot see this merge, so -d alone does nothing.
	_, err := NewGit(f.Dir).run("branch", "-d", "feat/done")
	require.Error(t, err, "if -d succeeded here the fixture is not reproducing a squash merge")

	s := f.sweeper(nil)

	states, err := s.Branches()
	require.NoError(t, err)

	done := branchNamed(t, states, "feat/done")

	tip, err := s.Delete(&done)
	require.NoError(t, err)
	require.NotEmpty(t, tip, "the tip is what makes the deletion reversible")

	assert.Empty(t, f.run("branch", "--list", "feat/done"))

	// The receipt has to actually restore it, or it is decoration.
	f.run("branch", "feat/done", tip)
	assert.Contains(t, f.run("branch", "--list", "feat/done"), "feat/done")
}

// TestBranchHeldByAWorktreeIsNeverSwept — the fleet runs many concurrent
// worktrees, so a merged branch someone is standing on is the common case, not
// an edge one. Deleting it would break their checkout.
func TestBranchHeldByAWorktreeIsNeverSwept(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	f.branchWithWork("feat/held", "held.txt")
	f.squashMergeToMain("feat/held")
	f.abandonRemote("feat/held")

	held := filepath.Join(realDir(t, t.TempDir()), "held")
	f.run("worktree", "add", held, "feat/held")

	states, err := f.sweeper(nil).Branches()
	require.NoError(t, err)

	state := branchNamed(t, states, "feat/held")
	assert.Equal(t, ProofSameTree, state.Proof, "it really is merged — that is what makes this the dangerous case")
	assert.Equal(t, held, state.HeldBy)
	assert.False(t, state.Deletable())
	assert.Contains(t, state.Why(), held, "the report must name the worktree so the reader can go look")
}

// TestWorktreesReportDeadRegistrationsAndPruneClearsThem. Pruning removes only
// administrative entries git has already marked prunable; it never removes a
// directory, which is why it is in the --execute set at all.
func TestWorktreesReportDeadRegistrationsAndPruneClearsThem(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	f.branchWithWork("feat/wt", "wt.txt")

	root := realDir(t, t.TempDir())
	live := filepath.Join(root, "live")
	dead := filepath.Join(root, "dead")

	f.run("worktree", "add", live, "feat/wt")
	f.run("worktree", "add", "-b", "feat/dead", dead)
	require.NoError(t, os.Rename(dead, filepath.Join(root, "moved")))

	s := f.sweeper(nil)

	before, err := s.Worktrees()
	require.NoError(t, err)
	require.Len(t, before, 3, "main, live, dead")

	assert.True(t, before[0].Main, "git lists the main working tree first")

	var sawDead, sawLive bool

	for _, w := range before {
		switch w.Path {
		case dead:
			sawDead = true

			assert.NotEmpty(t, w.Prunable, "a registration whose path is gone must be reported prunable")
		case live:
			sawLive = true

			assert.Empty(t, w.Prunable, "a live worktree must never be reported prunable")
		}
	}

	assert.True(t, sawDead && sawLive, "both controls must be present")

	pruned, err := s.PruneWorktrees()
	require.NoError(t, err)
	assert.NotEmpty(t, pruned, "prune must report what it cleared")

	after, err := s.Worktrees()
	require.NoError(t, err)
	assert.Len(t, after, 2, "only the dead registration goes")
	assert.DirExists(t, live, "pruning must not remove a live worktree's directory")
}

// TestParseWorktreePorcelainReadsBothAnnotations pins the two keys the
// classification depends on, in the exact shape git emits (verified against a
// real moved worktree and a real `git worktree lock`).
func TestParseWorktreePorcelainReadsBothAnnotations(t *testing.T) {
	t.Parallel()

	states := parseWorktreePorcelain(strings.Join([]string{
		"worktree /repo",
		"HEAD " + strings.Repeat("a", 40),
		"branch refs/heads/main",
		"",
		"worktree /gone",
		"HEAD " + strings.Repeat("b", 40),
		"branch refs/heads/feat/gone",
		"prunable gitdir file points to non-existent location",
		"",
		"worktree /locked",
		"HEAD " + strings.Repeat("c", 40),
		"branch refs/heads/feat/locked",
		"locked held by a sibling session",
		"",
	}, "\n"))

	require.Len(t, states, 3)

	assert.True(t, states[0].Main)
	assert.Equal(t, "main", states[0].Branch, "refs/heads/ must be stripped")
	assert.False(t, states[1].Main)

	assert.Equal(t, "gitdir file points to non-existent location", states[1].Prunable,
		"git's own wording is more useful than ours")
	assert.Equal(t, "held by a sibling session", states[2].Locked)
	assert.Empty(t, states[2].Prunable, "locked is not prunable")
}

// TestStashesReadTheSharedStack drives the other NUL-separated format. The
// stack is repo-wide, so this is also the reminder that the entries listed may
// belong to another session entirely — which is why sweep only reports them.
func TestStashesReadTheSharedStack(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)

	require.NoError(t, os.WriteFile(filepath.Join(f.Dir, "README.md"), []byte("# dirty\n"), 0o600))
	f.run("stash", "push", "-m", "claude-session-start: auto-save")

	stashes, err := f.sweeper(nil).Stashes(time.Now().Add(72 * time.Hour))
	require.NoError(t, err)
	require.Len(t, stashes, 1)

	assert.Equal(t, "stash@{0}", stashes[0].Ref)
	assert.Contains(t, stashes[0].Subject, "auto-save")
	assert.Equal(t, 3, stashes[0].AgeDays, "age is measured against the caller's clock, not the wall clock")
	assert.True(t, stashes[0].SessionAuto, "session auto-saves are droppable by doctrine and must be marked")
}

// TestStashesAreEmptyOnACleanStack — the other direction, and the one that
// would hide a parse failure behind a plausible-looking nil.
func TestStashesAreEmptyOnACleanStack(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)

	stashes, err := f.sweeper(nil).Stashes(time.Now())
	require.NoError(t, err)
	assert.Empty(t, stashes)
}

// TestNewSweeperFallsBackToTheLocalTrunk — a repo with no origin must still
// classify rather than error on a ref nobody has.
func TestNewSweeperFallsBackToTheLocalTrunk(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "solo")
	gitAt(t, t.TempDir(), "init", "--initial-branch=main", dir)
	gitAt(t, dir, "commit", "--allow-empty", "-m", "initial")

	s, err := NewSweeper(NewGit(dir), nil)
	require.NoError(t, err)
	assert.Equal(t, "main", s.Base(), "with no origin the local trunk is the base")

	fetched, err := s.FetchPrune()
	require.NoError(t, err, "no origin is not an error; it is a fact to report")
	assert.False(t, fetched)
}

// TestNewSweeperRejectsARepoWithNoTrunk — an empty repo has no base to classify
// against, and guessing one would make every branch look unmerged.
func TestNewSweeperRejectsARepoWithNoTrunk(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "empty")
	gitAt(t, t.TempDir(), "init", "--initial-branch=main", dir)

	_, err := NewSweeper(NewGit(dir), nil)
	require.Error(t, err, "no commits means no base; the sweeper must refuse rather than assume")
	assert.Contains(t, err.Error(), "no base branch")
}
