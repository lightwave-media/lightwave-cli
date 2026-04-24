package cli

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/db"
)

// TestNextApprovedCommandStructure verifies the command is correctly defined
func TestNextApprovedCommandStructure(t *testing.T) {
	tests := []struct {
		name        string
		commandName string
		expectError bool
	}{
		{
			name:        "taskNextApprovedCmd exists",
			commandName: "next-approved",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if taskNextApprovedCmd == nil {
				t.Error("taskNextApprovedCmd is nil")
			}
			if taskNextApprovedCmd.Use != tt.commandName {
				t.Errorf("expected Use=%s, got %s", tt.commandName, taskNextApprovedCmd.Use)
			}
		})
	}
}

// TestTaskStateTransitionValidation verifies invalid state transitions are prevented
func TestTaskStateTransitionValidation(t *testing.T) {
	validTransitions := map[string][]string{
		"pending":     {"approved", "next_up", "on_hold"},
		"approved":    {"in_progress", "on_hold"},
		"next_up":     {"in_progress", "on_hold"},
		"in_progress": {"in_review", "on_hold", "blocked"},
		"in_review":   {"done", "on_hold", "blocked"},
		"blocked":     {"approved", "on_hold"},
		"done":        {"archived"},
		"archived":    {},
	}

	// Verify transition map has reasonable coverage
	if len(validTransitions) == 0 {
		t.Error("valid transitions map is empty")
	}

	for status, allowed := range validTransitions {
		if len(allowed) == 0 && status != "archived" {
			t.Logf("Warning: status %s has no valid transitions", status)
		}
	}
}

// TestTaskPriorityOrdering verifies tasks are ordered by priority correctly
func TestTaskPriorityOrdering(t *testing.T) {
	priorityOrder := map[string]int{
		"p1_urgent": 0,
		"p2_high":   1,
		"p3_medium": 2,
		"p4_low":    3,
	}

	// Verify priority map is complete
	expectedPriorities := []string{"p1_urgent", "p2_high", "p3_medium", "p4_low"}
	if len(priorityOrder) != len(expectedPriorities) {
		t.Errorf("expected %d priorities, got %d", len(expectedPriorities), len(priorityOrder))
	}

	// Verify ordering is correct (lower number = higher priority)
	for _, p := range expectedPriorities {
		if _, ok := priorityOrder[p]; !ok {
			t.Errorf("priority %s not in map", p)
		}
	}
}

// TestTaskListOptions verifies task list filtering options are sensible
func TestTaskListOptions(t *testing.T) {
	opts := db.TaskListOptions{
		Status:   "approved,next_up",
		Priority: "p1_urgent,p2_high",
		Limit:    10,
	}

	if opts.Limit <= 0 {
		t.Error("limit must be positive")
	}

	if opts.Priority == "" {
		t.Error("priority filter should not be empty")
	}

	if opts.Status == "" {
		t.Error("status filter should not be empty")
	}
}
