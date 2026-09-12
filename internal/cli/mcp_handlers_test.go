//nolint:testpackage // needs LookupHandler and findChild
package cli

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/sst"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestMCPStampCommandsHaveHandlers(t *testing.T) {
	t.Parallel()

	for _, cmd := range mcpStampCommands(t) {
		t.Run(cmd.Name, func(t *testing.T) {
			t.Parallel()

			_, ok := LookupHandler("mcp." + cmd.Name)
			require.True(t, ok, "stamp declares mcp %s; handler is missing", cmd.Name)
		})
	}
}

func TestMCPStampCommandsDispatch(t *testing.T) {
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

	for _, cmd := range mcpStampCommands(t) {
		// Resolved here rather than inside the subtest: findChild calls
		// cobra's Commands(), which sorts the child slice IN PLACE on first
		// call. Calling it from parallel subtests races on that sort, and the
		// race detector only catches it once the tree is large enough for the
		// sort to still be running when the next subtest reads — which is why
		// it stayed hidden until the embedded stamp grew.
		child := findChild(mcpCmd, cmd.Name)

		t.Run(cmd.Name, func(t *testing.T) {
			t.Parallel()

			require.NotNil(t, child, "mcp %s subcommand should be attached from the stamp", cmd.Name)
			require.NotNil(t, child.RunE, "mcp %s must have a RunE", cmd.Name)
		})
	}
}

func mcpStampCommands(t *testing.T) []sst.CLICommand {
	t.Helper()

	cfg := config.Get()
	if cfg == nil {
		t.Skip("config not loaded; mcp stamp census skips")
	}

	stamp, err := sst.LoadCLIConfig(cfg.Paths.LightwaveRoot)
	require.NoError(t, err, "load CLI stamp")

	for _, domain := range stamp.Domains {
		if domain.Name == "mcp" {
			require.NotEmpty(t, domain.Commands, "mcp domain has no commands")

			return domain.Commands
		}
	}

	t.Fatal("mcp domain missing from commands.yaml stamp")

	return nil
}
