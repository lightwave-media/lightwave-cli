package release_test

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/release"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    release.Version
		wantErr bool
	}{
		{name: "bare", in: "1.2.3", want: release.Version{Major: 1, Minor: 2, Patch: 3}},
		{name: "v-prefixed", in: "v1.2.3", want: release.Version{Major: 1, Minor: 2, Patch: 3}},
		{name: "monorepo tag", in: "nulltickets/v2.0.1", want: release.Version{Major: 2, Minor: 0, Patch: 1}},
		{name: "zero", in: "v0.0.0", want: release.Version{}},
		{name: "multi-digit", in: "v10.20.30", want: release.Version{Major: 10, Minor: 20, Patch: 30}},
		{name: "surrounding space", in: "  v1.2.3 ", want: release.Version{Major: 1, Minor: 2, Patch: 3}},
		{name: "two components rejected", in: "v1.2", wantErr: true},
		{name: "prerelease rejected", in: "v1.2.3-rc1", wantErr: true},
		{name: "empty rejected", in: "", wantErr: true},
		{name: "words rejected", in: "latest", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := release.ParseVersion(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVersionNextZeroesLowerComponents(t *testing.T) {
	t.Parallel()

	v := release.Version{Major: 3, Minor: 12, Patch: 7}

	assert.Equal(t, "4.0.0", v.Next(release.BumpMajor).String(),
		"a major bump must zero minor and patch")
	assert.Equal(t, "3.13.0", v.Next(release.BumpMinor).String(),
		"a minor bump must zero patch")
	assert.Equal(t, "3.12.8", v.Next(release.BumpPatch).String())
	assert.Equal(t, "3.12.7", v.Next(release.BumpNone).String())
}

func TestClassifyCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		body    string
		want    release.Bump
	}{
		{name: "feat is minor", subject: "feat: add thing", want: release.BumpMinor},
		{name: "feat with scope", subject: "feat(cli): add thing", want: release.BumpMinor},
		{name: "fix is patch", subject: "fix: correct thing", want: release.BumpPatch},
		{name: "chore is patch", subject: "chore: tidy", want: release.BumpPatch},
		{name: "docs is patch", subject: "docs: explain", want: release.BumpPatch},

		{name: "bang is major", subject: "feat!: drop v1 API", want: release.BumpMajor},
		{name: "bang with scope is major", subject: "refactor(api)!: rename", want: release.BumpMajor},
		{
			name:    "breaking footer is major",
			subject: "fix: correct thing",
			body:    "Some context.\n\nBREAKING CHANGE: config key renamed",
			want:    release.BumpMajor,
		},
		{
			name:    "hyphenated breaking footer is major",
			subject: "fix: correct thing",
			body:    "BREAKING-CHANGE: config key renamed",
			want:    release.BumpMajor,
		},
		{
			name:    "breaking must start a line",
			subject: "fix: mention BREAKING CHANGE: in passing",
			body:    "we discussed a BREAKING CHANGE: but did not make one",
			want:    release.BumpPatch,
		},

		{name: "non-conventional subject is patch", subject: "wip: session changes", want: release.BumpPatch},
		{name: "bare subject is patch", subject: "update the thing", want: release.BumpPatch},
		{name: "FEAT uppercase is minor", subject: "FEAT: shout", want: release.BumpMinor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, release.ClassifyCommit(tt.subject, tt.body))
		})
	}
}

func TestNextVersionTakesLargestBump(t *testing.T) {
	t.Parallel()

	last := release.Version{Major: 3, Minor: 12, Patch: 0}

	got, bump, err := release.NextVersion(last, []release.Commit{
		{Subject: "fix: one"},
		{Subject: "feat: two"},
		{Subject: "chore: three"},
	})
	require.NoError(t, err)

	assert.Equal(t, release.BumpMinor, bump, "feat must outrank fix and chore")
	assert.Equal(t, "3.13.0", got.String())
}

func TestNextVersionBreakingWinsFromAnywhereInRange(t *testing.T) {
	t.Parallel()

	got, bump, err := release.NextVersion(release.Version{Major: 3, Minor: 12}, []release.Commit{
		{Subject: "feat: one"},
		{Subject: "fix: two", Body: "BREAKING CHANGE: removed the old flag"},
		{Subject: "chore: three"},
	})
	require.NoError(t, err)

	assert.Equal(t, release.BumpMajor, bump)
	assert.Equal(t, "4.0.0", got.String())
}

// TestNextVersionEmptyRangeIsAnError pins the deliberate choice to fail rather
// than return the current version: a scripted release must not silently
// re-tag what is already released.
func TestNextVersionEmptyRangeIsAnError(t *testing.T) {
	t.Parallel()

	_, _, err := release.NextVersion(release.Version{Major: 1}, nil)
	assert.ErrorIs(t, err, release.ErrNoCommits)
}

func TestTagPrefix(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "v", release.TagPrefix(""),
		"single-artifact repos use the plain v<semver> grammar")
	assert.Equal(t, "nulltickets/v", release.TagPrefix("nulltickets"),
		"monorepo modules use the <module>/v<semver> grammar")
}
