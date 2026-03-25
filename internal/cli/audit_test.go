package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCalculateScore(t *testing.T) {
	tests := []struct {
		name     string
		counts   SeverityCounts
		expected int
	}{
		{"perfect", SeverityCounts{}, 100},
		{"one critical", SeverityCounts{Critical: 1}, 75},
		{"four criticals floors at zero", SeverityCounts{Critical: 4}, 0},
		{"mixed", SeverityCounts{Critical: 1, High: 2, Medium: 5, Low: 3}, 37},
		{"all low", SeverityCounts{Low: 10}, 90},
		{"overflow floors at zero", SeverityCounts{Critical: 10, High: 10}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateScore(tt.counts)
			if got != tt.expected {
				t.Errorf("calculateScore(%+v) = %d, want %d", tt.counts, got, tt.expected)
			}
		})
	}
}

func TestStatusFromScore(t *testing.T) {
	tests := []struct {
		score    int
		expected string
	}{
		{100, "on_track"},
		{80, "on_track"},
		{79, "at_risk"},
		{50, "at_risk"},
		{49, "off_track"},
		{0, "off_track"},
	}

	for _, tt := range tests {
		got := statusFromScore(tt.score)
		if got != tt.expected {
			t.Errorf("statusFromScore(%d) = %q, want %q", tt.score, got, tt.expected)
		}
	}
}

func TestCompositeKey(t *testing.T) {
	f := AuditFinding{
		Source:   "security",
		File:     "apps/core/settings.py",
		Line:     42,
		Category: "debug",
	}
	expected := "security:apps/core/settings.py:42:debug"
	if got := f.CompositeKey(); got != expected {
		t.Errorf("CompositeKey() = %q, want %q", got, expected)
	}
}

func TestDedup(t *testing.T) {
	findings := []AuditFinding{
		{Source: "security", File: "a.py", Line: 1, Category: "debug", Finding: "first"},
		{Source: "security", File: "a.py", Line: 1, Category: "debug", Finding: "duplicate"},
		{Source: "security", File: "a.py", Line: 2, Category: "debug", Finding: "different line"},
	}
	got := dedup(findings)
	if len(got) != 2 {
		t.Errorf("dedup returned %d findings, want 2", len(got))
	}
	// First occurrence wins
	if got[0].Finding != "first" {
		t.Errorf("dedup kept wrong finding: %q", got[0].Finding)
	}
}

func TestCountSeverities(t *testing.T) {
	findings := []AuditFinding{
		{Severity: "critical"},
		{Severity: "high"},
		{Severity: "high"},
		{Severity: "medium"},
		{Severity: "medium"},
		{Severity: "medium"},
		{Severity: "low"},
	}
	got := countSeverities(findings)
	if got.Critical != 1 || got.High != 2 || got.Medium != 3 || got.Low != 1 {
		t.Errorf("countSeverities = %+v, want {1,2,3,1}", got)
	}
}

func TestCollectGates(t *testing.T) {
	// Only run if gates.yaml exists
	home, _ := os.UserHomeDir()
	gatesPath := filepath.Join(home, ".brain", "governance", "audit", "gates.yaml")
	if _, err := os.Stat(gatesPath); os.IsNotExist(err) {
		t.Skip("gates.yaml not found, skipping")
	}

	findings, section, err := collectGates()
	if err != nil {
		t.Fatalf("collectGates() error: %v", err)
	}

	if section.Implemented == 0 {
		t.Error("expected at least one implemented gate")
	}
	if section.CoveragePct < 0 || section.CoveragePct > 100 {
		t.Errorf("coverage_pct out of range: %d", section.CoveragePct)
	}
	// Every gap should have a corresponding finding
	if len(section.Gaps) > 0 && len(findings) == 0 {
		t.Error("gaps exist but no findings generated")
	}
}

func TestBuildNarrative(t *testing.T) {
	counts := SeverityCounts{Critical: 2, High: 5}
	gates := &GatesSection{CoveragePct: 72}
	got := buildNarrative(counts, gates, nil)

	if got == "" {
		t.Error("narrative should not be empty")
	}
	if !contains(got, "2 critical") {
		t.Errorf("narrative missing critical count: %q", got)
	}
	if !contains(got, "72%") {
		t.Errorf("narrative missing gate coverage: %q", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestExtractDate(t *testing.T) {
	got := extractDate("/path/to/2026-03-23_audit.json")
	if got != "2026-03-23" {
		t.Errorf("extractDate = %q, want %q", got, "2026-03-23")
	}
}
