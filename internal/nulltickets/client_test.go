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

		page := map[string]any{"tasks": []map[string]any{{"id": "t-" + got}}}
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
