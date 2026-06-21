package cli //nolint:testpackage // exercises unexported gate logic (eligibleToMerge/checksGreen/ledger)

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEligibleToMerge(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		wantReason string
		pr         prCandidate
		wantOK     bool
	}{
		{name: "ready", pr: prCandidate{Mergeable: true, CIGreen: true}, wantOK: true},
		{name: "draft", pr: prCandidate{Draft: true, Mergeable: true, CIGreen: true}, wantReason: "draft"},
		{name: "conflicted", pr: prCandidate{CIGreen: true}, wantReason: "not mergeable"},
		{name: "red ci", pr: prCandidate{Mergeable: true}, wantReason: "CI not green"},
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

func TestChecksGreen(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		rollup []checkRollupEntry
		want   bool
	}{
		{name: "empty is not green", rollup: nil, want: false},
		{name: "checkrun success", rollup: []checkRollupEntry{{Status: "COMPLETED", Conclusion: "SUCCESS"}}, want: true},
		{name: "checkrun neutral+skipped", rollup: []checkRollupEntry{
			{Status: "COMPLETED", Conclusion: "NEUTRAL"},
			{Status: "COMPLETED", Conclusion: "SKIPPED"},
		}, want: true},
		{name: "checkrun running", rollup: []checkRollupEntry{{Status: "IN_PROGRESS"}}, want: false},
		{name: "checkrun failure", rollup: []checkRollupEntry{{Status: "COMPLETED", Conclusion: "FAILURE"}}, want: false},
		{name: "statuscontext success", rollup: []checkRollupEntry{{State: "SUCCESS"}}, want: true},
		{name: "statuscontext pending", rollup: []checkRollupEntry{{State: "PENDING"}}, want: false},
		{name: "mixed one red", rollup: []checkRollupEntry{
			{Status: "COMPLETED", Conclusion: "SUCCESS"},
			{State: "FAILURE"},
		}, want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, checksGreen(tc.rollup))
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
