package cli

import (
	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Workspace setup commands",
	Long: `Setup and maintain the development workspace.

Examples:
  lw setup sync                 # Sync Python dependencies
  lw setup sync --dev           # Sync with dev dependencies
  lw setup venv                 # Recreate virtualenv
  lw setup lock                 # Regenerate uv.lock`,
}

var setupSyncDev bool

var setupSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync Python dependencies",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("root")
		if err != nil {
			return err
		}
		if setupSyncDev {
			return runMake(dir, "sync-dev")
		}
		return runMake(dir, "sync")
	},
}

var setupVenvCmd = &cobra.Command{
	Use:   "venv",
	Short: "Recreate virtualenv from scratch",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("root")
		if err != nil {
			return err
		}
		return runMake(dir, "venv")
	},
}

var setupLockCmd = &cobra.Command{
	Use:   "lock",
	Short: "Regenerate uv.lock",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("root")
		if err != nil {
			return err
		}
		return runMake(dir, "lock")
	},
}

func init() {
	setupSyncCmd.Flags().BoolVar(&setupSyncDev, "dev", false, "Include dev dependencies")

	setupCmd.AddCommand(setupSyncCmd)
	setupCmd.AddCommand(setupVenvCmd)
	setupCmd.AddCommand(setupLockCmd)
}
