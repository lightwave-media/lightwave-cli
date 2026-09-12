package cli_test

import (
	"os"
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

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			// Vendored stamp content is a mirror of core and not ours to lint.
			if d.Name() == ".git" || d.Name() == "schemas" {
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		// This file names the banned strings on purpose.
		if strings.HasSuffix(path, "stale_paths_test.go") {
			return nil
		}

		src, readErr := os.ReadFile(path) //nolint:gosec // walking our own repo
		if readErr != nil {
			return readErr
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
					rel, _ := filepath.Rel(root, path)
					offenders = append(offenders,
						rel+": "+why+" — "+strings.TrimSpace(line))
				}
			}
		}

		return nil
	})
	require.NoError(t, err)

	require.Empty(t, offenders,
		"a path built from a dissolved layout resolves to nothing and every caller "+
			"treats nothing as an answer; see #387")
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
