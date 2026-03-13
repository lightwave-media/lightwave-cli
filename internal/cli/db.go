package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Database operations",
	Long: `Manage the LightWave database.

Examples:
  lw db migrate                 # Migrate all schemas
  lw db migrate --public        # Migrate public schema only
  lw db fresh                   # Create fresh migrations
  lw db tenant list             # List all tenants
  lw db tenant create           # Create a test tenant
  lw db reset                   # Quick reset (keeps migrations)
  lw db nuclear                 # Full nuclear reset`,
}

var dbMigratePublic bool

var dbMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database migrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		if dbMigratePublic {
			return runMake(dir, "migrate-public")
		}
		return runMake(dir, "migrate")
	},
}

var dbFreshCmd = &cobra.Command{
	Use:   "fresh",
	Short: "Create fresh migrations in dependency order",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "migrate-fresh")
	},
}

var dbTenantCmd = &cobra.Command{
	Use:   "tenant",
	Short: "Tenant management",
}

var dbTenantListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tenants/schemas",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "tenant-list")
	},
}

var dbTenantCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a test tenant",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "tenant-create")
	},
}

var dbResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Quick reset (keeps migrations, resets DB + tenants)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Print("This will drop the database and recreate tenants. Continue? [y/N] ")
		var confirm string
		fmt.Scanln(&confirm)
		if confirm != "y" && confirm != "Y" {
			fmt.Println("Cancelled")
			return nil
		}
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "quick-reset")
	},
}

var dbNuclearCmd = &cobra.Command{
	Use:   "nuclear",
	Short: "Full nuclear reset (destroys everything)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Print("NUCLEAR RESET: This will destroy ALL data and migrations. Type 'yes' to confirm: ")
		var confirm string
		fmt.Scanln(&confirm)
		if confirm != "yes" {
			fmt.Println("Cancelled")
			return nil
		}
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}
		return runMake(dir, "nuclear-reset")
	},
}

func init() {
	dbMigrateCmd.Flags().BoolVar(&dbMigratePublic, "public", false, "Migrate public schema only")

	dbTenantCmd.AddCommand(dbTenantListCmd)
	dbTenantCmd.AddCommand(dbTenantCreateCmd)

	dbCmd.AddCommand(dbMigrateCmd)
	dbCmd.AddCommand(dbFreshCmd)
	dbCmd.AddCommand(dbTenantCmd)
	dbCmd.AddCommand(dbResetCmd)
	dbCmd.AddCommand(dbNuclearCmd)
}
