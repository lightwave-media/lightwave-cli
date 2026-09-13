package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNoDissolvedUmbrellaPathsInSource guards the whole class rather than the
// three sites #387 happened to name.
//
// Every one of those failed the same way and none of them reported it. The
// dissolved ~/dev/lightwave-media umbrella (`packages/`) and the pre-rebuild
// schema tree (`lightwave/schema/definitions/`) both resolve to nothing, and
// every caller treated "nothing" as an answer:
//
//   - check_schema_test.go stat'd one, so the armed drift gate's three tests
//     skipped on every machine for months
//   - db/lineage.go read one, so "falls back to hardcoded defaults" was not a
//     fallback but the only branch that ever ran
//   - cli/sst.go concat-read one, so a heuristic corpus was permanently empty
//     and always answered "no docs generator"
//
// A path that cannot resolve produces a clean, plausible, wrong answer. Catching
// the next one by grep is cheaper than catching it by incident, so this test
// scans the source instead of waiting for a fourth site.
func TestNoDissolvedUmbrellaPathsInSource(t *testing.T) {
	t.Parallel()

	// Fingerprints of PATH CONSTRUCTION, not of the words themselves.
	//
	// The first draft banned the bare word "definitions" and flagged eleven
	// lines of help text — "CLI command definitions", "YAML definitions" — plus
	// the NotContains assertions that exist to guard against this very pattern.
	// A detector that fires on prose is one people learn to ignore, so these
	// match only a filepath.Join segment (quoted, comma-terminated) or an
	// embedded path literal.
	banned := map[string]string{
		`"packages",`:                  "dissolved ~/dev/lightwave-media umbrella",
		`packages/lightwave-`:          "dissolved ~/dev/lightwave-media umbrella",
		`"definitions",`:               "pre-rebuild lightwave/schema/definitions layout",
		`lightwave/schema/definitions`: "pre-rebuild lightwave/schema/definitions layout",
		`"lightwave", "schema"`:        "pre-rebuild lightwave/schema/definitions layout",
	}

	root := repoRoot(t)

	var offenders []string

	for _, rel := range trackedGoFiles(t, root) {
		// This file names the banned strings on purpose.
		if strings.HasSuffix(rel, "stale_paths_test.go") {
			continue
		}

		// Vendored stamp content mirrors lightwave-core and is not ours to lint.
		if strings.Contains(filepath.ToSlash(rel), "/corestamp/schemas/") {
			continue
		}

		// A tracked file can legitimately be absent from the worktree — deleted
		// but not yet staged, or an intent-to-add entry whose file is gone. Hard
		//-erroring there fails the guard for a reason that has nothing to do
		// with what it guards, which is how it broke during #318.
		src, readErr := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // a git-tracked path in our own repo
		if readErr != nil {
			continue
		}

		for _, line := range strings.Split(string(src), "\n") {
			// Comments describing the historical bug are the point of the fix,
			// not a recurrence of it.
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "//") {
				continue
			}

			// Assertions that FORBID the pattern have to name it. Flagging
			// check_schema_test.go's NotContains guards would mean the class
			// detector and the site guard cancel each other out.
			//
			// The issue-reference escape covers the same guards' failure
			// messages, which sit on their own continuation lines where a
			// single-line NotContains test cannot see them. A line citing the
			// issue is describing the pattern, not committing it — and a real
			// path literal never contains spaces, which those messages do.
			if strings.Contains(line, "NotContains") || strings.Contains(line, "#387") {
				continue
			}

			for needle, why := range banned {
				if strings.Contains(line, needle) {
					offenders = append(offenders,
						rel+": "+why+" — "+strings.TrimSpace(line))
				}
			}
		}
	}

	require.Empty(t, offenders,
		"a path built from a dissolved layout resolves to nothing and every caller "+
			"treats nothing as an answer; see #387")
}

// trackedGoFiles returns the .go files git says THIS repository tracks.
//
// The first version of this test walked the filesystem from the module root,
// which is wrong on any machine that keeps worktrees inside the repo. On the
// canonical checkout it descended into .worktrees/cineos-fdx-workspace — a
// different checkout, on an old branch, still carrying the pre-#387 code — and
// reported six offenders that are not this branch's source at all.
//
// It passed everywhere it was tried before merging: in an isolated worktree
// (no nested checkouts) and in CI (a clean clone). So it was green in CI and
// red on a developer machine, which is the inverse of the failure #387 was
// about and arguably worse — CI is the thing you trust.
//
// `git ls-files` fixes the scope at the source: it lists what this repo tracks,
// so nested worktrees, build output and another session's untracked work are
// all excluded by construction rather than by a skip list that has to keep
// guessing at directory names. CLAUDE.md section 17 states the general rule —
// scope any gate to work you can attribute to yourself.
func trackedGoFiles(t *testing.T, root string) []string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", root, "ls-files", "-z", "*.go").Output()
	require.NoError(t, err, "git ls-files")

	var files []string

	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}

	require.NotEmpty(t, files, "git tracks no .go files — the scan would vacuously pass")

	return files
}

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)

	for range 10 {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "walked past the filesystem root without finding go.mod")

		dir = parent
	}

	t.Fatal("go.mod not found within 10 parents of the test working directory")

	return ""
}
