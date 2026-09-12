package release_test

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/release"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSummarizeReproducesTheV3130Case is the incident this classification
// exists for (#382).
//
// v3.13.0's range held a `ci(release)!` commit whose BREAKING CHANGE was "no
// more nightly Release PRs" — a change to how people contribute, with no `lw`
// command, flag or output altered. Conventional-commit arithmetic computes that
// as a major, and publishing v4.0.0 tells every consumer their usage broke when
// none of it did.
//
// The bump still computes as major, deliberately: the fix is to make the
// reasoning visible before publishing, not to silently reclassify. A `ci!`
// commit CAN carry a real consumer break, and auto-downgrading would trade a
// loud wrong answer for a quiet one.
func TestSummarizeReproducesTheV3130Case(t *testing.T) {
	t.Parallel()

	commits := []release.Commit{
		{Subject: "feat(mcp): add managed listener lifecycle"},
		{Subject: "fix(git): worktree policy enforced the wrong version"},
		{
			Subject: "ci(release)!: drop the nightly Release PR train",
			Body:    "BREAKING CHANGE: contributors no longer get a nightly Release PR",
		},
	}

	s := release.Summarize(commits)

	assert.Equal(t, 3, s.Total)
	assert.Equal(t, map[string]int{"feat": 1, "fix": 1, "ci": 1}, s.ByType)

	if assert.Len(t, s.Breaking, 1) {
		assert.Equal(t, "ci", s.Breaking[0].Type)
		assert.Equal(t, "release", s.Breaking[0].Scope)
		assert.True(t, s.Breaking[0].ContributorFacing)
	}

	assert.True(t, s.AllBreakingAreContributorFacing(),
		"the whole point: a major resting only on a ci! commit must be flagged")

	// The arithmetic is unchanged — the warning informs the human, it does not
	// overrule them.
	_, bump, err := release.NextVersion(release.Version{Major: 3, Minor: 13}, commits)
	require.NoError(t, err)
	assert.Equal(t, release.BumpMajor, bump)
}

func TestAllBreakingAreContributorFacing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		commits []release.Commit
		want    bool
	}{
		{
			name:    "no breaking markers at all is not a contributor-facing major",
			commits: []release.Commit{{Subject: "feat: add a thing"}},
			want:    false,
		},
		{
			name:    "lone contributor-facing break",
			commits: []release.Commit{{Subject: "chore!: drop node 18 from the dev matrix"}},
			want:    true,
		},
		{
			name: "one consumer-facing break among contributor-facing ones",
			commits: []release.Commit{
				{Subject: "docs!: restructure the contributor guide"},
				{Subject: "feat(cli)!: remove the --legacy flag"},
			},
			want: false,
		},
		{
			name:    "refactor is consumer-facing — it can change behaviour",
			commits: []release.Commit{{Subject: "refactor!: collapse the resolver"}},
			want:    false,
		},
		{
			name:    "build is consumer-facing — it can change the shipped artifact",
			commits: []release.Commit{{Subject: "build!: switch to CGO_ENABLED=0"}},
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want,
				release.Summarize(tc.commits).AllBreakingAreContributorFacing())
		})
	}
}

func TestSummarizeCountsNonConventionalSeparately(t *testing.T) {
	t.Parallel()

	s := release.Summarize([]release.Commit{
		{Subject: "feat: one"},
		{Subject: "merged upstream changes"},
		{Subject: "WIP"},
	})

	assert.Equal(t, map[string]int{"feat": 1, "(non-conventional)": 2}, s.ByType)
	assert.Empty(t, s.Breaking,
		"a non-conventional subject cannot carry a parsed breaking marker")
}

func TestSummarizeFindsBreakingInTheBodyNotJustTheSubject(t *testing.T) {
	t.Parallel()

	s := release.Summarize([]release.Commit{
		{Subject: "fix(api): tighten validation", Body: "BREAKING CHANGE: rejects empty ids"},
	})

	if assert.Len(t, s.Breaking, 1) {
		assert.Equal(t, "fix", s.Breaking[0].Type)
		assert.False(t, s.Breaking[0].ContributorFacing)
	}
}

func TestSummarizeIsCaseInsensitiveOnType(t *testing.T) {
	t.Parallel()

	s := release.Summarize([]release.Commit{{Subject: "CI!: change the runner image"}})

	if assert.Len(t, s.Breaking, 1) {
		assert.True(t, s.Breaking[0].ContributorFacing,
			"type matching must not depend on the author's shift key")
	}
}
