//nolint:testpackage // tests unexported toolsFor/callTool/frame helpers
//nolint:testpackage // tests share unexported helpers with the server loop
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolsForTierFiltering(t *testing.T) {
	t.Parallel()
	dev := names(toolsFor(TierDeveloper))
	eng := names(toolsFor(TierEngineer))
	sing := names(toolsFor(TierSingular))
	assert.False(t, contains(dev, "epic_write"), "developer must be read-only, got %v", dev)
	assert.False(t, contains(dev, "dispatch_agent"), "developer must be read-only, got %v", dev)
	assert.True(t, contains(eng, "epic_write"), "engineer gets writes, got %v", eng)
	assert.False(t, contains(eng, "dispatch_agent"), "engineer must not get dispatch, got %v", eng)
	assert.True(t, contains(sing, "dispatch_agent"), "singular gets dispatch, got %v", sing)
	assert.True(t, contains(sing, "task_write"), "singular gets writes, got %v", sing)
}

func TestResolveTierDefaultAndFile(t *testing.T) {
	t.Parallel()
	assert.Equal(t, DefaultTier, ResolveTier("", ""))

	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".lightwave", "config", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "v_cto.yaml"), []byte("tier: singular\nname: v_cto\n"), 0o644))
	assert.Equal(t, TierSingular, ResolveTier(dir, "v_cto"))
	assert.Equal(t, DefaultTier, ResolveTier(dir, "missing"))
}

func TestDispatchAgentPlaneDown(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	s := Server{Client: srv.Client(), Base: srv.URL, HomeDir: t.TempDir()}
	out := s.callTool(t.Context(), TierSingular, mustJSON(t, callParams{
		Name:      "dispatch_agent",
		Arguments: mustJSON(t, map[string]string{"name": "v_cli-developer", "message": "hi"}),
	}))
	require.True(t, out.IsError, "expected error")
	assert.Contains(t, out.Content[0]["text"], "runtime_plane_down")
}

func TestDispatchAgentSuccess(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/instances/nullclaw/v_cli-developer/agent", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		assert.Contains(t, string(body), `"message":"hi"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)
	s := Server{Client: hs.Client(), Base: hs.URL, HomeDir: t.TempDir()}
	out := s.callTool(t.Context(), TierSingular, mustJSON(t, callParams{
		Name:      "dispatch_agent",
		Arguments: mustJSON(t, map[string]string{"name": "v_cli-developer", "message": "hi"}),
	}))
	require.False(t, out.IsError, "unexpected error: %s", contentText(out))
	assert.Contains(t, contentText(out), `"ok":true`)
}

func TestQueueReadPlaneDown(t *testing.T) {
	t.Parallel()
	s := Server{
		HomeDir: t.TempDir(),
		Connect: func(_ context.Context) (*pgxpool.Pool, error) {
			return nil, fmt.Errorf("%w: down", db.ErrDBUnavailable)
		},
	}
	out := s.callTool(t.Context(), TierEngineer, mustJSON(t, callParams{
		Name:      "queue_read",
		Arguments: []byte(`{}`),
	}))
	assert.True(t, out.IsError)
	assert.Contains(t, contentText(out), "runtime_plane_down")
}

func TestCallToolDeniedForTier(t *testing.T) {
	t.Parallel()
	s := Server{HomeDir: t.TempDir()}
	out := s.callTool(t.Context(), TierEngineer, mustJSON(t, callParams{
		Name:      "dispatch_agent",
		Arguments: mustJSON(t, map[string]string{"name": "v_cli-developer", "message": "hi"}),
	}))
	require.True(t, out.IsError)
	assert.Contains(t, contentText(out), "not advertised")
}

func TestServeInitializeAndToolList(t *testing.T) {
	t.Parallel()
	var in bytes.Buffer
	writeTestFrame(t, &in, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})
	writeTestFrame(t, &in, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	var out bytes.Buffer
	require.NoError(t, Serve(t.Context(), &in, &out, Server{HomeDir: t.TempDir()}))
	frames := splitFrames(out.Bytes())
	require.Len(t, frames, 2, "body=%s", out.String())
	assert.Contains(t, string(frames[0]), `"protocolVersion"`)
	assert.Contains(t, string(frames[1]), `"queue_read"`)
	assert.NotContains(t, string(frames[1]), `"dispatch_agent"`, "default engineer tier must not advertise dispatch_agent")
}

func names(tools []toolDef) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	return out
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func contentText(out toolCallResult) string {
	if len(out.Content) == 0 {
		return ""
	}
	return out.Content[0]["text"]
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func writeTestFrame(t *testing.T, w io.Writer, v any) {
	t.Helper()
	body, err := json.Marshal(v)
	require.NoError(t, err)
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

func splitFrames(raw []byte) [][]byte {
	var frames [][]byte
	rest := raw
	for len(rest) > 0 {
		idx := bytes.Index(rest, []byte("\r\n\r\n"))
		if idx < 0 {
			break
		}
		header := string(rest[:idx])
		var n int
		fmt.Sscanf(strings.TrimPrefix(strings.ToLower(header), "content-length: "), "%d", &n)
		start := idx + 4
		frames = append(frames, rest[start:start+n])
		rest = rest[start+n:]
	}
	return frames
}
