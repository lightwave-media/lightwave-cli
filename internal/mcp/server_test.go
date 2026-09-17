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
	assert.True(t, contains(dev, "stamp_list"), "stamp library is advertised on every tier, got %v", dev)
	assert.True(t, contains(dev, "stamp_read"), "stamp library is advertised on every tier, got %v", dev)
	assert.True(t, contains(eng, "stamp_list"), "stamp library is advertised on every tier, got %v", eng)
	assert.True(t, contains(eng, "epic_write"), "engineer gets writes, got %v", eng)
	assert.False(t, contains(eng, "dispatch_agent"), "engineer must not get dispatch, got %v", eng)
	assert.True(t, contains(sing, "dispatch_agent"), "singular gets dispatch, got %v", sing)
	assert.True(t, contains(sing, "task_write"), "singular gets writes, got %v", sing)
}

func TestToolsForUnresolvedIdentityDeniesEverything(t *testing.T) {
	t.Parallel()
	none := toolsFor(TierNone)
	assert.Empty(t, none, "an unresolved identity must get zero tools, including the read-only stamp library")
	assert.False(t, toolAllowed(TierNone, "stamp_list"), "even a read-only tool must be denied with no resolved identity")
}

func TestResolveTierFailsClosed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".lightwave", "config", "agents")
	require.NoError(t, os.MkdirAll(agentDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "v_cto.yaml"), []byte("tier: singular\nname: v_cto\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(agentDir, "v_bad-tier.yaml"), []byte("tier: made_up_tier\nname: v_bad-tier\n"), 0o644))

	cases := []struct {
		name    string
		home    string
		persona string
		want    Tier
	}{
		{"no persona given at all", dir, "", TierNone},
		{"a real, resolvable persona", dir, "v_cto", TierSingular},
		{"a persona name with no matching file", dir, "does-not-exist", TierNone},
		{"a persona file whose declared tier is not a known value", dir, "v_bad-tier", TierNone},
		{"a persona name matched against an unreadable home dir", filepath.Join(dir, "no-such-dir"), "v_cto", TierNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ResolveTier(tc.home, tc.persona))
		})
	}
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

func TestResolveTierRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		persona string
		body    string
		symlink bool
	}{
		{"parent traversal", "../v_outside", "name: ../v_outside\ntier: singular\n", false},
		{"mismatched name", "v_test", "name: v_other\ntier: singular\n", false},
		{"missing name", "v_test", "tier: singular\n", false},
		{"external symlink", "v_test", "name: v_test\ntier: singular\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			dir := filepath.Join(home, ".lightwave", "config", "agents")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			path := filepath.Join(dir, tc.persona+".yaml")
			if tc.symlink {
				target := filepath.Join(t.TempDir(), "persona.yaml")
				require.NoError(t, os.WriteFile(target, []byte(tc.body), 0o600))
				require.NoError(t, os.Symlink(target, path))
			} else {
				require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))
			}
			assert.Equal(t, TierNone, ResolveTier(home, tc.persona))
		})
	}
}

func TestServeRevokesPersonaWithoutRestart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writePersonaFixture(t, home, "v_test", "engineer")
	in, requests := io.Pipe()
	responses, out := io.Pipe()
	t.Cleanup(func() {
		_ = requests.Close()
		_ = in.Close()
		_ = responses.Close()
		_ = out.Close()
	})
	finished := make(chan error, 1)
	go func() {
		finished <- Serve(t.Context(), in, out, Server{
			HomeDir: home,
			Persona: "v_test",
			Connect: func(_ context.Context) (*pgxpool.Pool, error) {
				t.Error("revoked write reached the database")
				return nil, db.ErrDBUnavailable
			},
		})
	}()
	encoder := json.NewEncoder(requests)
	decoder := json.NewDecoder(responses)
	list := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
	require.NoError(t, encoder.Encode(list))
	var before json.RawMessage
	require.NoError(t, decoder.Decode(&before))
	require.Contains(t, string(before), "epic_write", "positive control: role initially permits writes")
	require.NoError(t, os.Remove(filepath.Join(home, ".lightwave", "config", "agents", "v_test.yaml")))
	require.NoError(t, encoder.Encode(list))
	var after json.RawMessage
	require.NoError(t, decoder.Decode(&after))
	assert.NotContains(t, string(after), "epic_write")
	require.NoError(t, encoder.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "epic_write", "arguments": map[string]any{}},
	}))
	var denied json.RawMessage
	require.NoError(t, decoder.Decode(&denied))
	assert.Contains(t, string(denied), "not advertised")
	require.NoError(t, requests.Close())
	require.NoError(t, <-finished)
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
	home := t.TempDir()
	writePersonaFixture(t, home, "v_test-engineer", "engineer")

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
	require.NoError(t, Serve(t.Context(), &in, &out, Server{HomeDir: home, Persona: "v_test-engineer"}))
	frames := splitFrames(out.Bytes())
	require.Len(t, frames, 2, "body=%s", out.String())
	assert.Contains(t, string(frames[0]), `"protocolVersion"`)
	assert.Contains(t, string(frames[1]), `"queue_read"`)
	assert.Contains(t, string(frames[1]), `"stamp_list"`)
	assert.NotContains(t, string(frames[1]), `"dispatch_agent"`, "engineer tier must not advertise dispatch_agent")
}

// writePersonaFixture writes a minimal resolvable persona YAML under home's
// .lightwave/config/agents/, for tests that exercise the real Serve/tools-list
// pipeline and need identity to actually resolve — an empty Server.Persona
// now denies everything (ResolveTier fails closed), so a test verifying what
// a REAL tier sees must give it one.
func writePersonaFixture(t *testing.T, home, name, tier string) {
	t.Helper()
	dir := filepath.Join(home, ".lightwave", "config", "agents")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, name+".yaml"),
		[]byte("tier: "+tier+"\nname: "+name+"\n"),
		0o644,
	))
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
