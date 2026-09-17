package knowledge //nolint:testpackage // Tests the private transport seam without accepting arbitrary production API hosts.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hasMoreKey = "has_more"

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient("fixture-token")
	require.NoError(t, err)
	client.base, client.http, client.interval = server.URL, server.Client(), 0
	return client
}

func respond(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	assert.NoError(t, json.NewEncoder(writer).Encode(value))
}

func TestNotionQueryTraversesPagesAndRejectsRepeatedCursor(t *testing.T) {
	t.Parallel()
	source, first, second := uuid.NewString(), uuid.NewString(), uuid.NewString()
	calls := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "/data_sources/"+source+"/query", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "Bearer fixture-token", r.Header.Get("Authorization"))
		assert.Equal(t, notionVersion, r.Header.Get("Notion-Version"))
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		if calls == 1 {
			respond(t, w, map[string]any{"results": []map[string]string{{"id": first}}, hasMoreKey: true, "next_cursor": "cursor"})
			return
		}
		assert.Equal(t, "cursor", body["start_cursor"])
		respond(t, w, map[string]any{"results": []map[string]string{{"id": second}}, hasMoreKey: true, "next_cursor": "cursor"})
	})
	_, err := client.List(t.Context(), source)
	require.ErrorContains(t, err, "pagination did not advance")
	assert.Equal(t, 2, calls)
}

func TestNotionFetchCompletesRelationsAndMarksUnknownBody(t *testing.T) {
	t.Parallel()
	id, source, relationA, relationB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/markdown"):
			respond(t, w, map[string]any{"markdown": "<unknown block>", "unknown_block_ids": []string{uuid.NewString()}, "truncated": false})
		case strings.Contains(r.URL.Path, "/properties/"):
			assert.True(t, strings.HasSuffix(r.URL.Path, "/;rel"))
			if r.URL.Query().Get("start_cursor") == "" {
				respond(t, w, map[string]any{"results": []map[string]any{{"relation": map[string]string{"id": relationA}}}, hasMoreKey: true, "next_cursor": "next"})
			} else {
				respond(t, w, map[string]any{"results": []map[string]any{{"relation": map[string]string{"id": relationB}}}, hasMoreKey: false})
			}
		default:
			respond(t, w, map[string]any{"id": id, "created_time": time.Now().UTC(), "last_edited_time": time.Now().UTC(), "parent": map[string]string{"type": "data_source_id", "data_source_id": source}, "properties": map[string]any{"Projects": map[string]any{"id": "%3Brel", "type": "relation", "relation": []any{}, hasMoreKey: true}}})
		}
	})
	page, err := client.Fetch(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, page.ContentComplete)
	assert.False(t, *page.ContentComplete)
	assert.Contains(t, *page.PropertiesJson, relationA)
	assert.Contains(t, *page.PropertiesJson, relationB)
	assert.Equal(t, source, *page.DataSourceId)
}

func TestNotionUpdatePreflightsBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	id := uuid.NewString()
	writes := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			writes++
			respond(t, w, map[string]any{})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/markdown") {
			respond(t, w, map[string]any{"markdown": "human edit"})
			return
		}
		respond(t, w, map[string]any{"id": id, "parent": map[string]string{"type": "page_id", "page_id": uuid.NewString()}, "properties": map[string]any{}})
	})
	_, err := client.Update(t.Context(), id, Content{Markdown: "old"}, Content{Markdown: "local edit"})
	require.ErrorContains(t, err, "changed before delivery")
	assert.Zero(t, writes)
}

func TestNotionUpdateUsesGuardedMarkdownAndReadBack(t *testing.T) {
	t.Parallel()
	id, body, writes := uuid.NewString(), "old", 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			writes++
			assert.True(t, strings.HasSuffix(r.URL.Path, "/markdown"))
			var request map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			assert.Equal(t, "update_content", request["type"])
			operation, ok := request["update_content"].(map[string]any)
			if !assert.True(t, ok) {
				return
			}
			assert.Equal(t, false, operation["allow_deleting_content"])
			body = "new"
			respond(t, w, map[string]any{})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/markdown") {
			respond(t, w, map[string]any{"markdown": body})
			return
		}
		respond(t, w, map[string]any{"id": id, "parent": map[string]string{"type": "page_id", "page_id": uuid.NewString()}, "properties": map[string]any{}})
	})
	page, err := client.Update(t.Context(), id, Content{Markdown: "old"}, Content{Markdown: "new"})
	require.NoError(t, err)
	assert.Equal(t, "new", *page.Markdown)
	assert.Equal(t, 1, writes)
}

func TestNotionErrorsAreBoundedAndDoNotExposeProviderContent(t *testing.T) {
	t.Parallel()
	calls := 0
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "999")
		w.WriteHeader(http.StatusTooManyRequests)
		_, err := w.Write([]byte("sensitive-provider-body"))
		assert.NoError(t, err)
	})
	_, err := client.List(t.Context(), uuid.NewString())
	require.ErrorContains(t, err, "retry budget")
	assert.NotContains(t, err.Error(), "sensitive-provider-body")
	assert.NotContains(t, err.Error(), "fixture-token")
	assert.Equal(t, 1, calls)
}
