//nolint:testpackage // exercises unexported listener and pid helpers
package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeStatusStoppedAndDryRun(t *testing.T) {
	t.Parallel()

	rt := Runtime{HomeDir: t.TempDir(), Listen: "127.0.0.1:0"}
	st := rt.Status(t.Context())
	assert.False(t, st.Running)
	assert.Equal(t, "stopped", st.State)
	assert.Contains(t, st.Detail, "no pidfile")

	dry, err := rt.Start(t.Context(), Server{HomeDir: rt.HomeDir}, StartOpts{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, "dry-run", dry.State)
	assert.Contains(t, dry.Detail, "would start")
	_, statErr := os.Stat(rt.pidPath())
	require.Error(t, statErr)
}

func TestRuntimeReadLogsTail(t *testing.T) {
	t.Parallel()

	rt := Runtime{HomeDir: t.TempDir()}
	_, err := rt.ReadLogs(10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no log file")

	require.NoError(t, os.MkdirAll(filepath.Dir(rt.logPath()), 0o755))
	require.NoError(t, os.WriteFile(rt.logPath(), []byte("one\ntwo\nthree\nfour\n"), 0o644))

	got, err := rt.ReadLogs(2)
	require.NoError(t, err)
	assert.Equal(t, "three\nfour\n", got)
}

func TestRuntimeStalePidfile(t *testing.T) {
	t.Parallel()

	rt := Runtime{HomeDir: t.TempDir(), Listen: "127.0.0.1:1"}
	require.NoError(t, os.MkdirAll(filepath.Dir(rt.pidPath()), 0o755))
	require.NoError(t, os.WriteFile(rt.pidPath(), []byte("999999\n"), 0o644))

	st := rt.Status(t.Context())
	assert.False(t, st.Running)
	assert.Equal(t, 999999, st.Pid)
	assert.Contains(t, st.Detail, "stale pidfile")
}

func TestRuntimeStopTerminatesProcess(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(t.Context(), "sleep", "30")
	require.NoError(t, cmd.Start())

	waited := false
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if waited {
			return
		}
		_ = cmd.Process.Kill()
		<-done
	})

	rt := Runtime{HomeDir: t.TempDir()}
	require.NoError(t, os.MkdirAll(filepath.Dir(rt.pidPath()), 0o755))
	require.NoError(t, os.WriteFile(rt.pidPath(), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644))

	st, err := rt.Stop(t.Context(), false)
	require.NoError(t, err)
	assert.Contains(t, st.Detail, "stopped")

	select {
	case <-done:
		waited = true
	case <-time.After(2 * time.Second):
		t.Fatal("sleep did not exit after SIGTERM")
	}

	_, statErr := os.Stat(rt.pidPath())
	require.Error(t, statErr)
}

func TestServeListenerHealthAndJSONRPC(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	home := t.TempDir()
	writePersonaFixture(t, home, "v_test-engineer", "engineer")
	s := Server{HomeDir: home, Persona: "v_test-engineer"}
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.serveListener(ctx, ln)
	}()

	addr := ln.Addr().String()
	waitHTTPOk(t, "http://"+addr+"/health")

	healthReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/health", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(healthReq)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"ok":true`)
	assert.Contains(t, string(body), `"server":"lightwave"`)

	rpcBody := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	rpcReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+"/mcp", strings.NewReader(rpcBody))
	require.NoError(t, err)

	rpcReq.Header.Set("Content-Type", "application/json")

	rpcResp, err := http.DefaultClient.Do(rpcReq)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rpcResp.Body.Close() })
	require.Equal(t, http.StatusOK, rpcResp.StatusCode)

	var decoded map[string]any
	require.NoError(t, json.NewDecoder(rpcResp.Body).Decode(&decoded))
	encoded, err := json.Marshal(decoded)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"stamp_list"`)
	assert.Contains(t, string(encoded), `"queue_read"`)
	assert.NotContains(t, string(encoded), `"dispatch_agent"`)

	cancel()
	select {
	case serveErr := <-errCh:
		require.ErrorIs(t, serveErr, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not shut down")
	}
}

func waitHTTPOk(t *testing.T, url string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		if reqErr != nil {
			t.Fatalf("health request: %v", reqErr)
		}

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", url)
}
