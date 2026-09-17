package knowledge //nolint:testpackage // Exercise private file recovery and HTTP transport seams.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryRemote struct {
	page           Page
	afterWrite     func()
	fetches        int
	writes         int
	failAfterWrite bool
}

func (remote *memoryRemote) List(context.Context, string) ([]RemotePage, error) {
	return []RemotePage{{ID: remote.page.NotionId, Edited: remote.page.LastEditedAt}}, nil
}
func (remote *memoryRemote) Fetch(context.Context, string) (Page, error) {
	remote.fetches++
	return remote.page, nil
}
func (remote *memoryRemote) Update(_ context.Context, _ string, _ Content, target Content) (Page, error) {
	remote.writes++
	if err := applyContent(&remote.page, target); err != nil {
		return Page{}, err
	}
	if remote.afterWrite != nil {
		remote.afterWrite()
	}
	if remote.failAfterWrite {
		remote.failAfterWrite = false
		return Page{}, errors.New("connection lost after provider accepted write")
	}
	return remote.page, nil
}

func engineFixture(t *testing.T) (Engine, *memoryRemote) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	id := uuid.NewString()
	tenant := uuid.New()
	source := uuid.NewString()
	now := time.Now().UTC()
	remote := &memoryRemote{page: Page{ID: uuid.MustParse(id), TenantID: tenant, NotionId: id, Title: "Example", ParentType: "data_source_id", Url: "https://www.notion.so/" + id, CreatedAt: now, UpdatedAt: now, LastEditedAt: now, Markdown: ptr("original body"), ContentComplete: ptr(true), PropertiesJson: ptr(`{}`), DataSourceId: ptr(source), SyncStatus: "synced"}}
	database := Database{ID: uuid.New(), TenantID: tenant, NotionId: uuid.NewString(), Title: "Fixture", Url: "https://www.notion.so", DataSourceId: ptr(source), Direction: ptr("bidirectional"), SyncEnabled: true, SyncStatus: "synced", CreatedAt: now, UpdatedAt: now}
	_, err = writePrint(filepath.Join(root, "specs", "notion_database", database.NotionId+".yaml"), database)
	require.NoError(t, err)
	return Engine{Files: Files{Root: root}, Remote: remote}, remote
}

func TestImportIsRepeatableAndDoesNotWriteToNotion(t *testing.T) {
	t.Parallel()
	engine, remote := engineFixture(t)
	first, err := engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	require.Len(t, first.Changes, 1)
	assert.Equal(t, "import", first.Changes[0].Action)
	second, err := engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	assert.Equal(t, "unchanged", second.Changes[0].Action)
	assert.Zero(t, remote.writes)
	bindings, err := engine.Files.Bindings()
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	assert.NotEmpty(t, bindings[0].LastWrittenSha256)
}

func TestUncertainAcceptedWriteRecoversWithoutReplay(t *testing.T) {
	t.Parallel()
	engine, remote := engineFixture(t)
	_, err := engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	local, err := engine.Files.LoadPage(remote.page.NotionId)
	require.NoError(t, err)
	local.Markdown = ptr("local result")
	_, err = engine.Files.SavePage(local)
	require.NoError(t, err)
	remote.failAfterWrite = true
	_, err = engine.Run(t.Context(), Options{})
	require.Error(t, err)
	binding, err := engine.Files.LoadBinding(local.NotionId)
	require.NoError(t, err)
	assert.Equal(t, "pending", binding.SyncStatus)
	assert.NotEmpty(t, binding.PendingContentJson)
	_, err = engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	assert.Equal(t, 1, remote.writes)
	binding, err = engine.Files.LoadBinding(local.NotionId)
	require.NoError(t, err)
	assert.Equal(t, "synced", binding.SyncStatus)
	assert.Empty(t, binding.PendingContentJson)
}

func TestConcurrentHumanAndLocalEditsAreRetained(t *testing.T) {
	t.Parallel()
	engine, remote := engineFixture(t)
	_, err := engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	local, err := engine.Files.LoadPage(remote.page.NotionId)
	require.NoError(t, err)
	local.Markdown = ptr("local result")
	_, err = engine.Files.SavePage(local)
	require.NoError(t, err)
	remote.page.Markdown = ptr("human edit")
	remote.page.LastEditedAt = time.Now().UTC()
	report, err := engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	assert.Equal(t, "drift", report.Changes[0].Action)
	assert.Zero(t, remote.writes)
	retained, err := engine.Files.LoadPage(local.NotionId)
	require.NoError(t, err)
	assert.Equal(t, "local result", *retained.Markdown)
	binding, err := engine.Files.LoadBinding(local.NotionId)
	require.NoError(t, err)
	assert.Contains(t, *binding.LastError, "human edit")
}

func TestDryRunDoesNotCreatePagesBindingsOrLocks(t *testing.T) {
	t.Parallel()
	engine, remote := engineFixture(t)
	report, err := engine.Run(t.Context(), Options{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, "import", report.Changes[0].Action)
	assert.NoFileExists(t, filepath.Join(engine.Files.Root, "specs", "notion_page", remote.page.NotionId+".yaml"))
	assert.NoFileExists(t, filepath.Join(engine.Files.Root, "index", "notion-sync.lock"))
	bindings, err := engine.Files.Bindings()
	require.NoError(t, err)
	assert.Empty(t, bindings)
}

func TestInterruptedFirstImportRetainsIdentity(t *testing.T) {
	t.Parallel()
	engine, remote := engineFixture(t)
	binding := newBinding(remote.page, "bidirectional")
	binding.SyncStatus = StatusPending
	require.NoError(t, engine.Files.SaveBinding(binding))
	_, err := engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	page, err := engine.Files.LoadPage(remote.page.NotionId)
	require.NoError(t, err)
	assert.Equal(t, remote.page.NotionId, page.NotionId)
	assert.Zero(t, remote.writes)
}

func TestBindingAuthorityAndFieldOverrideAreApplied(t *testing.T) {
	t.Parallel()
	policy, err := bindingPolicy("local", map[string]string{"markdown": "external"}, Content{Properties: map[string]json.RawMessage{"Evidence": json.RawMessage(`1`)}})
	require.NoError(t, err)
	assert.Equal(t, "local", policy["Evidence"])
	assert.Equal(t, "external", policy["markdown"])
	_, err = bindingPolicy("", nil)
	require.Error(t, err)
}

func TestArchiveNeverDiscardsUnpublishedLocalEdit(t *testing.T) {
	t.Parallel()
	engine, remote := engineFixture(t)
	_, err := engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	page, err := engine.Files.LoadPage(remote.page.NotionId)
	require.NoError(t, err)
	page.Markdown = ptr("unpublished result")
	_, err = engine.Files.SavePage(page)
	require.NoError(t, err)
	remote.page.Archived = ptr(true)
	_, err = engine.Run(t.Context(), Options{})
	require.ErrorContains(t, err, "archived page")
	assert.Zero(t, remote.writes)
	retained, err := engine.Files.LoadPage(page.NotionId)
	require.NoError(t, err)
	assert.Equal(t, "unpublished result", *retained.Markdown)
}

func TestLocalEditDuringDeliveryDoesNotRevertIndependentNotionEdit(t *testing.T) {
	t.Parallel()
	engine, remote := engineFixture(t)
	remote.page.PropertiesJson = ptr(`{"Priority":{"type":"number","number":1}}`)
	_, err := engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	local, err := engine.Files.LoadPage(remote.page.NotionId)
	require.NoError(t, err)
	local.Markdown = ptr("first result")
	_, err = engine.Files.SavePage(local)
	require.NoError(t, err)
	remote.page.PropertiesJson = ptr(`{"Priority":{"type":"number","number":2}}`)
	remote.page.LastEditedAt = time.Now().UTC()
	remote.afterWrite = func() {
		latest, loadErr := engine.Files.LoadPage(local.NotionId)
		require.NoError(t, loadErr)
		latest.Markdown = ptr("second result")
		_, writeErr := engine.Files.SavePage(latest)
		require.NoError(t, writeErr)
	}
	_, err = engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	remote.afterWrite = nil
	_, err = engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	assert.Equal(t, "second result", *remote.page.Markdown)
	assert.JSONEq(t, `{"Priority":{"type":"number","number":2}}`, *remote.page.PropertiesJson)
}

func TestUnchangedPagesAreSkippedUnlessFullRefresh(t *testing.T) {
	t.Parallel()
	engine, remote := engineFixture(t)
	_, err := engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	first := remote.fetches
	_, err = engine.Run(t.Context(), Options{})
	require.NoError(t, err)
	assert.Equal(t, first, remote.fetches)
	_, err = engine.Run(t.Context(), Options{Full: true})
	require.NoError(t, err)
	assert.Greater(t, remote.fetches, first)
}
