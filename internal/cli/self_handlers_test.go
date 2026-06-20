//nolint:testpackage // needs internal access to rootCmd
package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelfSyncCmd_Registered(t *testing.T) {
	t.Parallel()
	applyDecommissions(rootCmd)
	self := findChild(rootCmd, "self")
	require.NotNil(t, self, "self command should be registered")
	sync := findChild(self, "sync")
	require.NotNil(t, sync, "self sync subcommand should be registered")
	require.NotNil(t, sync.RunE)
}
