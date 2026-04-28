package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
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
		_, _ = fmt.Scanln(&confirm)
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
		_, _ = fmt.Scanln(&confirm)
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

var dbCleanupDryRun bool

var dbCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Drop orphaned test schemas (test_*, unique_schema_*)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		rows, err := pool.Query(ctx, `
			SELECT schema_name FROM information_schema.schemata
			WHERE schema_name LIKE 'test_%' OR schema_name LIKE 'unique_schema_%'
			ORDER BY schema_name
		`)
		if err != nil {
			return fmt.Errorf("failed to query schemas: %w", err)
		}
		defer rows.Close()

		var schemas []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			schemas = append(schemas, name)
		}

		if len(schemas) == 0 {
			fmt.Println("No orphaned test schemas found")
			return nil
		}

		fmt.Printf("Found %s orphaned test schemas:\n", color.YellowString("%d", len(schemas)))
		for _, s := range schemas {
			fmt.Printf("  %s\n", s)
		}

		if dbCleanupDryRun {
			fmt.Println(color.CyanString("\nDry run — no schemas dropped"))
			return nil
		}

		fmt.Printf("\nDrop all %d schemas? [y/N] ", len(schemas))
		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if confirm != "y" && confirm != "Y" {
			fmt.Println("Cancelled")
			return nil
		}

		dropped := 0
		for _, s := range schemas {
			_, err := pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE",
				strings.ReplaceAll(s, "'", "''")))
			if err != nil {
				fmt.Printf("  %s %s: %v\n", color.RedString("FAIL"), s, err)
			} else {
				fmt.Printf("  %s %s\n", color.GreenString("DROP"), s)
				dropped++
			}
		}

		fmt.Printf("\nDropped %s of %d schemas\n",
			color.GreenString("%d", dropped), len(schemas))
		return nil
	},
}

func init() {
	dbCleanupCmd.Flags().BoolVar(&dbCleanupDryRun, "dry-run", false, "List schemas without dropping")

	dbMigrateCmd.Flags().BoolVar(&dbMigratePublic, "public", false, "Migrate public schema only")

	dbTenantCmd.AddCommand(dbTenantListCmd)
	dbTenantCmd.AddCommand(dbTenantCreateCmd)

	dbCmd.AddCommand(dbLocalCmd)
	dbCmd.AddCommand(dbMigrateCmd)
	dbCmd.AddCommand(dbFreshCmd)
	dbCmd.AddCommand(dbTenantCmd)
	dbCmd.AddCommand(dbResetCmd)
	dbCmd.AddCommand(dbNuclearCmd)
	dbCmd.AddCommand(dbCleanupCmd)
}
