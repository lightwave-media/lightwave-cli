//nolint:testpackage // needs shippedSurface + findChild, both unexported
package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lwInvocation matches an `lw <domain> <verb>` EXAMPLE line in a help string.
//
// Anchored to an indented line start, because help text says "lw" in prose too
// and a looser matcher reads those as invocations: `lw docs` opens with "The lw
// docs subtree drives..." (a noun) and `lw lint` with "lw lint validates
// rendered artifacts" (a verb). An unanchored \blw ([a-z-]+) ([a-z-]+) flags
// both, which is the too-broad-detector failure this repo has now paid for
// three times in one day — a detector wrong in the direction of accusing is
// worse than none, because it spends other people's time.
//
// Every real example in this codebase is an indented line under "Examples:".
var lwInvocation = regexp.MustCompile(`(?m)^[ \t]+lw ([a-z-]+) ([a-z-]+)`)

// availableChildren returns the subcommands a user can actually run, which is
// what cobra prints under "Available Commands".
//
// Decommissioned commands are attached but hidden and refuse to run
// (command_status.go), so IsAvailableCommand is the right question — not
// "is it in the tree".
func availableChildren(t *testing.T, parent *cobra.Command) map[string]bool {
	t.Helper()

	out := make(map[string]bool)

	for _, c := range parent.Commands() {
		if c.IsAvailableCommand() {
			out[c.Name()] = true
		}
	}

	return out
}

// TestHelpNeverAdvertisesAnUnavailableCommand is the guard for the defect #272
// names and the class #426 and #442 already paid for.
//
// `lw codegen --help` listed four generators. `journeys` was decommissioned and
// refuses to run; `models` and `api` described Django and Ninja, a stack
// ADR-0009 retired; `types` was marked "(planned)" while shipping. Every worked
// example invoked `journeys`. The two generators that do work — `go` and
// `types` — were unmentioned or mislabelled.
//
// The assertion is deliberately one-directional: help must not NAME a command
// that cannot run. It does not require help to mention every command, because
// that would force the restatement this fix removed — cobra derives the real
// list under "Available Commands".
func TestHelpNeverAdvertisesAnUnavailableCommand(t *testing.T) {
	t.Parallel()

	root := shippedSurface(t)

	for _, domain := range root.Commands() {
		if !domain.IsAvailableCommand() {
			continue
		}

		children := availableChildren(t, domain)
		if len(children) == 0 {
			continue // a leaf command has no subcommands to misadvertise
		}

		for _, m := range lwInvocation.FindAllStringSubmatch(domain.Long, -1) {
			named, verb := m[1], m[2]
			if named != domain.Name() {
				continue // an example invoking a different domain
			}

			assert.True(t, children[verb],
				"`%s --help` shows the example `lw %s %s`, but %q is not an "+
					"available subcommand. Help that names a command nobody can "+
					"run is the #426 failure in prose.",
				domain.Name(), named, verb, verb)
		}
	}
}

// TestCodegenHelpDoesNotRestateTheGeneratorList pins the fix's shape, not only
// its current text.
//
// The previous help kept its own copy of the generator list, which is the
// mechanism that let it drift four-for-four. Re-adding a hand-maintained list
// would pass the test above on the day it was written and rot the same way.
func TestCodegenHelpDoesNotRestateTheGeneratorList(t *testing.T) {
	t.Parallel()

	root := shippedSurface(t)

	codegen := findChild(root, "codegen")
	require.NotNil(t, codegen, "codegen is not on the shipped surface")

	long := codegen.Long

	// The retired stack, named as future work in a help string for months
	// after it was retired.
	for _, gone := range []string{"Django", "Ninja"} {
		assert.NotContains(t, long, gone,
			"codegen help still names %s, a stack ADR-0009 retired", gone)
	}

	assert.NotContains(t, strings.ToLower(long), "(planned)",
		"a generator marked (planned) is either shipped, in which case the label "+
			"is wrong, or absent, in which case cobra will not list it")

	assert.Contains(t, long, "Available Commands",
		"help should point at the list cobra derives rather than keeping its own")
}
