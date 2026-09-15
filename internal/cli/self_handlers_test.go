//nolint:testpackage // needs internal access to rootCmd
package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// AssembleSurface already applies the decommission policy (root.go), so this
// used to call applyDecommissions a second time — from a parallel test body,
// against the process-global rootCmd. disableSubtree writes to the commands it
// walks, so that redundant call raced every other parallel test reading the
// same tree. Going through shippedSurface instead gets the same policy applied
// once, under the assembleOnce happens-before edge.
func TestSelfSyncCmd_Registered(t *testing.T) {
	t.Parallel()

	self := findChild(shippedSurface(t), "self")
	require.NotNil(t, self, "self command should be registered")
	sync := findChild(self, "sync")
	require.NotNil(t, sync, "self sync subcommand should be registered")
	require.NotNil(t, sync.RunE)
}
