package knowledge //nolint:testpackage // Verifies private migration/projection with a real isolated Postgres schema.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresRebuildPreservesPendingIntentAndTenantIsolation(t *testing.T) {
	t.Parallel()
	dsn := os.Getenv("LW_KNOWLEDGE_TEST_DSN")
	if dsn == "" {
		t.Skip("set LW_KNOWLEDGE_TEST_DSN to run isolated Postgres recovery verification")
	}
	admin, err := pgxpool.New(t.Context(), dsn)
	require.NoError(t, err)
	t.Cleanup(admin.Close)
	schema := "knowledge_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	_, err = admin.Exec(t.Context(), "CREATE SCHEMA "+quoted)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
		assert.NoError(t, cleanupErr)
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(t.Context(), "CREATE TABLE tenants(id uuid PRIMARY KEY)")
	require.NoError(t, err)
	require.NoError(t, Migrate(t.Context(), pool))
	require.NoError(t, Migrate(t.Context(), pool))
	engine, remote := engineFixture(t)
	_, err = pool.Exec(t.Context(), "INSERT INTO tenants(id) VALUES ($1)", remote.page.TenantID)
	require.NoError(t, err)
	engine.Projector = Postgres{Pool: pool}
	_, err = engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	var pages, bindings, databases int
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM notion_pages),(SELECT count(*) FROM external_refs),(SELECT count(*) FROM notion_databases)").Scan(&pages, &bindings, &databases))
	assert.Equal(t, 1, pages)
	assert.Equal(t, 1, bindings)
	assert.Equal(t, 1, databases)
	local, err := engine.Files.LoadPage(remote.page.NotionId)
	require.NoError(t, err)
	local.Markdown = ptr("unpublished after reset")
	_, err = engine.Files.SavePage(local)
	require.NoError(t, err)
	binding, err := engine.Files.LoadBinding(local.NotionId)
	require.NoError(t, err)
	binding.SyncStatus = StatusPending
	binding.PendingContentJson = []byte(`{"markdown":"unpublished after reset","properties":{}}`)
	require.NoError(t, engine.Files.SaveBinding(binding))
	_, err = pool.Exec(t.Context(), "TRUNCATE notion_pages,external_refs,notion_databases")
	require.NoError(t, err)
	report, err := Reindex(t.Context(), engine.Files, engine.Projector, false)
	require.NoError(t, err)
	assert.Len(t, report.Changes, 1)
	var markdown, status, intent string
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT p.markdown,r.sync_status,r.pending_content_json->>'markdown' FROM notion_pages p JOIN external_refs r ON p.id::text=r.local_id").Scan(&markdown, &status, &intent))
	assert.Equal(t, "unpublished after reset", markdown)
	assert.Equal(t, StatusPending, status)
	assert.Equal(t, markdown, intent)
	assert.Zero(t, remote.writes)
	wrong := binding
	wrong.TenantID = uuid.New()
	require.ErrorContains(t, engine.Projector.Project(t.Context(), local, wrong), "matching explicit tenant")
	// Prove the generated RLS policy under a non-superuser role; querying as the
	// administrative fixture owner alone would bypass it and prove nothing.
	role := "knowledge_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedRole := pgx.Identifier{role}.Sanitize()
	_, err = admin.Exec(t.Context(), "CREATE ROLE "+quotedRole+" NOLOGIN")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := admin.Exec(context.Background(), "DROP OWNED BY "+quotedRole+"; DROP ROLE "+quotedRole)
		assert.NoError(t, cleanupErr)
	})
	_, err = pool.Exec(t.Context(), "GRANT USAGE ON SCHEMA "+quoted+" TO "+quotedRole+"; GRANT SELECT ON ALL TABLES IN SCHEMA "+quoted+" TO "+quotedRole)
	require.NoError(t, err)
	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(t.Context(), "SET LOCAL ROLE "+quotedRole)
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), "SELECT set_config('app.current_org',$1,true)", uuid.NewString())
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(t.Context(), "SELECT count(*) FROM notion_pages").Scan(&pages))
	assert.Zero(t, pages)
	_, err = tx.Exec(t.Context(), "SELECT set_config('app.current_org',$1,true)", local.TenantID.String())
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(t.Context(), "SELECT count(*) FROM notion_pages").Scan(&pages))
	assert.Equal(t, 1, pages)
}
