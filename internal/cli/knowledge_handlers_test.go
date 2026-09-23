package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:paralleltest // RunHandler captures stdout and Setenv changes the print root.
func TestKnowledgeStatusAndReindexDryRunAreOffline(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("LW_HOME_PRINT", root)
	output, err := testutil.RunHandler(t, "knowledge.status", nil, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, output)
	output, err = testutil.RunHandler(t, "knowledge.reindex", nil, map[string]any{dryRunFlag: true})
	require.NoError(t, err)
	assert.Contains(t, output, `"dry_run": true`)
	assert.NoDirExists(t, filepath.Join(root, "index"))
}

//nolint:paralleltest // RunHandler captures stdout.
func TestKnowledgeMigrationReviewDoesNotApplyDDL(t *testing.T) {
	output, err := testutil.RunHandler(t, "knowledge.migrate", nil, map[string]any{dryRunFlag: true})
	require.NoError(t, err)
	assert.Contains(t, output, "CREATE TABLE IF NOT EXISTS notion_pages")
	assert.Contains(t, output, "FORCE ROW LEVEL SECURITY")
	_, err = testutil.RunHandler(t, "knowledge.migrate", nil, nil)
	require.ErrorContains(t, err, "--yes")
}

const (
	bindDatabaseID = "21539364-b3be-802a-832d-de8d9cefcd9a"
	bindOtherID    = "b8701544-1206-407e-934e-07485fe2f639"
)

// writeBindFixture lays down one notion_database instance and two property
// maps: one that claims the instance and one that claims a different database.
func writeBindFixture(t *testing.T, root string) string {
	t.Helper()
	dbDir := filepath.Join(root, "specs", "notion_database")
	mapDir := filepath.Join(root, "specs", "notion_property_map")
	require.NoError(t, os.MkdirAll(dbDir, 0o700))
	require.NoError(t, os.MkdirAll(mapDir, 0o700))

	instance := filepath.Join(dbDir, bindDatabaseID+".yaml")
	require.NoError(t, os.WriteFile(instance, []byte(`id: 2051e301-7db8-5d09-95f5-bcf257bfc15c
tenant_id: 0e19c7f8-5134-40a9-813b-3ae384615b57
notion_id: `+bindDatabaseID+`
data_source_id: 21539364-b3be-80b3-b554-000bd9c80693
title: Sprints
url: https://www.notion.so/21539364b3be802a832dde8d9cefcd9a
sync_enabled: false
sync_status: pending
direction: inbound
created_at: '2026-09-17T23:19:10.433701+00:00'
updated_at: '2026-09-17T23:19:10.433701+00:00'
`), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(mapDir, "agile-sprints.yaml"), []byte(`slug: agile-sprints
database_notion_id: `+bindDatabaseID+`
mappings:
  - notion_property: name
    notion_type: title
    local_field: name
    required: true
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(mapDir, "tasks.yaml"), []byte(`slug: tasks
database_notion_id: `+bindOtherID+`
mappings:
  - notion_property: title
    notion_type: title
    local_field: title
    required: true
`), 0o600))

	return instance
}

//nolint:paralleltest // RunHandler captures stdout and Setenv changes the print root.
func TestKnowledgeBindWritesPropertyMapRefAndTitle(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("LW_HOME_PRINT", root)
	instance := writeBindFixture(t, root)

	output, err := testutil.RunHandler(t, "knowledge.bind", []string{bindDatabaseID},
		map[string]any{"property-map": "agile-sprints", "title": "[DB] agile_sprints", jsonFlag: true})
	require.NoError(t, err)
	assert.Contains(t, output, `"property_map_ref": "agile-sprints"`)

	body, err := os.ReadFile(instance)
	require.NoError(t, err)
	assert.Contains(t, string(body), "property_map_ref: agile-sprints")
	assert.Contains(t, string(body), "title: '[DB] agile_sprints'")
	// Everything the instance already carried survives the rewrite.
	assert.Contains(t, string(body), "data_source_id")
	assert.Contains(t, string(body), "direction: inbound")
}

//nolint:paralleltest // RunHandler captures stdout and Setenv changes the print root.
func TestKnowledgeBindRejectsWhatSyncWouldReject(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv("LW_HOME_PRINT", root)
	instance := writeBindFixture(t, root)
	before, err := os.ReadFile(instance)
	require.NoError(t, err)

	// A map that claims a different database must not be bound.
	_, err = testutil.RunHandler(t, "knowledge.bind", []string{bindDatabaseID}, map[string]any{"property-map": "tasks"})
	require.ErrorContains(t, err, "different database")

	// A map that does not exist must not be bound.
	_, err = testutil.RunHandler(t, "knowledge.bind", []string{bindDatabaseID}, map[string]any{"property-map": "missing"})
	require.ErrorContains(t, err, `property map "missing"`)

	// An instance that does not exist cannot be bound.
	_, err = testutil.RunHandler(t, "knowledge.bind", []string{bindOtherID}, map[string]any{"property-map": "tasks"})
	require.Error(t, err)

	// Nothing to do is an error, not a silent no-op rewrite.
	_, err = testutil.RunHandler(t, "knowledge.bind", []string{bindDatabaseID}, nil)
	require.ErrorContains(t, err, "nothing to bind")

	after, err := os.ReadFile(instance)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a rejected bind must leave the instance untouched")
}
