//nolint:testpackage // needs internal access to rootCmd
package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelfSyncCmd_Registered(t *testing.T) {
	t.Parallel()

	// AssembleSurface already applies decommissions (root.go), so calling
	// applyDecommissions here re-ran it on the process-global rootCmd — a
	// second writer of a tree every other test reads in parallel. `-race
	// -shuffle=on` caught it racing IsAvailableCommand about one run in
	// eighteen. Going through assembleOnce keeps the assertion (that `self
	// sync` survives decommissioning) and makes this a pure reader.
	require.NoError(t, assembleOnce(), "assembling the shipped surface")

	self := findChild(rootCmd, "self")
	require.NotNil(t, self, "self command should be registered")
	sync := findChild(self, "sync")
	require.NotNil(t, sync, "self sync subcommand should be registered")
	require.NotNil(t, sync.RunE)
}
