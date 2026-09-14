//nolint:testpackage // exercises the unexported bucketing and report writer
package cli

import (
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bucket a branch lands in is the whole message the report delivers, and
// two of the five are load-bearing in opposite directions: `sweep` says "this
// will be deleted" and `unrouted` says "this is work nobody has routed". A
// branch in the wrong one is either destroyed or abandoned, so every shape gets
// a row here rather than a spot check.
func TestBucketOfSeparatesDeletableFromEndangered(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		want  string
		state git.BranchState
	}{{
		name:  "gone and proven is the only thing --execute touches",
		state: git.BranchState{Name: "feat/done", Gone: true, Proof: git.ProofSameTree},
		want:  bucketSweep,
	}, {
		name:  "gone without proof is endangered work, not tidy-up",
		state: git.BranchState{Name: "feat/closed", Gone: true, Upstream: "origin/feat/closed"},
		want:  bucketUnrouted,
	}, {
		name:  "a local-only branch has no remote that could have merged it",
		state: git.BranchState{Name: "feat/scratch"},
		want:  bucketUnrouted,
	}, {
		name:  "proof does not move a held branch out of held",
		state: git.BranchState{Name: "feat/held", Gone: true, Proof: git.ProofMergedPR, HeldBy: "/w/t"},
		want:  bucketHeld,
	}, {
		name:  "an unproven held branch is still held, not unrouted",
		state: git.BranchState{Name: "feat/busy", Gone: true, HeldBy: "/w/t"},
		want:  bucketHeld,
	}, {
		name:  "merged but still published is landed, not sweepable",
		state: git.BranchState{Name: "feat/open", Upstream: "origin/feat/open", Proof: git.ProofAncestor},
		want:  bucketLanded,
	}, {
		name:  "the current branch is never a candidate, whatever its proof",
		state: git.BranchState{Name: "feat/here", Gone: true, Proof: git.ProofSameTree, Current: true},
		want:  bucketActive,
	}, {
		name:  "the base branch is the recovery anchor",
		state: git.BranchState{Name: "main", Base: true, Upstream: "origin/main"},
		want:  bucketActive,
	}, {
		name:  "in-flight work with a live upstream",
		state: git.BranchState{Name: "feat/wip", Upstream: "origin/feat/wip"},
		want:  bucketActive,
	}}

	for _, testCase := range tests {
		tt := testCase
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, bucketOf(&tt.state))
			assert.Equal(t, tt.want == bucketSweep, tt.state.Deletable(),
				"the sweep bucket and Deletable() must be the same claim; two definitions of safe is one too many")
		})
	}
}

// branchNote is where a reader learns WHY. A proven branch shows its receipt; a
// stalled one shows the reason and how much work is behind it.
func TestBranchNoteCarriesTheReceiptOrTheReason(t *testing.T) {
	t.Parallel()

	proven := branchNote(&git.BranchState{
		Name: "feat/done", Gone: true, Proof: git.ProofMergedPR, Evidence: "#428",
	})
	assert.Contains(t, proven, "merged-pr")
	assert.Contains(t, proven, "#428", "a proof with no receipt is an assertion")

	stalled := branchNote(&git.BranchState{
		Name: "feat/closed", Gone: true, Upstream: "origin/feat/closed", Unique: 3,
	})
	assert.Contains(t, stalled, "unproven")
	assert.Contains(t, stalled, "3 unique commit(s)",
		"the reader deciding whether to open a PR needs to know how much is at stake")

	held := branchNote(&git.BranchState{
		Name: "feat/held", Gone: true, Proof: git.ProofSameTree, Evidence: "abc1234", HeldBy: "/w/pin",
	})
	assert.Contains(t, held, "abc1234")
	assert.Contains(t, held, "/w/pin", "a merged-but-held branch must name the worktree blocking it")
}

func TestReportNamesEveryBucketItPrints(t *testing.T) {
	t.Parallel()

	var out strings.Builder

	writeSweepBranches(&out, []git.BranchState{
		{Name: "feat/done", Gone: true, Proof: git.ProofSameTree, Evidence: "abc1234"},
		{Name: "feat/closed", Gone: true, Upstream: "origin/feat/closed", Unique: 2},
		{Name: "main", Base: true, Upstream: "origin/main"},
	})

	text := out.String()
	assert.Contains(t, text, "branches — 3 local")
	assert.Contains(t, text, bucketSweep)
	assert.Contains(t, text, bucketUnrouted)
	assert.Contains(t, text, "feat/closed")
	assert.NotContains(t, text, bucketHeld, "an empty bucket must not print a header")

	// `active` prints as a name list, since there is nothing to decide there.
	assert.Contains(t, text, "main")
}

// TestWorktreeSectionSaysWhyTheRestWereLeft — a count with no reason reads as
// an oversight; the fleet's dirty and locked trees belong to other sessions and
// the report has to say so.
func TestWorktreeSectionSaysWhyTheRestWereLeft(t *testing.T) {
	t.Parallel()

	var out strings.Builder

	writeSweepWorktrees(&out, []git.WorktreeState{
		{Path: "/repo", Branch: "main", Main: true},
		{Path: "/gone", Branch: "feat/gone", Prunable: "gitdir file points to non-existent location"},
		{Path: "/busy", Branch: "feat/busy", Dirty: true},
		{Path: "/pinned", Branch: "feat/pinned", Locked: "held"},
	})

	text := out.String()
	assert.Contains(t, text, "/gone")
	assert.Contains(t, text, "gitdir file points to non-existent location", "git's reason, not ours")
	assert.Contains(t, text, "1 dirty, 1 locked")
	assert.NotContains(t, text, "/busy", "a live worktree is counted, never listed as actionable")
}

// TestStashSectionHidesFreshEntriesAndFlagsAutoSaves — the stack is repo-wide,
// so listing this session's minutes-old stash would bury the stale ones that
// actually need a decision.
func TestStashSectionHidesFreshEntriesAndFlagsAutoSaves(t *testing.T) {
	t.Parallel()

	var out strings.Builder

	writeSweepStashes(&out, []git.StashState{
		{Ref: "stash@{0}", Subject: "On main: just now", AgeDays: 0},
		{Ref: "stash@{1}", Subject: "claude-session-start: auto", AgeDays: 40, SessionAuto: true},
	})

	text := out.String()
	assert.Contains(t, text, "2 on the shared stack")
	assert.NotContains(t, text, "just now", "a fresh stash is not a finding")
	assert.Contains(t, text, "stash@{1}")
	assert.Contains(t, text, "droppable by doctrine")
}

// TestSweepRefusesADirectoryThatIsNotARepo is the rejection path a --repo typo
// lands on. It must name the path: "not a git repository" with no subject sends
// the reader looking at the wrong directory.
func TestSweepRefusesADirectoryThatIsNotARepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := gitSweepHandler(t.Context(), nil, map[string]any{"repo": dir, "offline": true})
	require.Error(t, err, "sweeping a non-repository must fail, not report an empty clean sweep")
	assert.Contains(t, err.Error(), dir)
}
