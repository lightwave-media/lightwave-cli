package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
)

// Schema-driven db handlers. commands.yaml v3.0.0 declares 11 commands:
// shell, dump, restore, reset, migrate, makemigrations, check,
// schema-init, schema-list, schema-drop, migrate-schemas.
//
// Django-bound commands run via `docker compose exec backend python
// manage.py <args>`. Schema-level reads + drops talk to PG directly via
// the existing pgxpool. Destructive ops (reset, schema-drop, restore) gate
// behind --confirm or --force per the destructive-cmd convention.

func init() {
	RegisterHandler("db.shell", dbShellHandler)
	RegisterHandler("db.dump", dbDumpHandler)
	RegisterHandler("db.restore", dbRestoreHandler)
	RegisterHandler("db.reset", dbResetHandler)
	RegisterHandler("db.migrate", dbMigrateHandler)
	RegisterHandler("db.makemigrations", dbMakemigrationsHandler)
	RegisterHandler("db.check", dbCheckHandler)
	RegisterHandler("db.schema-init", dbSchemaInitHandler)
	RegisterHandler("db.schema-list", dbSchemaListHandler)
	RegisterHandler("db.schema-drop", dbSchemaDropHandler)
	RegisterHandler("db.migrate-schemas", dbMigrateSchemasHandler)
}

const dbExecTimeout = 15 * time.Minute

// djangoManage runs `docker compose exec backend python manage.py <args...>`
// streaming stdio. Bounded by dbExecTimeout to prevent silent hangs.
func djangoManage(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, dbExecTimeout)
	defer cancel()

	full := append([]string{"exec", "backend", "python", "manage.py"}, args...)
	return runCompose(ctx, full...)
}

// pgExec runs psql against the postgres container.
func pgExec(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, dbExecTimeout)
	defer cancel()
	full := append([]string{"exec", "db", "psql", "-U", "postgres"}, args...)
	return runCompose(ctx, full...)
}

// ---------------------------------------------------------------------------

func dbShellHandler(ctx context.Context, _ []string, flags map[string]any) error {
	env := flagStr(flags, "env")
	if env != "" && env != "local" {
		return fmt.Errorf("db shell: only --env=local supported (got %q)", env)
	}
	return pgExec(ctx, "lightwave_platform")
}

func dbDumpHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw db dump <env> [--output=<file>] [--tables=t1,t2]")
	}
	env := args[0]
	if env != "local" {
		return fmt.Errorf("db dump: remote env dumps not yet wired (got %q)", env)
	}
	out := flagStr(flags, "output")
	if out == "" {
		out = fmt.Sprintf("dump-%s-%s.sql", env, time.Now().Format("20060102-150405"))
	}
	cmdArgs := []string{"exec", "-T", "db", "pg_dump", "-U", "postgres", "lightwave_platform"}
	if t := flagStr(flags, "tables"); t != "" {
		for name := range strings.SplitSeq(t, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				cmdArgs = append(cmdArgs, "-t", name)
			}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, dbExecTimeout)
	defer cancel()
	c := composeCmd(ctx, cmdArgs...)
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("open output file: %w", err)
	}
	defer f.Close()
	c.Stdout = f
	if err := c.Run(); err != nil {
		return fmt.Errorf("pg_dump: %w", err)
	}
	fmt.Printf("Dumped → %s\n", color.CyanString(out))
	return nil
}

func dbRestoreHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw db restore <file> [--env=local] [--confirm]")
	}
	file := args[0]
	if !flagBool(flags, "confirm") {
		if !promptYesNo(fmt.Sprintf("Restore %s into %s? Existing data will be replaced.",
			file, flagStrOr(flags, "env", "local"))) {
			fmt.Println("Cancelled")
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, dbExecTimeout)
	defer cancel()
	c := composeCmd(ctx, "exec", "-T", "db", "psql", "-U", "postgres", "lightwave_platform")
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("open input file: %w", err)
	}
	defer f.Close()
	c.Stdin = f
	if err := c.Run(); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	fmt.Printf("Restored from %s\n", color.CyanString(file))
	return nil
}

func dbResetHandler(ctx context.Context, _ []string, flags map[string]any) error {
	if !flagBool(flags, "confirm") {
		if !promptYesNo("This drops + recreates the local DB and runs migrations. Continue?") {
			fmt.Println("Cancelled")
			return nil
		}
	}
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		return fmt.Errorf("drop public: %w", err)
	}
	fmt.Println("Public schema dropped + recreated")
	return djangoManage(ctx, "migrate")
}

func dbMigrateHandler(ctx context.Context, _ []string, flags map[string]any) error {
	args := []string{"migrate"}
	if flagBool(flags, "fake") {
		args = append(args, "--fake")
	}
	if v := flagStr(flags, "plan"); v != "" {
		// `--plan` is a Django boolean flag; if user supplied a value, ignore
		// and pass through as bool. (Schema lists --plan; bool table excludes
		// it because task.create reuses the name.)
		args = append(args, "--plan")
	}
	if v := flagStr(flags, "app"); v != "" {
		args = append(args, v)
	}
	return djangoManage(ctx, args...)
}

func dbMakemigrationsHandler(ctx context.Context, _ []string, flags map[string]any) error {
	args := []string{"makemigrations"}
	if v := flagStr(flags, "app"); v != "" {
		args = append(args, v)
	}
	if flagBool(flags, "empty") {
		args = append(args, "--empty")
	}
	if v := flagStr(flags, "name"); v != "" {
		args = append(args, "-n", v)
	}
	return djangoManage(ctx, args...)
}

func dbCheckHandler(ctx context.Context, _ []string, flags map[string]any) error {
	args := []string{"check"}
	if flagBool(flags, "deploy") {
		args = append(args, "--deploy")
	}
	if v := flagStr(flags, "fail-level"); v != "" {
		args = append(args, "--fail-level", v)
	}
	return djangoManage(ctx, args...)
}

func dbSchemaInitHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw db schema-init <schema-name>")
	}
	name := args[0]
	if err := djangoManage(ctx, "create_test_tenant", "--schema", name); err != nil {
		return err
	}
	if flagBool(flags, "skip-migrate") {
		return nil
	}
	return djangoManage(ctx, "migrate_schemas", "--schema", name)
}

func dbSchemaListHandler(ctx context.Context, _ []string, flags map[string]any) error {
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	rows, err := pool.Query(ctx, `
		SELECT schema_name FROM information_schema.schemata
		WHERE schema_name NOT IN ('pg_catalog','information_schema','pg_toast')
		  AND schema_name NOT LIKE 'pg_temp_%'
		  AND schema_name NOT LIKE 'pg_toast_temp_%'
		ORDER BY schema_name
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var schemas []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		schemas = append(schemas, n)
	}
	if asJSON(flags) {
		return emitJSON(schemas)
	}
	for _, s := range schemas {
		fmt.Println(s)
	}
	return nil
}

func dbSchemaDropHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw db schema-drop <schema-name> [--force]")
	}
	name := args[0]
	if name == "public" {
		return fmt.Errorf("refusing to drop 'public' schema (use lw db reset)")
	}
	if !flagBool(flags, "force") {
		if !promptYesNo(fmt.Sprintf("Drop schema %q (CASCADE)?", name)) {
			fmt.Println("Cancelled")
			return nil
		}
	}
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer db.Close()
	if !validIdent(name) {
		return fmt.Errorf("invalid schema name %q", name)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA %q CASCADE", name)); err != nil {
		return fmt.Errorf("drop schema: %w", err)
	}
	fmt.Printf("Dropped schema %s\n", color.RedString(name))
	return nil
}

func dbMigrateSchemasHandler(ctx context.Context, _ []string, flags map[string]any) error {
	args := []string{"migrate_schemas"}
	if v := flagStr(flags, "schema"); v != "" {
		args = append(args, "--schema", v)
	}
	return djangoManage(ctx, args...)
}

// validIdent rejects anything but [A-Za-z0-9_] to gate raw schema names
// going into a DROP statement. pgx's Exec doesn't parameterize identifiers.
func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

func flagStrOr(flags map[string]any, name, fallback string) string {
	if v := flagStr(flags, name); v != "" {
		return v
	}
	return fallback
}
