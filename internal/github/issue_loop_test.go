package github //nolint:testpackage // the row picker and the gh-output matcher are package-private on purpose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPickPullRequestPrefersTheLiveOneOverAClosedLeftover(t *testing.T) {
	t.Parallel()

	ref := TaskRef("task-1")
	// gh lists newest first: the closed PR from an earlier round is listed
	// before the open one that replaced it, and carries the same Refs line.
	rows := []ghPullRow{
		{Number: 12, State: "CLOSED", Body: ref},
		{Number: 9, State: "OPEN", Body: ref},
		{Number: 7, State: "MERGED", Body: ref},
	}

	pr := pickPullRequest(rows, "task-1")
	require.NotNil(t, pr)
	assert.Equal(t, 9, pr.Number, "an open PR is the one the task is still being worked on")
}

func TestPickPullRequestPrefersMergedOverClosedAndNewestWithinAState(t *testing.T) {
	t.Parallel()

	ref := TaskRef("task-1")

	pr := pickPullRequest([]ghPullRow{
		{Number: 12, State: "CLOSED", Body: ref},
		{Number: 7, State: "MERGED", Body: ref},
	}, "task-1")
	require.NotNil(t, pr)
	assert.Equal(t, 7, pr.Number, "a PR that shipped outranks an abandoned one")

	pr = pickPullRequest([]ghPullRow{
		{Number: 14, State: "CLOSED", Body: ref},
		{Number: 12, State: "CLOSED", Body: ref},
	}, "task-1")
	require.NotNil(t, pr)
	assert.Equal(t, 14, pr.Number, "ties go to the newest, which gh lists first")
}

func TestPickPullRequestHoldsTheBodyToTheLiteralLine(t *testing.T) {
	t.Parallel()

	// The search is full text; a PR that merely mentions the id is not the PR.
	assert.Nil(t, pickPullRequest([]ghPullRow{{Number: 3, State: "OPEN", Body: "see task-1 for context"}}, "task-1"))
	assert.Nil(t, pickPullRequest(nil, "task-1"))
}

func TestPickPullRequestCarriesTheMergeState(t *testing.T) {
	t.Parallel()

	pr := pickPullRequest([]ghPullRow{{Number: 3, State: "OPEN", Body: "Refs: task-1", MergeStateStatus: "DIRTY"}}, "task-1")
	require.NotNil(t, pr)
	assert.Equal(t, "DIRTY", pr.MergeState)
}

func TestAutoMergeSwitchedOffMatchesOnlyTheSettingRefusal(t *testing.T) {
	t.Parallel()

	assert.True(t, autoMergeSwitchedOff("GraphQL: Pull request Auto merge is not allowed for this repository (enablePullRequestAutoMerge)"))
	assert.True(t, autoMergeSwitchedOff("auto-merge is not allowed"))

	// Everything else that mentions auto-merge must leave GitHub holding the PR.
	for _, other := range []string{
		"GraphQL: Resource not accessible by personal access token (enablePullRequestAutoMerge)",
		"GraphQL: Pull request is already in auto-merge state",
		"the merge queue is not enabled for this branch",
		"",
	} {
		assert.False(t, autoMergeSwitchedOff(other), other)
	}
}
