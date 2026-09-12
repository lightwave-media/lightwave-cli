//nolint:testpackage // needs LookupHandler and findChild
package cli

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/sst"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// Both tests below are deliberately serial. They reach config.Get()/Load(),
// which is an unguarded lazy singleton — `if cfg == nil { cfg, _ = Load() }`
// with no mutex or sync.Once — and Load() writes the package-global viper via
// SetConfigName. Two parallel tests whose first call lands together race on
// both. Same reason command_surface_test.go carries //nolint:paralleltest for
// the shared rootCmd singleton.
//
// This raced only when these two ran without another test having loaded config
// first, so a full-package run hid it while `-run TestMCP` reproduced it every
// time. The underlying unguarded Get() is filed separately; serialising here
// is the fix for this file, not for that.

//nolint:paralleltest // races on the config/viper global singleton via config.Get
func TestMCPStampCommandsHaveHandlers(t *testing.T) {
	for _, cmd := range mcpStampCommands(t) {
		_, ok := LookupHandler("mcp." + cmd.Name)
		require.True(t, ok, "stamp declares mcp %s; handler is missing", cmd.Name)
	}
}

//nolint:paralleltest // races on the config/viper global singleton via config.Load
func TestMCPStampCommandsDispatch(t *testing.T) {
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

	// Resolved here, on one goroutine, rather than inside parallel subtests.
	// findChild calls cobra's Commands(), which lazily sorts the parent's
	// command slice IN PLACE and flips commandsAreSorted (cobra command.go:1295)
	// — an unsynchronised write. Calling it from parallel subtests raced on
	// every run under -race and turned `mise run ci` red. Same loop shape as
	// runbook_test.go, which walks its subcommands serially for this reason.
	for _, cmd := range mcpStampCommands(t) {
		child := findChild(mcpCmd, cmd.Name)
		require.NotNil(t, child, "mcp %s subcommand should be attached from the stamp", cmd.Name)
		require.NotNil(t, child.RunE, "mcp %s must have a RunE", cmd.Name)
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
