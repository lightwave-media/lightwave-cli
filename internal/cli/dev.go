package cli

import (
	"github.com/spf13/cobra"
)

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Development lifecycle commands",
	Long: `Manage the local development environment.

Examples:
  lw dev start                  # Start services in background
  lw dev stop                   # Stop all services
  lw dev logs                   # Tail backend logs
  lw dev shell                  # Django shell_plus
  lw dev ssh                    # Bash in backend container
  lw dev domain cineos          # Start with cineos domain`,
}

var devStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start all services in background",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "start-bg")
	},
}

var devStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop all services",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "stop")
	},
}

var devLogsService string

var devLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Tail service logs",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		switch devLogsService {
		case "frontend":
			return runMake(dir, "logs-frontend")
		case "backend":
			return runMake(dir, "logs-backend")
		default:
			return runMake(dir, "logs")
		}
	},
}

var devShellCmd = &cobra.Command{
	Use:   "shell",
	Short: "Open Django shell_plus",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "shell")
	},
}

var devSSHCmd = &cobra.Command{
	Use:   "ssh",
	Short: "Bash shell in backend container",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "ssh")
	},
}

var devDomainCmd = &cobra.Command{
	Use:   "domain <cineos|lwm|js>",
	Short: "Start with a specific domain",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		target := "dev-" + args[0]
		return runMake(dir, target)
	},
}

func init() {
	devLogsCmd.Flags().StringVar(&devLogsService, "service", "", "Service to tail (backend, frontend)")

	devCmd.AddCommand(devStartCmd)
	devCmd.AddCommand(devStopCmd)
	devCmd.AddCommand(devLogsCmd)
	devCmd.AddCommand(devShellCmd)
	devCmd.AddCommand(devSSHCmd)
	devCmd.AddCommand(devDomainCmd)
}
