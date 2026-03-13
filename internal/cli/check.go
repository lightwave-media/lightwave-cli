package cli

import (
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run quality checks",
	Long: `Run code quality checks across the monorepo.

Examples:
  lw check                      # Run all pre-commit checks
  lw check ci                   # Run CI checks locally
  lw check fix                  # Auto-fix linting issues
  lw check ruff                 # Run ruff on backend
  lw check types                # TypeScript type-check
  lw check domains              # Lint API domain usage`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("root")
		if err != nil {
			return err
		}
		return runMake(dir, "check")
	},
}

var checkCICmd = &cobra.Command{
	Use:   "ci",
	Short: "Run CI checks locally",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("root")
		if err != nil {
			return err
		}
		return runMake(dir, "ci-local")
	},
}

var checkFixCmd = &cobra.Command{
	Use:   "fix",
	Short: "Auto-fix linting issues",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("root")
		if err != nil {
			return err
		}
		return runMake(dir, "fix")
	},
}

var checkRuffFix bool

var checkRuffCmd = &cobra.Command{
	Use:   "ruff",
	Short: "Run ruff on backend code",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		if checkRuffFix {
			return runMake(dir, "ruff-fix")
		}
		return runMake(dir, "ruff")
	},
}

var checkTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "Run TypeScript type-check",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "npm-type-check")
	},
}

var checkDomainsCmd = &cobra.Command{
	Use:   "domains",
	Short: "Lint API domain usage",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "lint-api-domains")
	},
}

func init() {
	checkRuffCmd.Flags().BoolVar(&checkRuffFix, "fix", false, "Auto-fix ruff issues")

	checkCmd.AddCommand(checkCICmd)
	checkCmd.AddCommand(checkFixCmd)
	checkCmd.AddCommand(checkRuffCmd)
	checkCmd.AddCommand(checkTypesCmd)
	checkCmd.AddCommand(checkDomainsCmd)
}
