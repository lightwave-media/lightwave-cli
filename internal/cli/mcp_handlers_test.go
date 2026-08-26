//nolint:testpackage // needs LookupHandler
package cli

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestMCPServe_HandlerRegistered(t *testing.T) {
	t.Parallel()
	h, ok := LookupHandler("mcp.serve")
	require.True(t, ok, "mcp.serve must be registered in init()")
	require.NotNil(t, h)
}

func TestMCPServe_DispatchedFromStamp(t *testing.T) {
	t.Parallel()
	_, err := config.Load()
	if err != nil {
		t.Skipf("config load: %v", err)
	}
	root := &cobra.Command{Use: "lw"}
	require.NoError(t, BuildDispatched(root, map[string]bool{}))
	mcpCmd := findChild(root, "mcp")
	if mcpCmd == nil {
		t.Skip("stamp did not dispatch mcp (lightwave-core commands.yaml missing or mcp in_development)")
	}
	serve := findChild(mcpCmd, "serve")
	require.NotNil(t, serve, "mcp serve subcommand should be attached")
	require.NotNil(t, serve.RunE)
}
