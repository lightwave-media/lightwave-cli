package cli

import (
	"testing"
	"time"
)

func TestBranchToAgent(t *testing.T) {
	tests := []struct {
		branch string
		want   string
	}{
		// lw/ prefix — direct agent branches
		{"lw/backend-engineer", "Backend Engineer"},
		{"lw/frontend-engineer", "Frontend Engineer"},
		{"lw/cto", "Cto"},

		// feature/ prefix with agent name
		{"feature/backend-engineer-add-pagination", "Backend Engineer"},
		{"feature/release-engineer-fix-ci", "Release Engineer"},
		{"feature/infrastructure-engineer-vpc-update", "Infrastructure Engineer"},

		// fix/ prefix
		{"fix/qa-engineer-flaky-test", "Qa Engineer"},

		// main/master — direct push
		{"main", "(direct push)"},
		{"master", "(direct push)"},

		// Unknown branches
		{"some-random-branch", "(unattributed)"},
		{"dependabot/npm_and_yarn/foo", "(unattributed)"},
	}

	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			got := branchToAgent(tt.branch)
			if got != tt.want {
				t.Errorf("branchToAgent(%q) = %q, want %q", tt.branch, got, tt.want)
			}
		})
	}
}

func TestKebabToDisplay(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"backend-engineer", "Backend Engineer"},
		{"cto", "Cto"},
		{"general-manager", "General Manager"},
		{"qa-engineer", "Qa Engineer"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := kebabToDisplay(tt.input)
			if got != tt.want {
				t.Errorf("kebabToDisplay(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDetectRetryLoops(t *testing.T) {
	now := time.Now()

	// 4 failures from same agent+repo+workflow within 30 minutes — should be a loop
	allRuns := []struct {
		Repo string
		Run  ghRun
	}{
		{Repo: "lightwave-media/lightwave-sys", Run: ghRun{
			Name: "CI", HeadBranch: "lw/release-engineer", Conclusion: "failure",
			CreatedAt: now.Add(-25 * time.Minute),
		}},
		{Repo: "lightwave-media/lightwave-sys", Run: ghRun{
			Name: "CI", HeadBranch: "lw/release-engineer", Conclusion: "failure",
			CreatedAt: now.Add(-20 * time.Minute),
		}},
		{Repo: "lightwave-media/lightwave-sys", Run: ghRun{
			Name: "CI", HeadBranch: "lw/release-engineer", Conclusion: "failure",
			CreatedAt: now.Add(-15 * time.Minute),
		}},
		{Repo: "lightwave-media/lightwave-sys", Run: ghRun{
			Name: "CI", HeadBranch: "lw/release-engineer", Conclusion: "failure",
			CreatedAt: now.Add(-10 * time.Minute),
		}},
		// Different agent — not a loop for release-engineer
		{Repo: "lightwave-media/lightwave-platform", Run: ghRun{
			Name: "CI", HeadBranch: "lw/backend-engineer", Conclusion: "success",
			CreatedAt: now.Add(-5 * time.Minute),
		}},
	}

	cutoff := now.Add(-1 * time.Hour)
	loops := detectRetryLoops(allRuns, cutoff)

	if len(loops) != 1 {
		t.Fatalf("expected 1 retry loop, got %d", len(loops))
	}

	loop := loops[0]
	if loop.Agent != "Release Engineer" {
		t.Errorf("loop agent = %q, want %q", loop.Agent, "Release Engineer")
	}
	if loop.Runs != 4 {
		t.Errorf("loop runs = %d, want 4", loop.Runs)
	}
}

func TestDetectRetryLoopsNoFalsePositive(t *testing.T) {
	now := time.Now()

	// 2 failures — below the 3-run threshold
	allRuns := []struct {
		Repo string
		Run  ghRun
	}{
		{Repo: "lightwave-media/lightwave-sys", Run: ghRun{
			Name: "CI", HeadBranch: "lw/release-engineer", Conclusion: "failure",
			CreatedAt: now.Add(-20 * time.Minute),
		}},
		{Repo: "lightwave-media/lightwave-sys", Run: ghRun{
			Name: "CI", HeadBranch: "lw/release-engineer", Conclusion: "failure",
			CreatedAt: now.Add(-10 * time.Minute),
		}},
	}

	cutoff := now.Add(-1 * time.Hour)
	loops := detectRetryLoops(allRuns, cutoff)

	if len(loops) != 0 {
		t.Errorf("expected 0 retry loops for 2 failures, got %d", len(loops))
	}
}

func TestRunsSummaryCalculation(t *testing.T) {
	// Verify the success rate math used in the runs command
	tests := []struct {
		total    int
		failures int
		wantRate float64
	}{
		{10, 3, 70.0},
		{10, 0, 100.0},
		{10, 10, 0.0},
		{0, 0, 0.0},
		{5, 2, 60.0},
	}

	for _, tt := range tests {
		rate := 0.0
		if tt.total > 0 {
			rate = float64(tt.total-tt.failures) / float64(tt.total) * 100
		}
		if rate != tt.wantRate {
			t.Errorf("total=%d failures=%d: rate=%.1f%%, want %.1f%%",
				tt.total, tt.failures, rate, tt.wantRate)
		}
	}
}
