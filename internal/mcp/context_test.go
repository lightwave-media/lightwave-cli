//nolint:testpackage // exercises the MCP dispatch and runtime observation helpers
package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStandardStdioHandshakeAndPersona(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writePersonaFixture(t, home, "engineer", "engineer")
	for _, persona := range []string{"engineer", "missing"} {
		t.Run(persona, func(t *testing.T) {
			t.Parallel()
			in := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}\n")
			var out bytes.Buffer
			require.NoError(t, Serve(t.Context(), in, &out, Server{HomeDir: home, Persona: persona}))
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			require.Len(t, lines, 2)
			for _, line := range lines {
				assert.True(t, json.Valid([]byte(line)))
			}
			assert.Contains(t, lines[0], "instructions")
			if persona == "engineer" {
				assert.Contains(t, lines[1], "context_get")
				assert.NotContains(t, lines[1], "dispatch_agent")
			} else {
				assert.Contains(t, lines[1], `"tools":[]`)
			}
		})
	}
}

func TestContextUsesCallerWorkspaceAndExistingObservations(t *testing.T) {
	t.Parallel()
	home, cwd := t.TempDir(), t.TempDir()
	root := filepath.Join(home, ".lightwave")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "observability"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "observability", "sessions.jsonl"), []byte(
		`{"session_id":"other-app","harness":"opencode","action_type":"session_start","ts":"2026-09-17T00:00:00Z"}`+"\n"+
			`{"session_id":"closed","action_type":"session_end","ts":"2026-09-17T01:00:00Z"}`+"\n"), 0o600))
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/instances", r.URL.Path)
		_, _ = w.Write([]byte(`{"instances":{"nullclaw":{"worker":{"status":"running","pid":123,"token":"private"}}}}`))
	}))
	t.Cleanup(hub.Close)
	s := Server{HomeDir: home, Persona: "engineer", Base: hub.URL, Client: hub.Client()}
	result := s.workspaceContext(t.Context(), map[string]string{"cwd": cwd, "task_id": "task-123", "harness": "codex"})
	require.False(t, result.IsError)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(contentText(result)), &payload))
	assert.Equal(t, cwd, payload["workspace"].(map[string]any)["cwd"])
	assert.Equal(t, "task-123", payload["job"].(map[string]any)["task_id"])
	assert.Contains(t, contentText(result), "opencode")
	assert.Contains(t, contentText(result), "unconfirmed")
	assert.Contains(t, contentText(result), "end_observed")
	assert.Contains(t, contentText(result), "worker")
	assert.NotContains(t, contentText(result), "private")
	assert.Contains(t, payload["sources"].(map[string]any)["agents"], "unavailable")
	assert.True(t, s.workspaceContext(t.Context(), map[string]string{"cwd": "relative"}).IsError)
}

func TestAgentObservationsDoNotExposeArguments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.json"), []byte(`{"id":"a","shell":"claude","status":"running","pid":0,"task_id":"job","shell_args":["private prompt"]}`), 0o600))
	rows, status := agentObservations(dir)
	require.Len(t, rows, 1)
	assert.Equal(t, "ok", status)
	assert.Equal(t, false, rows[0]["process_alive"])
	assert.NotContains(t, rows[0], "shell_args")
}
