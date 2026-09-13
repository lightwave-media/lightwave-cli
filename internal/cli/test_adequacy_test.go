package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #128 filed a test-adequacy finding and proposed a 200-LOC cap on test files.
// Measuring it first falsified both mechanisms the issue named, and surfaced the
// gap the issue was actually groping toward. The numbers, on main @ 88cd8b2:
//
//   - "Bloat correlates with duplicated fixture setup." The repo already runs
//     dupl (threshold 120) and excluded _test.go from it. Lifting that exclusion
//     and running dupl across the tree reports EIGHT duplicate blocks, and zero
//     of them are in a test file. All eight are production pairs (audit.go
//     against itself, epic/story, epic_handlers/sprint_handlers, db epics
//     /stories). There is no duplicated fixture setup to extract.
//
//   - "Large files mask mutation gaps." Inverted: 42% of files over 200 LOC
//     carry no rejection-path assertion, against 53% of the files under it. The
//     big files are BETTER covered. Splitting them would move work from the
//     better-tested half of the suite into the worse-tested half.
//
//     (The PR that introduced this file reported 68% against 64% — computed
//     with the narrow first-pass regex described at rejectionPathAssertion,
//     before `if err == nil` was added. Those numbers were wrong. The
//     conclusion they supported — no LOC cap — survives the correction and is
//     strengthened by it, but the figures themselves should not be quoted.)
//
// What is real: 47 of 92 test files (51%) never assert that anything is
// REJECTED. They test only the happy path. That is the adequacy gap, it does
// not track file length, and a LOC cap would not have moved it — while
// actively discouraging the one thing that helps, which is writing more tests.
//
// So this guard measures the gap itself rather than a proxy for it, and
// ratchets rather than caps. CLAUDE.md section 18 and this repo's own
// `lw check` rules already require both directions of every detector; this is
// that existing rule applied to the test suite.
//
// KNOWN BLIND SPOT, by construction: it scans `git ls-files`, so a brand-new
// test file is invisible until it is staged. `mise run ci` on an unstaged file
// therefore passes, and the same tree fails at `git add` + pre-push. That is
// the #388 class — a gate blind to untracked work — and it is kept on purpose:
// the alternative is a filesystem walk, which is what #404 had to be reverted
// for after it descended into a nested worktree and linted another session's
// checkout. Staged-only is the narrower wrong answer, and it is wrong in the
// safe direction. Stage before you trust a local pass.
func TestRejectionPathCoverageDoesNotRegress(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	var silent []string

	for _, rel := range trackedGoFiles(t, root) {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}

		// Vendored stamp content mirrors lightwave-core and is not ours to hold
		// to this repo's conventions.
		if strings.HasPrefix(filepath.ToSlash(rel), "internal/corestamp/") {
			continue
		}

		// Tracked but absent from the worktree — deleted and not yet staged, or
		// an intent-to-add entry. Failing here would fail the guard for a reason
		// unrelated to what it guards, which is how the #387 guard broke.
		src, readErr := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // a git-tracked path in our own repo
		if readErr != nil {
			continue
		}

		if !hasRejectionPathAssertion(string(src)) {
			silent = append(silent, rel)
		}
	}

	assert.LessOrEqual(t, len(silent), rejectionPathSilentBaseline,
		"test files with no rejection-path assertion grew to %d (baseline %d). "+
			"A new test file should prove the code says no to something, not only "+
			"that it says yes — see #128. Lower rejectionPathSilentBaseline when "+
			"you close the gap in an existing file.\nSilent files:\n  %s",
		len(silent), rejectionPathSilentBaseline, strings.Join(silent, "\n  "))
}

// rejectionPathSilentBaseline is the number of test files asserting no rejection
// path when the gap was first measured (#128).
//
// A ratchet, not a threshold — the same posture as unstampedBaseline and as the
// golangci-lint --new-from-merge-base this repo already runs. Turning 47 files
// into a hard failure would produce a gate whose first act is to block every PR,
// and the reliable outcome of that is the gate being switched off.
//
// Lower it as files gain a rejection case. Raising it needs a reason in the
// commit message.
const rejectionPathSilentBaseline = 47

// rejectionPathAssertion matches the ways this repo says "expect this to fail".
//
// The vocabulary is measured, not guessed. A first pass used only the testify
// Error family plus wantErr and counted 59 silent files; adding `if err == nil`
// — the hand-rolled `if err == nil { t.Fatal("expected error") }`, which appears
// 18 times — dropped it to 47. Twelve files were being reported as untested on a
// rejection path while testing one. A detector that is wrong in the direction of
// accusing is worse than no detector, because it spends other people's time.
var rejectionPathAssertion = regexp.MustCompile(
	`(?:require|assert)\.Error(?:f|Is|As|Contains)?\b` + // testify
		`|\bwantErr\w*` + // table-driven field
		`|\bexpectErr\w*` + // table-driven field, older spelling
		`|\berrContains\b` + // table-driven field
		`|\bif err == nil\b`, // hand-rolled expect-an-error
)

// hasRejectionPathAssertion reports whether a test file proves the code under
// test refuses something, rather than only that it accepts.
func hasRejectionPathAssertion(src string) bool {
	return rejectionPathAssertion.MatchString(src)
}

// TestRejectionPathDetectorFiresBothWays drives the classifier against a
// known-bad and a known-good subject.
//
// CLAUDE.md section 18: a detector run only against the thing it expects to
// catch confirms itself. This session has already shipped two detectors that
// were too broad — one flagged eleven lines of help text, another flagged the
// NotContains guards written to forbid the very pattern it hunted — and both
// were caught by running them against subjects that must stay silent.
func TestRejectionPathDetectorFiresBothWays(t *testing.T) {
	t.Parallel()

	happyPathOnly := `
func TestThing(t *testing.T) {
	got, err := Parse("ok")
	require.NoError(t, err)
	assert.Equal(t, "ok", got)
	require.NotNil(t, got)
}`

	cases := map[string]struct {
		src  string
		want bool
	}{
		// Each of these asserts a rejection; the detector must see it.
		"testify require.Error": {`require.Error(t, err)`, true},
		"testify assert.Error":  {`assert.Error(t, err, "context")`, true},
		"testify ErrorIs":       {`require.ErrorIs(t, err, ErrNotFound)`, true},
		"testify ErrorContains": {`require.ErrorContains(t, err, "retired")`, true},
		"table wantErr":         {"cases := []struct{ wantErr bool }{{wantErr: true}}", true},
		"table expectError":     {"cases := []struct{ expectError bool }{}", true},
		"table errContains":     {`errContains: "no such file"`, true},
		"hand-rolled":           {"if err == nil {\n\tt.Fatal(\"expected error\")\n}", true},

		// None of these does; the detector must stay silent, or the baseline is
		// measuring the wrong thing.
		"happy path only":  {happyPathOnly, false},
		"NoError alone":    {`require.NoError(t, err)`, false},
		"errors imported":  {`import "errors"`, false},
		"err != nil guard": {"if err != nil {\n\tt.Fatal(err)\n}", false},
		"the word error":   {`// returns an error when the path is absolute`, false},
		"empty":            {"", false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, hasRejectionPathAssertion(tc.src))
		})
	}
}

// TestSuccessAssertionIsNotARejection pins the distinction the whole baseline
// rests on.
//
// `if err != nil { t.Fatal(err) }` is the commonest line in this suite (179
// occurrences) and it asserts the OPPOSITE of a rejection: it demands success.
// Counting it would mark nearly every file as covered, the baseline would read
// near zero, and the guard would be green because it is blind — which is the
// failure mode this class of guard exists to prevent.
func TestSuccessAssertionIsNotARejection(t *testing.T) {
	t.Parallel()

	require.False(t, hasRejectionPathAssertion("if err != nil {\n\tt.Fatal(err)\n}"),
		"a success assertion must never be read as a rejection assertion")
}
