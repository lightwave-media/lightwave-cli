package cli

import (
	"context"
	"strings"
	"testing"
)

func TestNormalizePriority(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"P1 Urgent", "p1_urgent"},
		{"P2 High", "p2_high"},
		{"P3 Medium", "p3_medium"},
		{"P4 Low", "p4_low"},
		{"p1_urgent", "p1_urgent"},
		{"high", "p2_high"},
		{"unknown", "p3_medium"},
	}
	for _, tt := range tests {
		got := normalizePriority(tt.input)
		if got != tt.want {
			t.Errorf("normalizePriority(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"chore", "chore"},
		{"bug", "bug"},
		{"fix", "bug"},
		{"hotfix", "bug"},
		{"feature", "feature"},
		{"enhancement", "feature"},
		{"docs", "docs"},
		{"documentation", "docs"},
		{"custom", "custom"},
	}
	for _, tt := range tests {
		got := normalizeType(tt.input)
		if got != tt.want {
			t.Errorf("normalizeType(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStripSprintPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"[Sprint 6] Actual title", "Actual title"},
		{"[Sprint 12] Long sprint", "Long sprint"},
		{"No prefix here", "No prefix here"},
		{"[Sprint 1]No space", "No space"},
		{"[e80b91c8] APM: instrument Django", "APM: instrument Django"},
		{"[abcd1234] Some task", "Some task"},
		{"[ABCD1234] Not hex lowercase", "[ABCD1234] Not hex lowercase"},
	}
	for _, tt := range tests {
		got := stripSprintPrefix(tt.input)
		if got != tt.want {
			t.Errorf("stripSprintPrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseIssueBodyFull(t *testing.T) {
	body := `**Task ID:** 4ff8bbfe
**Epic:** Scrum Manager v2 — GitHub-Native Flow Management
**Priority:** P1 Urgent
**Type:** chore

Some description here.

**Dependencies:** db0512f6, a17e2134`

	issue := ghIssue{
		Body:   body,
		Labels: []ghLabel{{Name: "enhancement"}},
	}

	f := parseIssueBody(issue)

	if f.taskID != "4ff8bbfe" {
		t.Errorf("taskID = %q, want 4ff8bbfe", f.taskID)
	}
	if f.priority != "p1_urgent" {
		t.Errorf("priority = %q, want p1_urgent", f.priority)
	}
	if f.epic != "Scrum Manager v2 — GitHub-Native Flow Management" {
		t.Errorf("epic = %q", f.epic)
	}
	if f.taskType != "chore" {
		t.Errorf("taskType = %q, want chore (body Type overrides label)", f.taskType)
	}
	if len(f.deps) != 2 || f.deps[0] != "db0512f6" || f.deps[1] != "a17e2134" {
		t.Errorf("deps = %v, want [db0512f6 a17e2134]", f.deps)
	}
}

func TestParseIssueBodyNoDeps(t *testing.T) {
	body := `**Task ID:** abcd1234
**Dependencies:** None (first task)`

	f := parseIssueBody(ghIssue{Body: body})

	if len(f.deps) != 0 {
		t.Errorf("deps = %v, want empty (None should be filtered)", f.deps)
	}
}

func TestParseIssueBodyLabelFallback(t *testing.T) {
	body := `**Task ID:** abcd1234`

	f := parseIssueBody(ghIssue{
		Body:   body,
		Labels: []ghLabel{{Name: "bug"}},
	})

	if f.taskType != "bug" {
		t.Errorf("taskType = %q, want bug (label fallback)", f.taskType)
	}
}

func TestParseIssueBodyDuplicateTaskID(t *testing.T) {
	body := `**Task ID:** 30345b6c
Fix the 530/526 errors.

**Task ID:** ` + "`7e5da4f6`" + `
**Priority:** P1 Urgent`

	f := parseIssueBody(ghIssue{Body: body})

	if f.taskID != "7e5da4f6" {
		t.Errorf("taskID = %q, want 7e5da4f6 (last match wins)", f.taskID)
	}
}

func TestParseIssueBodyEmpty(t *testing.T) {
	f := parseIssueBody(ghIssue{Body: ""})

	if f.taskID != "" {
		t.Errorf("taskID = %q, want empty", f.taskID)
	}
	if f.priority != "" {
		t.Errorf("priority = %q, want empty", f.priority)
	}
	if f.taskType != "feature" {
		t.Errorf("taskType = %q, want feature (default)", f.taskType)
	}
}

func TestParseIssueBodyAcceptanceCriteria(t *testing.T) {
	t.Run("bold format", func(t *testing.T) {
		body := `**Priority:** P2 High

**Acceptance Criteria:**
- Issues labeled ready are eligible for pickup
- Priority ordering via issue labels (p1/p2/p3/p4)
- Removes dependency on lw task next-approved

**Dependencies:** None (first task)`

		f := parseIssueBody(ghIssue{Body: body})

		if f.acceptanceCriteria == "" {
			t.Fatal("expected acceptance criteria to be extracted")
		}
		if !strings.Contains(f.acceptanceCriteria, "Issues labeled ready") {
			t.Errorf("AC = %q, want to contain 'Issues labeled ready'", f.acceptanceCriteria)
		}
		if !strings.Contains(f.acceptanceCriteria, "Removes dependency") {
			t.Errorf("AC = %q, want to contain 'Removes dependency'", f.acceptanceCriteria)
		}
	})

	t.Run("heading with checkboxes", func(t *testing.T) {
		body := `**Priority:** P1 Urgent

## Acceptance Criteria
- [ ] cineos.io DNS points to ALB
- [ ] ACM certificate covers cineos.io

**Dependencies:** abcd1234`

		f := parseIssueBody(ghIssue{Body: body})

		if f.acceptanceCriteria == "" {
			t.Fatal("expected acceptance criteria to be extracted")
		}
		if !strings.Contains(f.acceptanceCriteria, "cineos.io DNS points to ALB") {
			t.Errorf("AC = %q, want to contain 'cineos.io DNS points to ALB'", f.acceptanceCriteria)
		}
		if !strings.Contains(f.acceptanceCriteria, "ACM certificate covers cineos.io") {
			t.Errorf("AC = %q, want to contain 'ACM certificate covers cineos.io'", f.acceptanceCriteria)
		}
	})
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("this is a very long string", 10); got != "this is..." {
		t.Errorf("truncate long = %q", got)
	}
}

func TestIssuePriorityRank(t *testing.T) {
	tests := []struct {
		labels []ghLabel
		body   string
		want   int
	}{
		{[]ghLabel{{Name: "p1"}}, "", 1},
		{[]ghLabel{{Name: "P2 High"}}, "", 2},
		{[]ghLabel{{Name: "ready"}, {Name: "p3"}}, "", 3},
		{[]ghLabel{{Name: "p4"}}, "", 4},
		// Fallback to body priority
		{nil, "**Priority:** P1 Urgent", 1},
		{nil, "**Priority:** P2 High", 2},
		// No priority at all
		{nil, "", 5},
	}
	for _, tt := range tests {
		issue := ghIssue{Labels: tt.labels, Body: tt.body}
		got := issuePriorityRank(issue)
		if got != tt.want {
			t.Errorf("issuePriorityRank(labels=%v, body=%q) = %d, want %d", tt.labels, tt.body, got, tt.want)
		}
	}
}

func TestDepsOKNoPool(t *testing.T) {
	// Without a DB pool, depsOK should always return true
	issue := ghIssue{Body: "**Dependencies:** abcd1234, ef567890"}
	if !depsOK(context.Background(), nil, issue, nil) {
		t.Error("depsOK(nil pool) should be true")
	}
}

func TestDepsOKNoDeps(t *testing.T) {
	issue := ghIssue{Body: "**Dependencies:** None"}
	if !depsOK(context.Background(), nil, issue, nil) {
		t.Error("depsOK(no deps) should be true")
	}
}

func TestSortByPriority(t *testing.T) {
	issues := []ghIssue{
		{Number: 1, Labels: []ghLabel{{Name: "p3"}}},
		{Number: 2, Labels: []ghLabel{{Name: "p1"}}},
		{Number: 3, Labels: []ghLabel{{Name: "p2"}}},
		{Number: 4},
	}
	sortByPriority(issues)
	if issues[0].Number != 2 || issues[1].Number != 3 || issues[2].Number != 1 || issues[3].Number != 4 {
		got := []int{issues[0].Number, issues[1].Number, issues[2].Number, issues[3].Number}
		t.Errorf("sortByPriority order = %v, want [2 3 1 4]", got)
	}
}

func TestMapLabelsToType(t *testing.T) {
	tests := []struct {
		labels []ghLabel
		want   string
	}{
		{[]ghLabel{{Name: "bug"}}, "bug"},
		{[]ghLabel{{Name: "enhancement"}}, "feature"},
		{[]ghLabel{{Name: "documentation"}}, "docs"},
		{[]ghLabel{{Name: "help wanted"}}, "feature"},
		{nil, "feature"},
	}
	for _, tt := range tests {
		got := mapLabelsToType(tt.labels)
		if got != tt.want {
			t.Errorf("mapLabelsToType(%v) = %q, want %q", tt.labels, got, tt.want)
		}
	}
}
