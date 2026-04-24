package cli

import (
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/paperclip"
)

// TestAgentStatusColorHelper verifies the color helper handles known statuses.
func TestAgentStatusColorHelper(t *testing.T) {
	tests := []struct {
		status       string
		wantNonBlank bool
	}{
		{"running", true},
		{"idle", true},
		{"active", true},
		{"working", true},
		{"error", true},
		{"failed", true},
		{"", true}, // unknown status: returns as-is (blank string)
		{"unknown", true},
	}

	for _, tt := range tests {
		t.Run("status="+tt.status, func(t *testing.T) {
			got := agentStatusColor(tt.status)
			// color-stripped version should contain the original status text (or be empty)
			stripped := stripANSI(got)
			if tt.status != "" && !strings.Contains(stripped, tt.status) {
				t.Errorf("agentStatusColor(%q) = %q: stripped %q does not contain original status", tt.status, got, stripped)
			}
		})
	}
}

// TestTruncateStr verifies the truncation helper for safe inputs.
func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input    string
		max      int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"hello world", 11, "hello world"},
		{"hello world", 8, "hello..."},
		{"hi", 2, "hi"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := truncateStr(tt.input, tt.max)
			if got != tt.expected {
				t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.expected)
			}
		})
	}
}

// TestTruncateStrPanicOnSmallMaxBug documents a panic in truncateStr when max < 3.
//
// BUG (LIGA-21): truncateStr(s, max) does s[:max-3]+"..." when len(s) > max.
// When max < 3, max-3 is negative, causing a runtime panic (slice bounds out of range).
// This can be triggered by any caller passing a small column width.
//
// Reproduction: truncateStr("hello", 1) → panic: slice bounds out of range [:-2]
//
// Fix: guard with if max <= 3 { return "..." } before the slice.
func TestTruncateStrPanicOnSmallMaxBug(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("BUG LIGA-21: truncateStr panics on max<3 — agent.go:369 does s[:max-3] without bounds guard; got panic: %v", r)
		}
	}()
	// This call should not panic but currently does.
	_ = truncateStr("hello", 1)
}

// TestAgentNameMatchingBehavior documents the current exact-match behaviour of
// lw agent status and lw agent assign. These commands require the exact agent
// name as shown by lw agent list (e.g. "Backend Engineer"), not the kebab-case
// form shown in the help text (e.g. "backend-engineer").
//
// BUG (LIGA-21): The help text examples use kebab-case ("backend-engineer") but
// the match is case-sensitive exact string equality. This causes:
//
//	lw agent status backend-engineer   → "agent not found" (incorrect)
//	lw agent status "Backend Engineer" → OK (works)
//
// Expected (desired) behaviour: both forms should resolve to the same agent.
func TestAgentNameMatchingBehavior(t *testing.T) {
	agents := []paperclip.Agent{
		{Name: "Backend Engineer", CompanyName: "LightWave Media LLC"},
		{Name: "QA Engineer", CompanyName: "LightWave Media LLC"},
		{Name: "Frontend Engineer", CompanyName: "LightWave Media LLC"},
	}

	normalizedFind := func(name string) *paperclip.Agent {
		normalized := normalizeAgentName(name)
		for i, a := range agents {
			if normalizeAgentName(a.Name) == normalized {
				return &agents[i]
			}
		}
		return nil
	}

	// --- Exact match: works ---
	t.Run("exact_case_match_works", func(t *testing.T) {
		if got := normalizedFind("Backend Engineer"); got == nil {
			t.Error("exact match 'Backend Engineer' should find agent")
		}
	})

	// --- Kebab-case: fixed by normalizeAgentName ---
	t.Run("kebab_case_should_match", func(t *testing.T) {
		if got := normalizedFind("backend-engineer"); got == nil {
			t.Error("kebab-case 'backend-engineer' should resolve to 'Backend Engineer' via normalizeAgentName")
		}
	})

	// --- Lowercase with spaces: fixed by normalizeAgentName ---
	t.Run("lowercase_space_should_match", func(t *testing.T) {
		if got := normalizedFind("backend engineer"); got == nil {
			t.Error("lowercase 'backend engineer' should resolve to 'Backend Engineer' via normalizeAgentName")
		}
	})
}

// TestAgentSyncDuplicateCompanyBug documents the duplicate company in lw agent sync.
//
// BUG (LIGA-21): lw agent sync --json returns two entries both named
// "LightWave Media LLC", one with 70 issues and one with 0 issues.
// This suggests /api/companies returns two company records with the same name.
// The sync output is misleading and the 0-issue entry is noise.
//
// Reproduction:
//
//	lw agent sync --json | python3 -c "import json,sys; d=json.load(sys.stdin); print([x['company'] for x in d])"
//	# => ['LightWave Media LLC', 'LightWave Media LLC']
//
// Expected: one entry per unique company name.
func TestAgentSyncDuplicateCompanyBug(t *testing.T) {
	// This is a documentation test — it validates the fix once applied.
	// Until then it passes vacuously (the live check requires Paperclip running).
	t.Skip("live test: run manually with 'lw agent sync --json' to reproduce duplicate company entry")
}

// stripANSI removes ANSI escape codes for test comparison.
func stripANSI(s string) string {
	// Simple strip: remove ESC[...m sequences
	out := strings.Builder{}
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// Skip until 'm'
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++ // skip 'm'
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}
