package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP surface (serve delegated to existing lw mcp when available)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve the schema MCP listener (lw mcp serve)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Prefer the installed lw binary's mcp serve if this stub is being developed
		// alongside the legacy tree; otherwise print the contract.
		if path, err := exec.LookPath("lw"); err == nil {
			c := exec.Command(path, append([]string{"mcp", "serve"}, args...)...)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			c.Stdin = os.Stdin
			return c.Run()
		}
		fmt.Fprintln(os.Stderr, "lw mcp serve: install full lw or wire MCP in this empty-tree binary")
		return fmt.Errorf("mcp serve not yet native in v2 empty-tree")
	},
}

func init() {
	mcpCmd.AddCommand(mcpServeCmd)
}
