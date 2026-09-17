package cli //nolint:testpackage // exercises unexported gate logic (eligibleToMerge/redChecks/ledger)

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Repeated fixture values. goconst flags the duplication, and naming them also
// says what they are: the context below is the advisory reporter from #488,
// whose red status must not by itself stop a merge.
const (
	advisoryReviewContext = "lightwave/local-review"
	checkCompleted        = "COMPLETED"
)

func TestEligibleToMerge(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		wantReason string
		pr         prCandidate
		wantOK     bool
	}{
		{name: "clean", pr: prCandidate{Mergeable: true, MergeState: "CLEAN"}, wantOK: true},
		{
			name:       "draft",
			pr:         prCandidate{Draft: true, Mergeable: true, MergeState: "CLEAN"},
			wantReason: "draft",
		},
		{name: "conflicted", pr: prCandidate{MergeState: "DIRTY"}, wantReason: "not mergeable"},
		// The #488 case: every REQUIRED check passed and one advisory
		// reporter is red. GitHub says UNSTABLE and would merge it; so do we.
		{
			name:   "only a non-required check is red",
			pr:     prCandidate{Mergeable: true, MergeState: "UNSTABLE", RedChecks: []string{advisoryReviewContext}},
			wantOK: true,
		},
		{
			name:       "a required check is red",
			pr:         prCandidate{Mergeable: true, MergeState: "BLOCKED", RedChecks: []string{"Tests"}},
			wantReason: "blocked — red: Tests",
		},
		{
			name:       "blocked with nothing red",
			pr:         prCandidate{Mergeable: true, MergeState: "BLOCKED"},
			wantReason: "blocked — a required check has not reported, or review is outstanding",
		},
		{
			name:       "merge state still computing",
			pr:         prCandidate{Mergeable: true, MergeState: "UNKNOWN"},
			wantReason: "merge state not computed yet — re-run",
		},
		{
			name:       "merge state absent",
			pr:         prCandidate{Mergeable: true},
			wantReason: "merge state not computed yet — re-run",
		},
		{
			name:       "behind main",
			pr:         prCandidate{Mergeable: true, MergeState: "BEHIND"},
			wantReason: "behind",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ok, reason := eligibleToMerge(tc.pr)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantReason, reason)
		})
	}
}

func TestRedChecks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		rollup []checkRollupEntry
		want   []string
	}{
		{name: "empty", rollup: nil, want: nil},
		{
			name:   "green check run",
			rollup: []checkRollupEntry{{Name: "Tests", Status: checkCompleted, Conclusion: "SUCCESS"}},
			want:   nil,
		},
		{
			name: "neutral and skipped are not red",
			rollup: []checkRollupEntry{
				{Name: "Bugbot", Status: checkCompleted, Conclusion: "NEUTRAL"},
				{Name: "Mermaid", Status: checkCompleted, Conclusion: "SKIPPED"},
			},
			want: nil,
		},
		{
			name:   "a running check is not red",
			rollup: []checkRollupEntry{{Name: "mise run ci", Status: "IN_PROGRESS"}},
			want:   nil,
		},
		{
			name:   "failed check run",
			rollup: []checkRollupEntry{{Name: "rust", Status: checkCompleted, Conclusion: "FAILURE"}},
			want:   []string{"rust"},
		},
		// The two rollup shapes label themselves in different fields and null
		// the other. Reading only `name` dropped every status context — the
		// shape `lightwave/local-review` reports in.
		{
			name:   "status context is labelled by context, not name",
			rollup: []checkRollupEntry{{Context: advisoryReviewContext, State: "FAILURE"}},
			want:   []string{advisoryReviewContext},
		},
		{
			name:   "status context error counts as red",
			rollup: []checkRollupEntry{{Context: "legacy/thing", State: "ERROR"}},
			want:   []string{"legacy/thing"},
		},
		{
			name:   "pending status context is not red",
			rollup: []checkRollupEntry{{Context: "legacy/thing", State: "PENDING"}},
			want:   nil,
		},
		{
			name: "both shapes at once",
			rollup: []checkRollupEntry{
				{Name: "Tests", Status: checkCompleted, Conclusion: "SUCCESS"},
				{Name: "web", Status: checkCompleted, Conclusion: "FAILURE"},
				{Context: advisoryReviewContext, State: "FAILURE"},
			},
			want: []string{"web", advisoryReviewContext},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, redChecks(tc.rollup))
		})
	}
}

//nolint:paralleltest // t.Setenv redirects the ledger path; must not run in parallel
func TestSignoffHandlerRoundTrip(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "release-signoff.yaml")
	t.Setenv("LW_RELEASE_SIGNOFF", ledgerPath)

	err := releaseSignOffHandler(context.Background(), []string{"lightwave-ui"}, map[string]any{"by": "v_cto"})
	require.NoError(t, err, "record sign-off")

	ledger, err := loadSignoffs()
	require.NoError(t, err, "reload ledger")
	require.Contains(t, ledger.Signoffs, "lightwave-ui")
	assert.Equal(t, "v_cto", ledger.Signoffs["lightwave-ui"].ApprovedBy)

	err = releaseSignOffHandler(context.Background(), []string{"lightwave-ui"}, map[string]any{"clear": true})
	require.NoError(t, err, "clear sign-off")

	ledger, err = loadSignoffs()
	require.NoError(t, err, "reload after clear")
	assert.NotContains(t, ledger.Signoffs, "lightwave-ui")
}

//nolint:paralleltest // t.Setenv redirects the ledger path; must not run in parallel
func TestReleaseMergeGateClosedWithoutSignoff(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "release-signoff.yaml")
	t.Setenv("LW_RELEASE_SIGNOFF", ledgerPath)
	pinReleaseFlags(t)

	// No sign-off recorded: the gate must short-circuit before any gh call,
	// returning nil (a closed gate is a normal outcome, not an error).
	err := releaseMergeHandler(context.Background(), []string{"lightwave-ui"}, map[string]any{})
	require.NoError(t, err)
}

func pinReleaseFlags(t *testing.T) {
	t.Helper()

	home := t.TempDir()
	flagsDir := filepath.Join(home, ".lightwave", "config", "flags")
	require.NoError(t, os.MkdirAll(flagsDir, 0o755))

	reg := `flags:
  - flag_key: autonomous_release_merge
    default: false
    owner: v_release-engineer
  - flag_key: autonomous_release_pr_merge
    default: false
    owner: v_release-engineer
  - flag_key: release_merge_hold
    default: false
    owner: v_cto
  - flag_key: autonomous_qa_release_pass
    default: false
    owner: v_qa-engineer
  - flag_key: lw_voice_commands
    default: false
    owner: v_release-engineer
`
	require.NoError(t, os.WriteFile(filepath.Join(flagsDir, "registry.yaml"), []byte(reg), 0o644))
	t.Setenv("LW_FLAGS_REGISTRY", filepath.Join(flagsDir, "registry.yaml"))
	t.Setenv("LW_FLAGS_PRINT", filepath.Join(home, ".lightwave", "config", "flags.toml"))
	t.Setenv("LW_FLAGS_STAMP", filepath.Join(flagsDir, "registry.yaml"))
}

func TestRepoNameHelpers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "lightwave-media/lightwave-ui", resolveRepo("lightwave-ui"))
	assert.Equal(t, "owner/repo", resolveRepo("owner/repo"))
	assert.Equal(t, "lightwave-ui", shortRepo("lightwave-media/lightwave-ui"))
	assert.Equal(t, "lightwave-ui", shortRepo("lightwave-ui"))
}
