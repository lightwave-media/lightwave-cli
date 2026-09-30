package github //nolint:testpackage // exercises the shared adapter and its package-private fixtures

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reviewedHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func reviewFixturePage(nodes string) string {
	return `{"data":{"repository":{"pullRequest":{"headRefOid":"` + reviewedHead + `","reviewDecision":null,"connection":{"nodes":` + nodes + `}}}}}`
}

func fakeReviewGH(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("LW_REVIEW_FIXTURES", dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	files := map[string]string{
		"prs":      `[{"number":9,"url":"pr/9","state":"OPEN","headRefOid":"` + reviewedHead + `","body":"Refs: task-1","statusCheckRollup":[{"name":"CI","conclusion":"SUCCESS"}]}]`,
		"threads":  "[" + reviewFixturePage("[]") + "]",
		"opinions": "[" + reviewFixturePage("[]") + "]",
		"gh": `#!/bin/sh
printf 'CALL\n' >> "$LW_REVIEW_FIXTURES/calls"
printf '%s\n' "$@" >> "$LW_REVIEW_FIXTURES/calls"
case "$1 $2" in
  'pr list') cat "$LW_REVIEW_FIXTURES/prs" ;;
  'api graphql')
    if test -e "$LW_REVIEW_FIXTURES/unavailable"; then exit 1; fi
    for arg do
      case "$arg" in
        query=*reviewThreads*) cat "$LW_REVIEW_FIXTURES/threads"; exit ;;
        query=*latestOpinionatedReviews*) cat "$LW_REVIEW_FIXTURES/opinions"; exit ;;
      esac
    done
    exit 2 ;;
  'pr merge')
    for arg do
      if test "$arg" = --auto && test -e "$LW_REVIEW_FIXTURES/refuse-auto"; then
        printf '%s\n' 'auto merge is not allowed' >&2; exit 1
      fi
    done
    if test -e "$LW_REVIEW_FIXTURES/changed-head"; then
      printf '%s\n' 'head does not match' >&2; exit 1
    fi ;;
  *) exit 2 ;;
esac
`,
	}
	for name, contents := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o700))
	}

	return dir
}

func TestFindPullRequestReadsAllReviewPagesAndKeepsUnresolvedFindings(t *testing.T) { //nolint:paralleltest // fake gh uses PATH and fixture environment
	dir := fakeReviewGH(t)
	threads := "[" + reviewFixturePage(`[
      {"isResolved":true,"isOutdated":false},
      {"isResolved":false,"isOutdated":true,"comments":{"nodes":[{"url":"pr/9#outdated-finding"}]}}
    ]`) + "," + reviewFixturePage(`[
      {"isResolved":false,"isOutdated":false,"comments":{"nodes":[{"url":"pr/9#finding"}]}}
    ]`) + "]"
	opinions := "[" + reviewFixturePage(`[{"state":"APPROVED"},{"state":"DISMISSED"}]`) + "," +
		reviewFixturePage(`[{"state":"CHANGES_REQUESTED","url":"pr/9#review"}]`) + "]"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "threads"), []byte(threads), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "opinions"), []byte(opinions), 0o600))

	pr, err := FindPullRequestForTask("owner/repo", "task-1")
	require.NoError(t, err)
	require.NotNil(t, pr)
	assert.Equal(t, reviewedHead, pr.HeadRefOID)
	assert.Equal(t, []string{"unresolved review thread: pr/9#outdated-finding", "unresolved review thread: pr/9#finding", "changes requested: pr/9#review"}, pr.BlockingReviews)
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(calls), "--paginate\n--slurp"), "both review connections must be fully read")
}

func TestFindPullRequestRefusesChangedOrIncompleteReviewEvidence(t *testing.T) { //nolint:paralleltest // fake gh uses PATH and fixture environment
	for name, page := range map[string]string{
		"changed between connections": strings.ReplaceAll("["+reviewFixturePage("[]")+"]", reviewedHead, "new-head"),
		"changed on a later page":     "[" + reviewFixturePage("[]") + "," + strings.ReplaceAll(reviewFixturePage("[]"), reviewedHead, "new-head") + "]",
		"no pages":                    "[]",
		"missing PR":                  `[{"data":{"repository":{"pullRequest":null}}}]`,
		"missing connection":          `[{"data":{"repository":{"pullRequest":{"headRefOid":"` + reviewedHead + `"}}}}]`,
		"malformed JSON":              "not json",
	} {
		t.Run(name, func(t *testing.T) {
			dir := fakeReviewGH(t)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "opinions"), []byte(page), 0o600))
			pr, err := FindPullRequestForTask("owner/repo", "task-1")
			require.Error(t, err)
			assert.Nil(t, pr)
		})
	}
}

func TestFindPullRequestReturnsReviewTransportFailure(t *testing.T) { //nolint:paralleltest // fake gh uses PATH and fixture environment
	dir := fakeReviewGH(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "unavailable"), nil, 0o600))
	pr, err := FindPullRequestForTask("owner/repo", "task-1")
	require.ErrorContains(t, err, "read reviews")
	assert.Nil(t, pr)
}

func TestArmAutoMergePinsBothMergePathsToTheObservedHead(t *testing.T) { //nolint:paralleltest // fake gh uses PATH and fixture environment
	dir := fakeReviewGH(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "refuse-auto"), nil, 0o600))
	require.NoError(t, ArmAutoMerge("owner/repo", 9, reviewedHead))
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(calls), "--match-head-commit\n"+reviewedHead))
}

func TestArmAutoMergeRefusesMissingOrChangedHead(t *testing.T) { //nolint:paralleltest // fake gh uses PATH and fixture environment
	dir := fakeReviewGH(t)
	require.ErrorContains(t, ArmAutoMerge("owner/repo", 9, ""), "without the reviewed head")
	_, err := os.Stat(filepath.Join(dir, "calls"))
	require.ErrorIs(t, err, os.ErrNotExist, "a missing SHA must not invoke gh")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "changed-head"), nil, 0o600))
	require.ErrorContains(t, ArmAutoMerge("owner/repo", 9, reviewedHead), "head does not match")
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(calls), "CALL\n"), "a changed head must not trigger a fallback")
}
