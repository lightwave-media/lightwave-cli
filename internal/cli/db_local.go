package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/fatih/color"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

const (
	localPgPort    = "5432"
	localPgHost    = "localhost"
	localPgUser    = "postgres"
	localPgDBName  = "lightwave_platform"
	localPgDataDir = "/opt/homebrew/var/postgresql@14"
	brewService    = "postgresql@14"
)

var dbLocalCmd = &cobra.Command{
	Use:   "local",
	Short: "Manage local PostgreSQL (brew, always-on)",
	Long: `Manage the local brew PostgreSQL instance on port 5432.

This is the local platform database — same schema as Docker and production.
Migrations keep all environments in sync.

Examples:
  lw db local init     # First-time setup: initdb, start, create DB
  lw db local start    # Start brew postgres
  lw db local stop     # Stop brew postgres
  lw db local status   # Check health (exit 0 = healthy, 1 = not ready)`,
}

var dbLocalInitCmd = &cobra.Command{
	Use:   "init",
	Short: "First-time setup: initdb, start postgres, create database",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Step 1: initdb if data dir doesn't exist
		if _, err := os.Stat(localPgDataDir); os.IsNotExist(err) {
			fmt.Printf("  Initializing data directory at %s...\n", localPgDataDir)
			initdb := exec.Command("/opt/homebrew/opt/postgresql@14/bin/initdb", "-D", localPgDataDir)
			initdb.Stdout = os.Stdout
			initdb.Stderr = os.Stderr
			if err := initdb.Run(); err != nil {
				return fmt.Errorf("initdb failed: %w", err)
			}
			fmt.Println(color.GreenString("  Data directory initialized"))
		} else {
			fmt.Println("  Data directory already exists, skipping initdb")
		}

		// Step 2: Start postgres via pg_ctl (more reliable than brew services)
		if err := startLocalPg(); err != nil {
			return err
		}

		// Step 3: Wait for pg_isready
		if err := waitForPgReady(5 * time.Second); err != nil {
			return err
		}
		fmt.Println(color.GreenString("  PostgreSQL is ready"))

		// Step 4: Create user if not exists
		createUser := exec.Command("createuser", "-h", localPgHost, "-p", localPgPort, "-s", localPgUser)
		if err := createUser.Run(); err != nil {
			// Ignore — user likely already exists
			fmt.Println("  User 'postgres' already exists or created")
		} else {
			fmt.Println(color.GreenString("  Created superuser 'postgres'"))
		}

		// Step 5: Create database if not exists
		if dbExists() {
			fmt.Printf("  Database '%s' already exists\n", localPgDBName)
		} else {
			createDB := exec.Command("createdb", "-h", localPgHost, "-p", localPgPort, "-U", localPgUser, localPgDBName)
			createDB.Stdout = os.Stdout
			createDB.Stderr = os.Stderr
			if err := createDB.Run(); err != nil {
				return fmt.Errorf("createdb failed: %w", err)
			}
			fmt.Printf(color.GreenString("  Created database '%s'\n"), localPgDBName)
		}

		// Step 6: Register with brew services for auto-start on login
		register := exec.Command("brew", "services", "start", brewService)
		register.Stdout = os.Stdout
		register.Stderr = os.Stderr
		_ = register.Run() // best-effort

		// Print connection info
		fmt.Println()
		fmt.Println(color.CyanString("  Local PostgreSQL ready:"))
		fmt.Printf("    Host: %s\n", localPgHost)
		fmt.Printf("    Port: %s\n", localPgPort)
		fmt.Printf("    User: %s\n", localPgUser)
		fmt.Printf("    DB:   %s\n", localPgDBName)

		return nil
	},
}

var dbLocalStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start local brew PostgreSQL",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := startLocalPg(); err != nil {
			return err
		}
		if err := waitForPgReady(5 * time.Second); err != nil {
			return err
		}
		fmt.Printf(color.GreenString("  PostgreSQL running on port %s\n"), localPgPort)
		return nil
	},
}

var dbLocalStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop local brew PostgreSQL",
	RunE: func(cmd *cobra.Command, args []string) error {
		stop := exec.Command("/opt/homebrew/opt/postgresql@14/bin/pg_ctl",
			"-D", localPgDataDir, "stop", "-m", "fast")
		stop.Stdout = os.Stdout
		stop.Stderr = os.Stderr
		if err := stop.Run(); err != nil {
			return fmt.Errorf("pg_ctl stop failed: %w", err)
		}
		fmt.Println(color.GreenString("  PostgreSQL stopped"))
		return nil
	},
}

var dbLocalStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check local PostgreSQL health (exit 0 = healthy, 1 = not ready)",
	RunE: func(cmd *cobra.Command, args []string) error {
		pgReady := exec.Command("pg_isready", "-h", localPgHost, "-p", localPgPort)
		running := pgReady.Run() == nil

		hasDB := false
		if running {
			hasDB = dbExists()
		}

		if running {
			fmt.Printf("  PostgreSQL: %s (port %s)\n", color.GreenString("running"), localPgPort)
		} else {
			fmt.Printf("  PostgreSQL: %s\n", color.RedString("stopped"))
		}

		if hasDB {
			fmt.Printf("  Database:   %s (%s)\n", color.GreenString("exists"), localPgDBName)
		} else if running {
			fmt.Printf("  Database:   %s (run 'lw db local init')\n", color.YellowString("missing"))
		} else {
			fmt.Printf("  Database:   %s\n", color.RedString("unknown"))
		}

		if !running || !hasDB {
			os.Exit(1)
		}
		return nil
	},
}

// startLocalPg starts postgres via pg_ctl, cleaning stale PID if needed
func startLocalPg() error {
	// Check if already running
	check := exec.Command("pg_isready", "-h", localPgHost, "-p", localPgPort)
	if check.Run() == nil {
		fmt.Println("  PostgreSQL already running")
		return nil
	}

	// Clean stale PID file if process doesn't exist
	pidFile := localPgDataDir + "/postmaster.pid"
	if _, err := os.Stat(pidFile); err == nil {
		// PID file exists but pg_isready failed — stale
		fmt.Println("  Removing stale PID file...")
		os.Remove(pidFile)
	}

	fmt.Println("  Starting PostgreSQL...")
	start := exec.Command("/opt/homebrew/opt/postgresql@14/bin/pg_ctl",
		"-D", localPgDataDir,
		"-l", "/opt/homebrew/var/log/postgresql@14.log",
		"start")
	start.Stdout = os.Stdout
	start.Stderr = os.Stderr
	if err := start.Run(); err != nil {
		return fmt.Errorf("pg_ctl start failed: %w", err)
	}
	return nil
}

// waitForPgReady polls pg_isready until success or timeout
func waitForPgReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		check := exec.Command("pg_isready", "-h", localPgHost, "-p", localPgPort)
		if check.Run() == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("PostgreSQL not ready after %s", timeout)
}

// dbExists checks if the lightwave_platform database exists
func dbExists() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	dsn := fmt.Sprintf("host=%s port=%s dbname=%s user=%s sslmode=disable",
		localPgHost, localPgPort, localPgDBName, localPgUser)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return false
	}
	defer pool.Close()

	return pool.Ping(ctx) == nil
}

func init() {
	dbLocalCmd.AddCommand(dbLocalInitCmd)
	dbLocalCmd.AddCommand(dbLocalStartCmd)
	dbLocalCmd.AddCommand(dbLocalStopCmd)
	dbLocalCmd.AddCommand(dbLocalStatusCmd)
}
