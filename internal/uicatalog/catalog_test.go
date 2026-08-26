package uicatalog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/uicatalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func TestList_WalksEveryCurrentPrint(t *testing.T) {
	t.Parallel()
	ui := t.TempDir()
	writeFile(t, ui, "src/components/base/buttons/button.tsx", "// vendored: untitled-ui\nexport const Button = () => null\n")
	writeFile(t, ui, "src/components/base/buttons/button.contract.yaml", "covers:\n  - primary action\ndoes_not_cover:\n  - split button\n")
	writeFile(t, ui, "src/components/base/buttons/button.test.tsx", "test placeholder\n")
	writeFile(t, ui, "src/components/internal/decorators.tsx", "not a component\n")
	writeFile(t, ui, "src/components/application/tables/data-table.tsx", "export const DataTable = () => null\n")

	entries, err := uicatalog.List(ui)
	require.NoError(t, err)
	require.Len(t, entries, 2, "census must walk every current component print, not a named instance")

	byName := map[string]uicatalog.Entry{}
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	button := byName["Button"]
	assert.Equal(t, "base", button.Category)
	assert.Equal(t, "base/buttons/button.tsx", button.Path)
	assert.Contains(t, button.Provenance, "vendored: untitled-ui")
	assert.Equal(t, []string{"primary action"}, button.Covers)
	assert.Equal(t, []string{"split button"}, button.DoesNotCover)

	table := byName["DataTable"]
	assert.Equal(t, "application", table.Category)
	assert.Equal(t, "unregistered", table.Provenance)
	assert.Empty(t, table.Covers)
}

func TestSearch_MatchesNeedPhrasesNotFolderNamesOnly(t *testing.T) {
	t.Parallel()
	entries := []uicatalog.Entry{
		{Category: "base", Name: "Button", Path: "base/buttons/button.tsx", Covers: []string{"primary action", "labeled button"}},
		{Category: "application", Name: "DataTable", Path: "application/tables/data-table.tsx", Covers: []string{"tabular data"}},
	}

	hits := uicatalog.Search(entries, "primary action")
	require.Len(t, hits, 1)
	assert.Equal(t, "Button", hits[0].Name)

	byFolder := uicatalog.Search(entries, "buttons")
	require.Len(t, byFolder, 1)
	assert.Equal(t, "Button", byFolder[0].Name, "path remains searchable, but need phrases must also hit")

	assert.Empty(t, uicatalog.Search(entries, "split button"))
}

func TestDuplicate_FindsUpstreamClone(t *testing.T) {
	t.Parallel()
	entries := []uicatalog.Entry{
		{Name: "Button", Path: "base/buttons/button.tsx"},
	}
	got := uicatalog.Duplicate(entries, "button")
	require.NotNil(t, got)
	assert.Equal(t, "Button", got.Name)
	assert.Nil(t, uicatalog.Duplicate(entries, "Toast"))
}

func TestList_UnreachableCatalog(t *testing.T) {
	t.Parallel()
	_, err := uicatalog.List(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}
