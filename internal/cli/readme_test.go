//nolint:testpackage // reuses shippedSurface + rootCmd from command_surface_test.go
package cli

import (
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

func readmeClaims(t *testing.T) (claimed int, names []string) {
	t.Helper()

	root := repoRootFromGit(t)

	raw, err := os.ReadFile(filepath.Join(root, "README.md")) //nolint:gosec // the repo's own README
	require.NoError(t, err, "reading README.md")

	m := readmeSurfaceBlock.FindStringSubmatch(string(raw))
	require.Len(t, m, 3,
		"README.md no longer contains a \"<N> top-level commands ship in the current build:\" "+
			"block followed by a fence. If that section moved, move this guard with it — "+
			"do not delete it, or the list goes back to being unchecked")

	claimed, err = strconv.Atoi(m[1])
	require.NoError(t, err)

	return claimed, strings.Fields(m[2])
}

// shippedCommandNames returns what `lw --help` lists: available (non-hidden)
// children of the assembled root, including cobra's own help and completion.
//
// Both of those are attached lazily during Execute, not by AssembleSurface, so
// a test that only assembles would report them missing and fail against a
// README that correctly lists them. Initialising them here is what makes the
// test tree match the shipped binary rather than an internal halfway state.
func shippedCommandNames(t *testing.T) []string {
	t.Helper()

	root := shippedSurface(t)
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

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
