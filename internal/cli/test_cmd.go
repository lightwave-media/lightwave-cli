package cli

import (
	"github.com/spf13/cobra"
)

var testVerbose bool

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Run tests",
	Long: `Run test suites across the monorepo.

Examples:
  lw test                       # Run full test suite
  lw test --verbose             # Run with verbose output
  lw test smoke                 # Run smoke tests only
  lw test infra                 # Run infrastructure tests
  lw test infra --quick         # Quick infrastructure tests`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if testVerbose {
			dir, err := resolveMakeDir("platform")
			if err != nil {
				return err
			}
			return runMake(dir, "test-v")
		}
		dir, err := resolveMakeDir("root")
		if err != nil {
			return err
		}
		return runMake(dir, "test")
	},
}

var testSmokeCmd = &cobra.Command{
	Use:   "smoke",
	Short: "Run smoke tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "test-smoke")
	},
}

var testInfraQuick bool

var testInfraCmd = &cobra.Command{
	Use:   "infra",
	Short: "Run infrastructure tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("catalog")
		if err != nil {
			return err
		}
		if testInfraQuick {
			return runMake(dir, "test-quick")
		}
		return runMake(dir, "test")
	},
}

func init() {
	testCmd.Flags().BoolVarP(&testVerbose, "verbose", "V", false, "Verbose test output")
	testInfraCmd.Flags().BoolVar(&testInfraQuick, "quick", false, "Run quick tests only")

	testCmd.AddCommand(testSmokeCmd)
	testCmd.AddCommand(testInfraCmd)
}
