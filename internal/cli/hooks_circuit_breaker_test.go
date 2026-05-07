package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withTempState points pushCircuitBreakerStatePath() at a tmp dir for the
// duration of the test, optionally seeding the state file.
func withTempState(t *testing.T, state map[string]circuitBreakerEntry) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	path := filepath.Join(dir, "lightwave", "push-circuit-breaker.json")

	if state != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestCircuitBreaker_NoStateFile_ReturnsNil(t *testing.T) {
	withTempState(t, nil)
	flags := map[string]any{"branch": "any"}
	if err := hooksCircuitBreakerCheckHandler(context.Background(), nil, flags); err != nil {
		t.Errorf("expected nil for missing state file, got: %v", err)
	}
}

func TestCircuitBreaker_BelowThreshold_ReturnsNil(t *testing.T) {
	withTempState(t, map[string]circuitBreakerEntry{
		"feat/x": {ConsecutiveFailures: 2, LastError: "test fail"},
	})
	flags := map[string]any{"branch": "feat/x"}
	if err := hooksCircuitBreakerCheckHandler(context.Background(), nil, flags); err != nil {
		t.Errorf("expected nil below threshold, got: %v", err)
	}
}

func TestCircuitBreaker_BranchNotInState_ReturnsNil(t *testing.T) {
	withTempState(t, map[string]circuitBreakerEntry{
		"other-branch": {ConsecutiveFailures: 3, LastError: "test fail"},
	})
	flags := map[string]any{"branch": "feat/x"}
	if err := hooksCircuitBreakerCheckHandler(context.Background(), nil, flags); err != nil {
		t.Errorf("expected nil for branch not in state, got: %v", err)
	}
}

func TestCircuitBreaker_MissingBranchFlag_Errors(t *testing.T) {
	withTempState(t, nil)
	flags := map[string]any{}
	err := hooksCircuitBreakerCheckHandler(context.Background(), nil, flags)
	if err == nil {
		t.Fatal("expected error when --branch is absent")
	}
}

// TestCircuitBreaker_AtThreshold_ExitsOne does not actually call the
// handler (which calls os.Exit(1)). Exit calls would tear down the test
// process. Instead we directly verify the readCircuitBreakerState +
// threshold logic that the handler builds on.
func TestCircuitBreaker_AtThreshold_StateMatches(t *testing.T) {
	path := withTempState(t, map[string]circuitBreakerEntry{
		"feat/x": {ConsecutiveFailures: 3, LastError: "ruff failed"},
	})
	state, err := readCircuitBreakerState(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := state["feat/x"]
	if !ok {
		t.Fatal("entry missing")
	}
	if entry.ConsecutiveFailures < PushCircuitBreakerThreshold {
		t.Errorf("expected entry to meet threshold, got %d (threshold %d)", entry.ConsecutiveFailures, PushCircuitBreakerThreshold)
	}
	if entry.LastError != "ruff failed" {
		t.Errorf("lastError did not round-trip, got %q", entry.LastError)
	}
}

func TestReadCircuitBreakerState_MalformedJSON_Errors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	path := filepath.Join(dir, "lightwave", "push-circuit-breaker.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCircuitBreakerState(path); err == nil {
		t.Fatal("expected parse error for malformed JSON")
	}
}

func TestReadCircuitBreakerState_EmptyFile_ReturnsEmptyMap(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	path := filepath.Join(dir, "lightwave", "push-circuit-breaker.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := readCircuitBreakerState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(state) != 0 {
		t.Errorf("expected empty map, got %v", state)
	}
}
