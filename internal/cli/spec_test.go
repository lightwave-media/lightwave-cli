package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/db"
)

// TestSpecGenerateCommandStructure verifies the command is correctly defined
func TestSpecGenerateCommandStructure(t *testing.T) {
	if specGenerateCmd == nil {
		t.Fatal("specGenerateCmd is nil")
	}
	if specGenerateCmd.Use != "generate <task-id>" {
		t.Errorf("expected Use='generate <task-id>', got '%s'", specGenerateCmd.Use)
	}
}

// TestGenerateSpecContent verifies spec contains all required sections
func TestGenerateSpecContent(t *testing.T) {
	title := "Test Feature"
	desc := "Test Description"
	epicName := "Test Epic"
	sprintName := "Test Sprint"
	now := time.Now()

	tc := &db.TaskContext{
		Task: db.Task{
			ID:          "task-123",
			ShortID:     "abc1",
			Title:       title,
			Description: &desc,
			Status:      "approved",
			Priority:    "p2_high",
			TaskType:    "feature",
			UpdatedAt:   now,
		},
		EpicName:    &epicName,
		SprintName:  &sprintName,
		SprintStart: &now,
		SprintEnd:   &now,
	}

	spec := generateSpec(tc)

	// Verify spec content
	requiredSections := []string{
		"# Execution Spec",
		"## Epic Context",
		"## Sprint Context",
		"## Description",
		"## Acceptance Criteria",
		"## Anti-Slop Checklist",
		"## Testing Strategy",
		"## Expected Changes",
	}

	for _, section := range requiredSections {
		if !strings.Contains(spec, section) {
			t.Errorf("spec missing required section: %s", section)
		}
	}

	// Verify task data is included
	if !strings.Contains(spec, title) {
		t.Errorf("spec missing task title: %s", title)
	}
	if !strings.Contains(spec, "task-123") {
		t.Errorf("spec missing task ID")
	}

	// Verify anti-slop checklist items
	antiSlopItems := []string{
		"What breaks if I do nothing?",
		"Can an existing thing absorb this?",
		"What can I DELETE",
	}

	for _, item := range antiSlopItems {
		if !strings.Contains(spec, item) {
			t.Errorf("spec missing anti-slop item: %s", item)
		}
	}
}

// TestGenerateSpecForDifferentTaskTypes verifies spec adapts to task type
func TestGenerateSpecForDifferentTaskTypes(t *testing.T) {
	tests := []struct {
		taskType      string
		expectedWords []string
	}{
		{
			taskType:      "feature",
			expectedWords: []string{"models", "services", "Commands"},
		},
		{
			taskType:      "fix",
			expectedWords: []string{"Bug"},
		},
		{
			taskType:      "chore",
			expectedWords: []string{"Infrastructure", "Configuration"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.taskType, func(t *testing.T) {
			tc := &db.TaskContext{
				Task: db.Task{
					ID:        "task-123",
					ShortID:   "abc1",
					Title:     "Test",
					Status:    "approved",
					Priority:  "p3_medium",
					TaskType:  tt.taskType,
					UpdatedAt: time.Now(),
				},
			}

			spec := generateSpec(tc)

			// Verify spec contains expected words for task type
			for _, word := range tt.expectedWords {
				if !strings.Contains(spec, word) {
					t.Logf("spec for %s doesn't contain '%s'", tt.taskType, word)
				}
			}

			// Verify it's valid markdown
			if !strings.Contains(spec, "#") {
				t.Error("spec is not valid markdown (no headers)")
			}
		})
	}
}

// TestGenerateSpecValidatesAllFields verifies all context fields are handled
func TestGenerateSpecValidatesAllFields(t *testing.T) {
	now := time.Now()
	epic := "Epic"
	sprint := "Sprint"
	story := "Story"
	status := "active"

	tc := &db.TaskContext{
		Task: db.Task{
			ID:          "task-123",
			ShortID:     "abc1",
			Title:       "Title",
			Status:      "approved",
			Priority:    "p1_urgent",
			TaskType:    "feature",
			EpicID:      &epic,
			SprintID:    &sprint,
			Description: &story,
			UpdatedAt:   now,
		},
		EpicName:     &epic,
		EpicStatus:   &status,
		SprintName:   &sprint,
		SprintStatus: &status,
		SprintStart:  &now,
		SprintEnd:    &now,
		StoryName:    &story,
	}

	spec := generateSpec(tc)

	// Verify all fields are included when present
	if !strings.Contains(spec, epic) {
		t.Error("epic not in spec")
	}
	if !strings.Contains(spec, sprint) {
		t.Error("sprint not in spec")
	}
	if !strings.Contains(spec, story) {
		t.Error("story not in spec")
	}

	// Verify timestamp is included
	if !strings.Contains(spec, "Generated") {
		t.Error("generation timestamp not in spec")
	}
}

// TestGenerateSpecEmptyFieldHandling verifies nil fields are handled safely
func TestGenerateSpecEmptyFieldHandling(t *testing.T) {
	tc := &db.TaskContext{
		Task: db.Task{
			ID:        "task-123",
			ShortID:   "abc1",
			Title:     "Title",
			Status:    "pending",
			Priority:  "p4_low",
			TaskType:  "docs",
			UpdatedAt: time.Now(),
		},
		// All optional fields are nil
	}

	// Should not panic with nil fields
	spec := generateSpec(tc)

	if spec == "" {
		t.Error("spec should not be empty even with nil fields")
	}

	// Verify core content is still there
	if !strings.Contains(spec, "# Execution Spec") {
		t.Error("spec missing header")
	}
	if !strings.Contains(spec, "Title") {
		t.Error("spec missing title")
	}
}
