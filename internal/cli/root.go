package cli

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	verbose bool
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "lw",
	Short: "LightWave CLI - Task management and scaffolding",
	Long: `LightWave CLI (lw) is a command-line tool for managing tasks,
scaffolding Django apps, and working with the LightWave platform.

Built with Go for speed. Direct PostgreSQL access for instant reads.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Load config before any command runs
		_, err := config.Load()
		return err
	},
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.config/lw/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	// Add subcommands
	rootCmd.AddCommand(taskCmd)
	rootCmd.AddCommand(sprintCmd)
	rootCmd.AddCommand(storyCmd)
	rootCmd.AddCommand(epicCmd)
	rootCmd.AddCommand(specCmd)
	rootCmd.AddCommand(githubCmd)
	rootCmd.AddCommand(orchestratorCmd)
	rootCmd.AddCommand(awsCmd)
	rootCmd.AddCommand(infraCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(versionCmd)

	// Monorepo workflow commands
	rootCmd.AddCommand(makeCmd)
	rootCmd.AddCommand(devCmd)
	rootCmd.AddCommand(dbCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(testCmd)
	rootCmd.AddCommand(cdnCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(emailCmd)
	rootCmd.AddCommand(metaCmd)
	rootCmd.AddCommand(codegenCmd)
	rootCmd.AddCommand(driftCmd)
	rootCmd.AddCommand(lineageCmd)
	rootCmd.AddCommand(docCmd)
}

// versionCmd shows version info
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("lw version 2.1.0 (Go)")
		fmt.Println("Built with direct PostgreSQL access")
		fmt.Println("Native runtime: LightWave Augusta (packages/lightwave-sys)")
	},
}

// configCmd manages configuration
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage CLI configuration",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Get()

		fmt.Println(color.CyanString("LightWave CLI Configuration"))
		fmt.Println()
		fmt.Printf("Environment: %s\n", color.YellowString(cfg.Environment))
		fmt.Printf("Tenant:      %s\n", color.YellowString(cfg.Tenant))
		fmt.Println()
		fmt.Println(color.CyanString("Database (Tier 2):"))
		fmt.Printf("  Host:     %s\n", cfg.Database.Host)
		fmt.Printf("  Port:     %d\n", cfg.Database.Port)
		fmt.Printf("  Database: %s\n", cfg.Database.Name)
		fmt.Printf("  User:     %s\n", cfg.Database.User)
		fmt.Println()
		fmt.Println(color.CyanString("API (Tier 3):"))
		fmt.Printf("  URL: %s\n", cfg.GetAPIURL())
		fmt.Printf("  Agent Key: %s\n", maskKey(config.GetAgentKey()))

		return nil
	},
}

func init() {
	configCmd.AddCommand(configShowCmd)
}

func maskKey(key string) string {
	if key == "" {
		return color.RedString("(not set)")
	}
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
