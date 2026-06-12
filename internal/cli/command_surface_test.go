//nolint:testpackage // needs internal access to rootCmd + the command registry
package cli

import (
	"strings"
	"testing"

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

// TestCommandSurface_OnlyVerifiedExposed is the trust gate: every command a
// release exposes must be end-to-end verified (in VerifiedCommands). If you add
// a command and this fails, either verify it + add a test + list it, or
// decommission it. A release tag must mean something.
//
//nolint:paralleltest // mutates the shared rootCmd via applyDecommissions
func TestCommandSurface_OnlyVerifiedExposed(t *testing.T) {
	applyDecommissions(rootCmd)

	for _, c := range rootCmd.Commands() {
		if c.Hidden {
			continue
		}

		assert.Truef(t, VerifiedCommands[c.Name()],
			"`lw %s` is EXPOSED but not verified — verify it end-to-end and add it to "+
				"VerifiedCommands, or add it to DecommissionedCommands", c.Name())
	}
}

//nolint:paralleltest // mutates the shared rootCmd via applyDecommissions
func TestCommandSurface_DecommissionedAreOffline(t *testing.T) {
	applyDecommissions(rootCmd)

	for name := range DecommissionedCommands {
		// Space-separated keys address a nested subcommand.
		c := rootCmd
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

//nolint:paralleltest // reads package globals; trivial
func TestCommandSurface_NoOverlap(t *testing.T) {
	for name := range VerifiedCommands {
		_, both := DecommissionedCommands[name]
		assert.Falsef(t, both, "%q is in both VerifiedCommands and DecommissionedCommands", name)
	}
}
