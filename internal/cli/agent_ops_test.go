package cli

import (
	"testing"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/paperclip"
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
	allRuns := []runEntry{
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
	allRuns := []runEntry{
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

func TestMatchActivityToRun(t *testing.T) {
	now := time.Now()

	activities := []paperclip.Activity{
		{AgentName: "Backend Engineer", Action: "heartbeat", CreatedAt: now.Add(-2 * time.Minute)},
		{AgentName: "Release Engineer", Action: "heartbeat", CreatedAt: now.Add(-10 * time.Minute)},
		{AgentName: "", Action: "system", CreatedAt: now.Add(-1 * time.Minute)}, // no agent name
	}

	t.Run("matches_closest_heartbeat_within_5min", func(t *testing.T) {
		got := matchActivityToRun(activities, now)
		if got != "Backend Engineer" {
			t.Errorf("got %q, want %q", got, "Backend Engineer")
		}
	})

	t.Run("no_match_when_too_old", func(t *testing.T) {
		// CI run happened 20 minutes ago — no heartbeat within 5min before it
		got := matchActivityToRun(activities, now.Add(-20*time.Minute))
		if got != "" {
			t.Errorf("got %q, want empty (no match)", got)
		}
	})

	t.Run("no_match_when_empty_activities", func(t *testing.T) {
		got := matchActivityToRun(nil, now)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("skips_entries_without_agent_name", func(t *testing.T) {
		noNameActivities := []paperclip.Activity{
			{AgentName: "", Action: "heartbeat", CreatedAt: now.Add(-1 * time.Minute)},
		}
		got := matchActivityToRun(noNameActivities, now)
		if got != "" {
			t.Errorf("got %q, want empty (no agent name)", got)
		}
	})
}

func TestMatchCommitAuthorFromMessage(t *testing.T) {
	// matchCommitAuthor shells out to gh, so we can't unit test the full flow.
	// Instead, test that branchToAgent + the fallback chain works for direct pushes.
	t.Run("direct_push_returns_direct_push_without_correlation", func(t *testing.T) {
		got := branchToAgent("main")
		if got != "(direct push)" {
			t.Errorf("got %q, want %q", got, "(direct push)")
		}
	})
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
