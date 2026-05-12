package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/version"
	"github.com/spf13/cobra"
)

var versionJSON bool

var (
	cfgFile string
	verbose bool
	dbURL   string
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
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		// Highest-precedence override: explicit --db-url flag.
		if dbURL != "" {
			if err := config.ApplyDBURL(cfg, dbURL); err != nil {
				return err
			}
		}
		return nil
	},
}

// Execute runs the root command. Loads config, lets the schema-driven
// dispatcher attach migrated domains, then hands off to cobra.
func Execute() error {
	if _, err := config.Load(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := BuildDispatched(rootCmd, legacyHardcodedDomains()); err != nil {
		return err
	}
	// Attach handler-only commands that have not yet landed in the
	// lightwave-core schema. Becomes a no-op once each command has a
	// schema entry (see AttachOrphanTaskCommands for the cleanup path).
	AttachOrphanTaskCommands(rootCmd)
	return rootCmd.Execute()
}

// legacyHardcodedDomains returns the set of schema domain names still
// registered via hardcoded *Cmd in init() below. The dispatcher skips these
// to avoid Use-string collisions. Phase 4 migrates each domain to the
// dispatcher and removes its entry from this set.
func legacyHardcodedDomains() map[string]bool {
	return map[string]bool{
		"spec": true,
	}
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.config/lw/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().StringVar(&dbURL, "db-url", "", "platform DB DSN (overrides LW_DB_URL and config.yaml)")

	// Domains migrated to the schema dispatcher (handlers register in
	// init() of the corresponding *_handlers.go file): task, sprint, story,
	// epic, db, check, infra, plan, schema, spec, deploy, context, scaffold,
	// local. Legacy cobra trees for those domains were removed in the Phase 5
	// sweep; helper funcs that the handlers still call (printTaskTable,
	// printSprintTable, runTaskCreate, etc.) remain in the original *.go
	// files as plain package functions.
	//
	// specCmd is kept because `lw spec generate <task-id>` (the execution-spec
	// generator) is a distinct command from schema's `spec.generate-tasks`.
	// Until the legacy semantics are renamed or merged, the dispatcher's spec
	// tree is parked behind the spec entry in legacyHardcodedDomains().
	rootCmd.AddCommand(specCmd)
	rootCmd.AddCommand(githubCmd)
	rootCmd.AddCommand(awsCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(versionCmd)

	// Standalone utilities not modelled as schema domains.
	rootCmd.AddCommand(makeCmd)
	rootCmd.AddCommand(testCmd)
	rootCmd.AddCommand(cdnCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(emailCmd)
	rootCmd.AddCommand(codegenCmd)
	rootCmd.AddCommand(driftCmd)
	rootCmd.AddCommand(contentCmd)
	rootCmd.AddCommand(sstCmd)
	rootCmd.AddCommand(auditCmd)
	rootCmd.AddCommand(healthCmd)
	rootCmd.AddCommand(browserCmd)
	rootCmd.AddCommand(worktreeCmd)
	rootCmd.AddCommand(councilCmd)
	rootCmd.AddCommand(agentCmd)
	rootCmd.AddCommand(memoryCmd)
}

// versionCmd shows version info
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long: `Print version information.

The --json flag emits machine-readable output including a per-subsystem API
version map. Plugins and scripts that depend on lw subcommands should pin a
minimum API version for the subsystem they call (e.g. "paperclip") rather
than the binary's release version — release tags can move without breaking
any subsystem, and subsystem APIs can break between patch releases.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if versionJSON {
			payload := map[string]any{
				"version": version.Version,
				"commit":  version.Commit,
				"date":    version.Date,
				"apis":    version.APIs(),
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}
		fmt.Printf("lw version %s\n", version.Version)
		fmt.Printf("  commit: %s\n", version.Commit)
		fmt.Printf("  built:  %s\n", version.Date)
		apis := version.APIs()
		if len(apis) > 0 {
			fmt.Println("  apis:")
			for name, v := range apis {
				fmt.Printf("    %s: %d\n", name, v)
			}
		}
		return nil
	},
}

func init() {
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "output JSON")
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
		if cfg.Database.URL != "" {
			fmt.Printf("  URL:      %s\n", cfg.Database.URL)
		}
		fmt.Printf("  Host:     %s\n", cfg.DisplayHost())
		fmt.Printf("  Port:     %d\n", cfg.DisplayPort())
		fmt.Printf("  Database: %s\n", cfg.Database.Name)
		fmt.Printf("  User:     %s\n", cfg.Database.User)
		fmt.Println()
		fmt.Println(color.CyanString("API (Tier 3):"))
		fmt.Printf("  URL: %s\n", cfg.GetAPIURL())
		fmt.Printf("  Agent Key: %s\n", maskKey(config.GetAgentKey()))

		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config key in ~/.config/lw/config.yaml",
	Long: fmt.Sprintf(`Persist a config key to ~/.config/lw/config.yaml. Creates the file
(and parent directory) if missing. Subsequent commands use the new value.

Settable keys: %v

Examples:
  lw config set database.url postgres://lw@localhost:5433/lightwave_platform
  lw config set database.host 127.0.0.1
  lw config set tenant lwm_core`, config.SettableKeys()),
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return config.Set(args[0], args[1])
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get the resolved value of a config key",
	Long: `Print the value of a config key after resolution (flag > env > file > default).
Exits 0 with the value on stdout when set, exits 1 silently when unset.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, ok := config.Lookup(args[0])
		if !ok {
			os.Exit(1)
		}
		fmt.Println(v)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
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
