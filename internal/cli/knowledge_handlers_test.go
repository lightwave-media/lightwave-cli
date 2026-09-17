package cli_test

import (
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
