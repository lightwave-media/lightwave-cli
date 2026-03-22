package cli

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var makeCmd = &cobra.Command{
	Use:   "make <scope> [target] [-- args]",
	Short: "Run Makefile targets across the monorepo",
	Long: `Run make targets from any scope in the LightWave monorepo.

Scopes: root, platform, cli, augusta, infra, catalog

Examples:
  lw make platform              # List targets in platform
  lw make platform test         # Run platform test target
  lw make root check            # Run root check target
  lw make cli build -- -j4      # Pass extra args to make`,
	Args:                  cobra.ArbitraryArgs,
	DisableFlagParsing:    false,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return showMakeHelp()
		}

		scope := args[0]
		if scope == "help" {
			return showMakeHelp()
		}
		if len(args) == 1 {
			return listMakeTargets(scope)
		}

		target := args[1]
		dir, err := resolveMakeDir(scope)
		if err != nil {
			return err
		}

		// Pass remaining args (after --) to make
		var extra []string
		if cmd.ArgsLenAtDash() >= 0 && cmd.ArgsLenAtDash() < len(args) {
			extra = args[cmd.ArgsLenAtDash():]
		}

		return runMake(dir, target, extra...)
	},
}

func showMakeHelp() error {
	fmt.Println(color.CyanString("Available scopes:"))
	fmt.Println()
	for scope, rel := range makeScopes {
		fmt.Printf("  %-12s %s\n", color.GreenString(scope), rel)
	}
	fmt.Println()
	fmt.Println("Usage: lw make <scope> [target]")
	return nil
}
