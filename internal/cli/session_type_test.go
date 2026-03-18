package cli

import "testing"

func TestInferSessionType(t *testing.T) {
	tests := []struct {
		name     string
		labels   []string
		expected SessionType
	}{
		{"no labels defaults to backend", nil, SessionBackend},
		{"backend label", []string{"backend", "p1"}, SessionBackend},
		{"frontend label", []string{"frontend", "ready"}, SessionFrontend},
		{"infra label", []string{"infra"}, SessionInfra},
		{"python maps to backend", []string{"python"}, SessionBackend},
		{"docker maps to infra", []string{"docker"}, SessionInfra},
		{"github-actions maps to infra", []string{"github-actions"}, SessionInfra},
		{"backend wins over frontend", []string{"frontend", "backend"}, SessionBackend},
		{"backend wins over infra", []string{"infra", "backend"}, SessionBackend},
		{"frontend wins over infra", []string{"infra", "frontend"}, SessionFrontend},
		{"unrelated labels default backend", []string{"bug", "enhancement", "p2"}, SessionBackend},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InferSessionType(tt.labels)
			if got != tt.expected {
				t.Errorf("InferSessionType(%v) = %s, want %s", tt.labels, got, tt.expected)
			}
		})
	}
}

func TestIssueBranchName(t *testing.T) {
	tests := []struct {
		name     string
		number   int
		title    string
		taskType string
		expected string
	}{
		{"feature", 52, "GitHub query picker", "feature", "feat/issue-52-github-query-picker"},
		{"bug fix", 10, "Login fails on mobile", "bug", "fix/issue-10-login-fails-on-mobile"},
		{"chore", 99, "Update dependencies", "chore", "chore/issue-99-update-dependencies"},
		{"hotfix", 5, "Critical crash", "hotfix", "fix/issue-5-critical-crash"},
		{"long title truncated", 42, "This is an extremely long title that should be truncated to avoid branch name issues in git", "feature",
			"feat/issue-42-this-is-an-extremely-long-title-that"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IssueBranchName(tt.number, tt.title, tt.taskType)
			if got != tt.expected {
				t.Errorf("IssueBranchName(%d, %q, %q) = %q, want %q", tt.number, tt.title, tt.taskType, got, tt.expected)
			}
		})
	}
}

func TestSessionTypeWorkingDir(t *testing.T) {
	if SessionBackend.WorkingDir() != "packages/lightwave-core" {
		t.Errorf("backend working dir wrong: %s", SessionBackend.WorkingDir())
	}
	if SessionFrontend.WorkingDir() != "packages/lightwave-frontend" {
		t.Errorf("frontend working dir wrong: %s", SessionFrontend.WorkingDir())
	}
	if SessionInfra.WorkingDir() != "packages/lightwave-infra" {
		t.Errorf("infra working dir wrong: %s", SessionInfra.WorkingDir())
	}
}
