package nulltickets_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/nulltickets"
)

func TestListTasksSendsTheCursorIntact(t *testing.T) {
	t.Parallel()

	// A base64 cursor carries + / = , each of which means something else in a
	// raw query string. A server that gets it mangled returns the wrong page.
	const cursor = "a+b/c=="

	var seen []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query().Get("cursor")
		seen = append(seen, got)

		// The real list shape: {items, next_cursor}, with a null cursor on the
		// last page (openapi PaginatedTasks).
		page := map[string]any{"items": []map[string]any{{"id": "t-" + got}}, "next_cursor": nil}
		if got == "" {
			page["next_cursor"] = cursor
		}

		assert.NoError(t, json.NewEncoder(w).Encode(page))
	}))
	t.Cleanup(server.Close)

	tasks, err := nulltickets.New(server.URL, "token").ListTasks(t.Context(), "pipe-1", "in_review")
	require.NoError(t, err)

	assert.Equal(t, []string{"", cursor}, seen, "the second request must carry the cursor exactly as issued")
	assert.Len(t, tasks, 2)
}

func TestListTasksRefusesANon200Page(t *testing.T) {
	t.Parallel()

	// A 401 or 500 is not an empty stage: reading it as one would reconcile
	// nothing and report success.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	tasks, err := nulltickets.New(server.URL, "bad-token").ListTasks(t.Context(), "pipe-1", "in_review")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Empty(t, tasks)
}
