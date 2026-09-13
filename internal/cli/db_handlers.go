package cli

import (
	"context"
	"errors"
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

// errDjangoRetired reports a verb whose entire implementation was the Django
// backend, and says what replaced it.
//
// Until #318 these verbs called `docker compose exec backend python manage.py`.
// The platform is a Go monolith (ADR-0018); there is no manage.py and no
// `backend` compose service, so every one of the fourteen call sites had been
// dead since the migration. They did not fail cleanly — they failed as a docker
// error about a missing service, which reads like a local environment problem
// rather than a retired command, so the CLI was inviting people to debug their
// docker setup for a capability that no longer exists.
//
// Each caller passes what actually replaced it. Where nothing did, it says so;
// a pointer to a plausible-sounding wrong verb is worse than admitting the gap,
// because the reader spends their time on the wrong thing.
func errDjangoRetired(verb, replacement string) error {
	return fmt.Errorf(
		"`lw %s` was implemented by the retired Django backend and has been dead since "+
			"the Go migration (ADR-0018) — there is no manage.py. %s",
		verb, replacement)
}

// Replacement guidance, kept together so the story stays consistent across the
// four files that had Django call sites.
const (
	// The schema is generated from the SST stamp, not diffed from ORM models,
	// so "make migrations" has no counterpart: `lw codegen go` emits schema.sql
	// whole.
	replByCodegen = "The schema is generated, not migrated from models: " +
		"`lw codegen go` emits schema.sql from the SST entity schemas."

	// django-tenants gave each tenant its own Postgres schema. The Go stack
	// keeps one schema and isolates by row: the generated DDL carries tenant_id
	// plus an RLS policy per table (verified: 45 RLS declarations, one `tenants`
	// table, in the platform's generated schema).
	replByRLS = "Per-tenant Postgres schemas were replaced by row-level security (ADR-0010): " +
		"one schema, tenant_id plus an RLS policy per table, emitted by `lw codegen go`."

	// Honest gap. backend/foundation/db/migrations/*.sql is the migration set;
	// no lw verb applies it yet, and inventing one here would be a feature
	// rather than the Django removal this change is.
	replNoVerbYet = "No lw verb applies migrations yet — the set is " +
		"backend/foundation/db/migrations/*.sql in lightwave-platform. Tracked separately."
)

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
		return errors.New("usage: lw db dump <env> [--output=<file>] [--tables=t1,t2]")
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
		return errors.New("usage: lw db restore <file> [--env=local] [--confirm]")
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

	// Reset used to finish by re-running Django migrations. It now leaves an
	// empty public schema and says so, rather than reporting success on a
	// half-done reset.
	return errDjangoRetired("db reset", "The schema was dropped and recreated but NOT repopulated. "+replNoVerbYet)
}

func dbMigrateHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errDjangoRetired("db migrate", replNoVerbYet)
}

func dbMakemigrationsHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errDjangoRetired("db makemigrations", replByCodegen)
}

func dbCheckHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errDjangoRetired("db check",
		"That was Django's system check, not a connectivity probe. For connectivity use `lw db shell`.")
}

func dbSchemaInitHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errDjangoRetired("db schema-init", replByRLS)
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
		return errors.New("usage: lw db schema-drop <schema-name> [--force]")
	}

	name := args[0]
	if name == "public" {
		return errors.New("refusing to drop 'public' schema (use lw db reset)")
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

func dbMigrateSchemasHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errDjangoRetired("db migrate-schemas", replByRLS)
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
