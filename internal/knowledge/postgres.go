package knowledge

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed generated/notion/schema.sql generated/sync/schema.sql
var migrations embed.FS

func MigrationSQL() (string, error) {
	var sql strings.Builder

	for _, path := range []string{"generated/notion/schema.sql", "generated/sync/schema.sql"} {
		body, err := migrations.ReadFile(path)
		if err != nil {
			return "", err
		}

		sql.Write(body)
		sql.WriteByte('\n')
	}

	return sql.String(), nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	sql, err := MigrationSQL()
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('lightwave-knowledge-migrate'))"); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, sql); err != nil {
		return fmt.Errorf("apply stamped projection tables: %w", err)
	}

	for table, model := range map[string]any{"notion_databases": Database{}, "notion_pages": Page{}, "external_refs": Binding{}} {
		typ := reflect.TypeOf(model)
		for index := 0; index < typ.NumField(); index++ {
			column := strings.Split(typ.Field(index).Tag.Get("json"), ",")[0]

			var present bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2)", table, column).Scan(&present); err != nil {
				return err
			}

			if !present {
				return fmt.Errorf("existing %s table lacks stamped column %s; migration rolled back, an explicit upgrade is required", table, column)
			}
		}
	}

	return tx.Commit(ctx)
}

type Postgres struct{ Pool *pgxpool.Pool }

func (store Postgres) ProjectDatabase(ctx context.Context, database *Database) error {
	if database.TenantID == uuid.Nil {
		return errors.New("database projection requires an explicit tenant")
	}

	tx, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org',$1,true)", database.TenantID.String()); err != nil {
		return err
	}

	if err := projectRow(ctx, tx, "notion_databases", "notion_id", *database); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

//nolint:gocritic // Value snapshots isolate reconciliation from mutations of generated records.
func (store Postgres) Project(ctx context.Context, page Page, binding Binding) error {
	if page.TenantID == uuid.Nil || page.TenantID != binding.TenantID {
		return errors.New("projection requires matching explicit tenant identities")
	}

	tx, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org',$1,true)", page.TenantID.String()); err != nil {
		return err
	}

	if err := projectRow(ctx, tx, "notion_pages", "notion_id", page); err != nil {
		return err
	}

	if err := projectRow(ctx, tx, "external_refs", "binding_key", binding); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// projectRow uses the generated struct's tags, so column additions follow the
// stamp. Only these two fixed table names are allowed; no provider SQL enters it.
func projectRow(ctx context.Context, tx pgx.Tx, table, key string, row any) error {
	if table != "notion_pages" && table != "external_refs" && table != "notion_databases" {
		return errors.New("unsupported projection table")
	}

	data, err := json.Marshal(row)
	if err != nil {
		return err
	}

	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}
	// The generated model represents these JSONB columns as serialized strings.
	for _, name := range []string{"properties_json", "metadata"} {
		var value string
		if raw, ok := record[name]; ok && json.Unmarshal(raw, &value) == nil && value != "" {
			if !json.Valid([]byte(value)) {
				return fmt.Errorf("invalid JSON in %s", name)
			}

			record[name] = json.RawMessage(value)
		}
	}

	data, err = json.Marshal(record)
	if err != nil {
		return err
	}

	var columns, updates []string

	typeOf := reflect.TypeOf(row)
	for i := 0; i < typeOf.NumField(); i++ {
		name := strings.Split(typeOf.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}

		column := pgx.Identifier{name}.Sanitize()

		columns = append(columns, column)
		if name != "id" && name != "tenant_id" {
			updates = append(updates, column+"=EXCLUDED."+column)
		}
	}

	identifier := pgx.Identifier{table}.Sanitize()
	query := "INSERT INTO " + identifier + " (" + strings.Join(columns, ",") + ") SELECT " + strings.Join(columns, ",") + " FROM jsonb_populate_record(NULL::" + identifier + ",$1::jsonb) ON CONFLICT (tenant_id," + pgx.Identifier{key}.Sanitize() + ") DO UPDATE SET " + strings.Join(updates, ",")
	_, err = tx.Exec(ctx, query, string(data))

	return err
}
