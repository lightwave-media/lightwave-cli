package cli //nolint:testpackage // the gate's seams are package-private on purpose

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gh "github.com/lightwave-media/lightwave-cli/internal/github"
)

const (
	success       = "SUCCESS"
	requiredGate  = "CI Required Gate"
	dryRunFlagKey = "dry-run"
)

// policyYAML is the shape of core's branch_protection.yaml the gate reads.
const policyYAML = `example:
  merge_policy:
    needs_operator_paths:
      - ".github/**"
      - "mise.toml"
      - "scripts/check-*.sh"
`

// withMergeGate swaps the gate's three reads: the branch's required checks, the
// PR's changed files, and core's policy file (policyYAML when policy is nil).
func withMergeGate(t *testing.T, required, files []string, policyErr error) {
	t.Helper()

	origRequired, origFiles, origRead := requiredChecks, pullRequestFiles, readRepoFile
	requiredChecks = func(string, string) ([]string, error) { return required, nil }
	pullRequestFiles = func(string, int) ([]string, error) { return files, nil }
	readRepoFile = func(string, string, string) ([]byte, error) {
		if policyErr != nil {
			return nil, policyErr
		}

		return []byte(policyYAML), nil
	}
	t.Cleanup(func() { requiredChecks, pullRequestFiles, readRepoFile = origRequired, origFiles, origRead })
}

func greenPR(names ...string) *gh.PullRequest {
	pr := &gh.PullRequest{Number: 9, URL: "https://x/pr/9", State: stateOpen}
	for _, n := range names {
		pr.Checks = append(pr.Checks, gh.CheckOutcome{Name: n, Conclusion: success})
	}

	return pr
}

func TestMergeGateHolds(t *testing.T) { //nolint:paralleltest // swaps package seams
	cases := []struct {
		name     string
		repo     string
		want     string
		pr       *gh.PullRequest
		required []string
		files    []string
	}{
		{"green but nothing required (platform#710)", testRepo, holdNoRequiredCheck, greenPR("Unit checks", "Postgres RLS"), nil, nil},
		{"core is never armed", coreRepo, holdCore, greenPR("ci"), []string{"ci"}, nil},
		{"a required check that has not reported", testRepo, holdRequiredPending, greenPR("ci"), []string{"ci", requiredGate}, nil},
		{"a skipped required check is not a pass", testRepo, holdRequiredPending,
			&gh.PullRequest{Number: 9, State: stateOpen, Checks: []gh.CheckOutcome{{Name: "ci", Conclusion: "SKIPPED"}}}, []string{"ci"}, nil},
		{"a diff into .github/**", testRepo, holdOperatorPath, greenPR("ci"), []string{"ci"}, []string{"README.md", ".github/workflows/ci.yml"}},
		{"a single-star pattern stays in its directory", testRepo, holdOperatorPath, greenPR("ci"), []string{"ci"}, []string{"scripts/check-pins.sh"}},
	}

	for _, c := range cases {
		withMergeGate(t, c.required, c.files, nil)

		why := (&mergeGate{}).hold(c.repo, c.pr)
		assert.Contains(t, why, c.want, c.name)
	}
}

func TestMergeGatePassesARequiredGreenPROutsideOperatorPaths(t *testing.T) { //nolint:paralleltest // swaps package seams
	withMergeGate(t, []string{requiredGate}, []string{"internal/cli/x.go", "scripts/nested/check-x.sh", "docs/mise.toml"}, nil)

	assert.Empty(t, (&mergeGate{}).hold(testRepo, greenPR(requiredGate, "Lint")))
}

func TestMergeGateHoldsWhenItCannotReadThePolicy(t *testing.T) { //nolint:paralleltest // swaps package seams
	withMergeGate(t, []string{"ci"}, []string{"x.go"}, errors.New("HTTP 404"))

	assert.Contains(t, (&mergeGate{}).hold(testRepo, greenPR("ci")), holdUnverifiable, "a gate that cannot see does not say yes")
}

func TestLoadOperatorPathsRefusesAPolicyThatNamesNone(t *testing.T) { //nolint:paralleltest // swaps package seams
	orig := readRepoFile
	readRepoFile = func(string, string, string) ([]byte, error) { return []byte("example:\n  merge_policy: {}\n"), nil }
	t.Cleanup(func() { readRepoFile = orig })

	_, err := loadOperatorPaths()
	require.Error(t, err, "empty means the file moved, never that nothing is protected")
}

func TestGlobMatch(t *testing.T) {
	t.Parallel()

	assert.True(t, globMatch(".github/**", ".github/workflows/ci.yml"))
	assert.True(t, globMatch("scripts/check-*.sh", "scripts/check-pins.sh"))
	assert.False(t, globMatch("scripts/check-*.sh", "scripts/nested/check-x.sh"))
	assert.True(t, globMatch("mise.toml", "mise.toml"))
	assert.False(t, globMatch("mise.toml", "docs/mise.toml"))
	assert.False(t, globMatch("go.sum", "go_sum"), "a dot is literal")
}

func TestIssueReconcileHoldsAGreenPRTheBranchDoesNotRequireChecksFor(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{inReview: []map[string]any{reviewTask("green", 1, 2)}}
	_, _, armed := withIssueLoopSeams(t, queue, nil, map[string]*gh.PullRequest{"green": greenPR("Unit checks")})
	withMergeGate(t, nil, nil, nil)

	out, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline})
	require.NoError(t, err)
	assert.Empty(t, *armed, "nothing was required, so nothing could have said no")
	assert.Contains(t, out, reconcileHeld)
	assert.Contains(t, out, holdNoRequiredCheck)
	assert.Empty(t, queue.transitions, "a held task stays in review for the next tick")
}

func TestIssueReconcileDryRunReportsTheHold(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{inReview: []map[string]any{reviewTask("green", 1, 2)}}
	withIssueLoopSeams(t, queue, nil, map[string]*gh.PullRequest{"green": greenPR("Unit checks")})
	withMergeGate(t, nil, nil, nil)

	out, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline, dryRunFlagKey: true})
	require.NoError(t, err)
	assert.Contains(t, out, reconcileDryRun+":"+reconcileHeld)
	assert.Contains(t, out, holdNoRequiredCheck)
}

func TestIssueReconcileHoldsAnOrphanedPRTheGateRefuses(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{notDoing: []map[string]any{droppedTask("t", 1, time.Hour)}}
	_, _, armed := withIssueLoopSeams(t, queue, nil, map[string]*gh.PullRequest{"t": orphanPR(false, "SUCCESS")})
	withMergeGate(t, []string{"ci"}, []string{".github/workflows/ci.yml"}, nil)

	_, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline})
	require.NoError(t, err)
	assert.Empty(t, *armed)
}
