package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fatih/color"
)

// PushCircuitBreakerThreshold is the consecutive-failure count above which
// the circuit breaker fires. Documented in lightwave-cli/CLAUDE.md and
// must stay in sync with whatever increments the counter in pre-push hooks.
const PushCircuitBreakerThreshold = 3

// circuitBreakerEntry is the per-branch row in the state file.
type circuitBreakerEntry struct {
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	LastError           string `json:"lastError"`
	LastAttempt         string `json:"lastAttempt"`
}

func init() {
	RegisterHandler("hooks.circuit-breaker.check", hooksCircuitBreakerCheckHandler)
}

// hooksCircuitBreakerCheckHandler reads the push-circuit-breaker state
// file and exits non-zero when the configured branch has hit the failure
// threshold. Designed to be the one-line consult that every repo's
// pre-push hook calls before attempting a push.
//
// Exit codes:
//
//	0 — under threshold (or no state recorded)
//	1 — at/over threshold (push should be blocked)
//	2 — state file unreadable / malformed (treated as a tool error,
//	    not a circuit-breaker trip — pre-push hooks should fail-open
//	    rather than wedge the repo on a parse error)
func hooksCircuitBreakerCheckHandler(_ context.Context, _ []string, flags map[string]any) error {
	branch, _ := flags["branch"].(string)
	if branch == "" {
		return fmt.Errorf("--branch is required")
	}

	path := pushCircuitBreakerStatePath()
	state, err := readCircuitBreakerState(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, color.YellowString("warning: %v", err))
		os.Exit(2)
	}

	entry, ok := state[branch]
	if !ok || entry.ConsecutiveFailures < PushCircuitBreakerThreshold {
		return nil
	}

	fmt.Fprintln(os.Stderr, color.RedString("Push circuit breaker tripped for branch %q.", branch))
	fmt.Fprintf(os.Stderr, "  consecutive failures: %d (threshold: %d)\n", entry.ConsecutiveFailures, PushCircuitBreakerThreshold)
	if entry.LastAttempt != "" {
		fmt.Fprintf(os.Stderr, "  last attempt:         %s\n", formatAttempt(entry.LastAttempt))
	}
	if entry.LastError != "" {
		fmt.Fprintf(os.Stderr, "  last error:           %s\n", entry.LastError)
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Escalate to your manager with the repeating error before pushing again.")
	fmt.Fprintf(os.Stderr, "To unblock manually, delete the %q entry from %s.\n", branch, path)

	os.Exit(1)
	return nil
}

// pushCircuitBreakerStatePath is split out so tests can override via HOME.
func pushCircuitBreakerStatePath() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "lightwave", "push-circuit-breaker.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "lightwave", "push-circuit-breaker.json")
}

// readCircuitBreakerState returns the per-branch map. A missing file is
// treated as an empty state (not an error) — first run on a fresh machine
// must succeed without setup.
func readCircuitBreakerState(path string) (map[string]circuitBreakerEntry, error) {
	if path == "" {
		return map[string]circuitBreakerEntry{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]circuitBreakerEntry{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	state := map[string]circuitBreakerEntry{}
	if len(data) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return state, nil
}

// formatAttempt renders an ISO8601 timestamp as "2026-05-07 12:34 UTC (5m
// ago)" when parseable, falling back to the raw string otherwise.
func formatAttempt(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return fmt.Sprintf("%s (%s ago)", t.UTC().Format("2006-01-02 15:04 UTC"), time.Since(t).Round(time.Second))
}
