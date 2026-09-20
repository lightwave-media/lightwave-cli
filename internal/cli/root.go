package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "lw",
	Short: "Lightwave CLI (v2 empty-tree — home, schema, check, mcp)",
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(homeCmd)
	rootCmd.AddCommand(schemaCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(mcpCmd)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
