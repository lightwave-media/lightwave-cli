//nolint:testpackage // reuses shippedSurface + rootCmd from command_surface_test.go
package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The README enumerates the top-level command surface by hand, and nothing
// checked it. It was correct when measured — 45 claimed, 45 listed, 45 built —
// but correct by maintenance rather than by construction, and every command
// added or decommissioned since has had to remember to edit it.
//
// The README makes this argument itself, about gates:
//
//	a check which reports success without actually covering the surface is
//	worse than no check
//
// An unchecked list of what ships is the documentation form of that. So the
// list is now a checked artifact: the count and the names both have to match
// what `lw --help` would print.
//
// Deliberately NOT asserted: prose. This pins the enumerations that rot
// silently, not the wording around them.

// readmeSurfaceBlock finds "N top-level commands ship in the current build:"
// and the fenced block under it, returning the claimed count and names.
var readmeSurfaceBlock = regexp.MustCompile(
	"(?s)(\\d+) top-level commands ship in the current build:\\s*```\\n(.*?)```")

func repoRootFromGit(t *testing.T) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel").Output()
	require.NoError(t, err, "git rev-parse --show-toplevel")

	return strings.TrimSpace(string(out))
}

// parseReadmeClaims is split out as a pure function so the extractor itself can
// be driven against a README that does NOT contain the block.
//
// Without that, the guard only ever sees input it can parse, and a regex that
// silently stops matching — because the section was reworded, or the fence
// changed — would turn the whole check into a no-op that still reports success.
// That is the exact failure this file exists to prevent, so the extractor gets
// the same both-directions treatment (CLAUDE.md section 18).
func parseReadmeClaims(src string) (claimed int, names []string, err error) {
	m := readmeSurfaceBlock.FindStringSubmatch(src)
	if len(m) != 3 {
		return 0, nil, errors.New(
			"README.md no longer contains a \"<N> top-level commands ship in the current build:\" " +
				"block followed by a fence. If that section moved, move this guard with it — " +
				"do not delete it, or the list goes back to being unchecked")
	}

	claimed, err = strconv.Atoi(m[1])
	if err != nil {
		return 0, nil, fmt.Errorf("parsing the claimed command count: %w", err)
	}

	return claimed, strings.Fields(m[2]), nil
}

func readmeClaims(t *testing.T) (claimed int, names []string) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRootFromGit(t), "README.md")) //nolint:gosec // the repo's own README
	require.NoError(t, err, "reading README.md")

	claimed, names, err = parseReadmeClaims(string(raw))
	require.NoError(t, err)

	return claimed, names
}

// TestParseReadmeClaims drives the extractor both ways.
func TestParseReadmeClaims(t *testing.T) {
	t.Parallel()

	const good = "blah\n\n7 top-level commands ship in the current build:\n\n```\naudit check\ndb\n```\n\nmore prose"

	t.Run("extracts count and names", func(t *testing.T) {
		t.Parallel()

		claimed, names, err := parseReadmeClaims(good)
		require.NoError(t, err)
		assert.Equal(t, 7, claimed)
		assert.Equal(t, []string{"audit", "check", "db"}, names)
	})

	// The rejection path. A README with no such block must fail loudly rather
	// than yield an empty list, because an empty list compares equal to nothing
	// and the guard would pass while checking no commands at all.
	for name, src := range map[string]string{
		"no block at all":    "just prose, no command inventory here",
		"sentence gone":      "```\naudit check\n```",
		"fence gone":         "7 top-level commands ship in the current build:\n\naudit check\n",
		"count not a number": "many top-level commands ship in the current build:\n```\naudit\n```",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, names, err := parseReadmeClaims(src)
			require.Error(t, err, "an unparsable README must not read as zero commands")
			assert.Empty(t, names)
		})
	}
}

// shippedCommandNames returns what `lw --help` lists: available (non-hidden)
// children of the assembled root, including cobra's own help and completion.
//
// Those two are attached by assembleOnce rather than here. They used to be
// initialised in this function, which made it a WRITER to the process-global
// rootCmd sitting outside the once-gate — so it raced every parallel test that
// read the same tree, and `go test -race -shuffle=on` failed intermittently
// naming whichever reader happened to lose. See assembleOnce's comment: this is
// the same defect it was written to fix, one layer out.
func shippedCommandNames(t *testing.T) []string {
	t.Helper()

	// assembleOnce does the InitDefault* calls now; doing them here made this
	// helper a writer of the shared root and raced every parallel reader.
	root := shippedSurface(t)

	var names []string

	for _, c := range root.Commands() {
		if c.IsAvailableCommand() || c.Name() == "help" {
			names = append(names, c.Name())
		}
	}

	sort.Strings(names)

	return names
}

func TestReadmeCommandListMatchesShippedSurface(t *testing.T) {
	t.Parallel()

	_, claimed := readmeClaims(t)
	actual := shippedCommandNames(t)

	sort.Strings(claimed)

	inReadmeOnly := missingFrom(claimed, actual)
	inBuildOnly := missingFrom(actual, claimed)

	assert.Empty(t, inReadmeOnly,
		"README.md advertises top-level commands the build does not ship: %v. "+
			"A README that lists a command nobody can run is the same failure as a "+
			"gate that passes without covering anything", inReadmeOnly)

	assert.Empty(t, inBuildOnly,
		"the build ships top-level commands README.md does not list: %v. "+
			"Regenerate the block with `lw --help`", inBuildOnly)
}

// TestReadmeCommandCountMatchesItsOwnList guards the sentence against the fence
// beneath it. They are two hand-maintained numbers for one fact, so they can
// disagree with each other without either matching the build.
func TestReadmeCommandCountMatchesItsOwnList(t *testing.T) {
	t.Parallel()

	claimed, names := readmeClaims(t)

	assert.Len(t, names, claimed,
		"README.md says %d top-level commands ship but its own list has %d entries",
		claimed, len(names))
}

// missingFrom returns the elements of want that do not appear in got.
func missingFrom(want, got []string) []string {
	have := make(map[string]bool, len(got))
	for _, g := range got {
		have[g] = true
	}

	var missing []string

	for _, w := range want {
		if !have[w] {
			missing = append(missing, w)
		}
	}

	return missing
}
