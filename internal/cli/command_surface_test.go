//nolint:testpackage // needs internal access to rootCmd + the command registry
package cli

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func findChild(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}

	return nil
}

// assembleOnce builds the shipped surface onto rootCmd, exactly once.
//
// Once, for two reasons. AssembleSurface is not idempotent — BuildDispatched
// attaches unconditionally, so a second call lists every schema-dispatched
// command twice and the duplicate guard below would fire on its own side
// effect. And sharing one assembly makes the order these tests run in
// irrelevant, which matters under `go test -shuffle=on`: an earlier draft had
// one test assemble while another read a pristine rootCmd, and the suite failed
// about 40% of runs depending on which went first.
var assembleOnce = sync.OnceValue(func() error {
	if _, err := config.Load(); err != nil {
		return err
	}

	return AssembleSurface(rootCmd)
})

// shippedSurface returns the assembled root — what `lw --help` actually prints.
//
// When commands.yaml is unreachable, BuildDispatched warns and attaches
// nothing, leaving only the hand-wired commands. That surface is too small to
// check, and it is the normal state on a machine with no lightwave-core
// checkout, so these tests skip.
//
// It must not be the state in CI. Set LW_SURFACE_GATE_STRICT=1 there and the
// skip becomes a failure, so a broken core checkout cannot quietly turn this
// gate back into the no-op it used to be (#350). A gate that skips in CI is
// indistinguishable from a gate that passes, which is the whole bug.
func shippedSurface(t *testing.T) *cobra.Command {
	t.Helper()

	require.NoError(t, assembleOnce(), "assembling the shipped surface")

	// `task` is dispatched from the stamp and never hand-wired, so its absence
	// means commands.yaml did not load. AttachOrphanTaskCommands only adds
	// verbs beneath an existing `task`; it never creates one.
	if findChild(rootCmd, "task") == nil {
		if os.Getenv("LW_SURFACE_GATE_STRICT") == "1" {
			t.Fatal("LW_SURFACE_GATE_STRICT=1 but no schema-dispatched commands were " +
				"attached — lightwave-core/src/schemas/interfaces/cli/commands.yaml " +
				"did not load, so this gate would have checked nothing")
		}

		t.Skip("no schema-dispatched commands (lightwave-core commands.yaml missing)")
	}

	return rootCmd
}

// TestCommandSurface_EveryExposedCommandIsAccountedFor is the trust gate: every
// command a release exposes must be named in one of the three status lists.
// A release tag must mean something.
//
// The gate used to walk rootCmd without assembling it. rootCmd holds only the
// hand-wired commands, so the whole schema-dispatched surface — 26 of 45 — was
// never examined, and CI passed green the entire time (#350). It now walks the
// assembled tree.
//
//nolint:paralleltest // shares the assembled rootCmd singleton
func TestCommandSurface_EveryExposedCommandIsAccountedFor(t *testing.T) {
	root := shippedSurface(t)

	for _, c := range root.Commands() {
		if c.Hidden {
			continue
		}

		name := c.Name()
		if VerifiedCommands[name] {
			continue
		}

		if _, unreviewed := UnreviewedCommands[name]; unreviewed {
			continue
		}

		assert.Failf(t, "unaccounted command on the shipped surface",
			"`lw %s` is EXPOSED but appears in none of VerifiedCommands, "+
				"UnreviewedCommands, or DecommissionedCommands. Verify it end-to-end and "+
				"add it to VerifiedCommands with its test, or decommission it. Do not add "+
				"it to UnreviewedCommands — that list only shrinks.", name)
	}
}

// TestCommandSurface_UnreviewedBacklogOnlyShrinks pins the backlog size so a new
// command cannot be parked in it. Lowering this number is the point; raising it
// should require saying so out loud in review.
//
//nolint:paralleltest // reads package globals; trivial
func TestCommandSurface_UnreviewedBacklogOnlyShrinks(t *testing.T) {
	const backlogAtGateArming = 26

	assert.LessOrEqualf(t, len(UnreviewedCommands), backlogAtGateArming,
		"UnreviewedCommands grew to %d. It was %d when the gate was armed and is "+
			"meant to shrink: verify a command end-to-end, move it to VerifiedCommands, "+
			"and drop this constant to match.", len(UnreviewedCommands), backlogAtGateArming)
}

// TestCommandSurface_NoCommandListedTwice catches a command reaching the root
// twice — once hand-wired in init(), once from the dispatcher. That is what
// happened to `runbook`: #335 shipped it on a hardcoded tree, #338 registered
// its handlers, and `lw --help` listed it twice.
//
// This asserts the user-visible symptom rather than the mechanism, so it holds
// however a command got there. legacyHardcodedDomains() is the escape hatch: a
// command still wired by hand belongs in it so the dispatcher skips it.
//
//nolint:paralleltest // shares the assembled rootCmd singleton
func TestCommandSurface_NoCommandListedTwice(t *testing.T) {
	root := shippedSurface(t)

	seen := map[string]int{}
	for _, c := range root.Commands() {
		seen[c.Name()]++
	}

	for name, n := range seen {
		assert.Equalf(t, 1, n,
			"`lw %s` is attached %d times, so `lw --help` lists it %d times — either add "+
				"it to legacyHardcodedDomains() so the dispatcher skips it, or drop its "+
				"rootCmd.AddCommand", name, n, n)
	}
}

//nolint:paralleltest // shares the assembled rootCmd singleton
func TestCommandSurface_DecommissionedAreOffline(t *testing.T) {
	root := shippedSurface(t)

	for name := range DecommissionedCommands {
		// Space-separated keys address a nested subcommand.
		c := root
		for part := range strings.FieldsSeq(name) {
			c = findChild(c, part)
			if c == nil {
				break
			}
		}

		if c == nil {
			continue // not registered in this build
		}

		assert.Truef(t, c.Hidden, "decommissioned %q must be hidden from --help", name)
		require.NotNilf(t, c.RunE, "decommissioned %q must have a disabling RunE", name)

		err := c.RunE(c, nil)
		require.Errorf(t, err, "decommissioned %q must refuse to run", name)
		assert.Contains(t, err.Error(), "decommissioned")
	}
}

// TestCommandSurface_NoOverlap keeps the three status lists disjoint. A command
// in two of them has no single answer to "does this ship?".
//
//nolint:paralleltest // reads package globals; trivial
func TestCommandSurface_NoOverlap(t *testing.T) {
	for name := range VerifiedCommands {
		_, decommissioned := DecommissionedCommands[name]
		assert.Falsef(t, decommissioned, "%q is both Verified and Decommissioned", name)

		_, unreviewed := UnreviewedCommands[name]
		assert.Falsef(t, unreviewed, "%q is both Verified and Unreviewed", name)
	}

	for name := range UnreviewedCommands {
		_, decommissioned := DecommissionedCommands[name]
		assert.Falsef(t, decommissioned, "%q is both Unreviewed and Decommissioned", name)
	}
}
