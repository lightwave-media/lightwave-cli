package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/spf13/cobra"
)

var (
	specOutputDir string
	specFull      bool
)

var specCmd = &cobra.Command{
	Use:   "spec",
	Short: "Spec generation commands",
	Long:  `Generate execution specs for Claude Code sessions from task context.`,
}

var specGenerateCmd = &cobra.Command{
	Use:   "generate <task-id>",
	Short: "Generate execution spec from task",
	Long: `Generate a comprehensive spec markdown for Claude Code session.
Includes task context, acceptance criteria, anti-slop checklist, and testing strategy.

Output: spec markdown file with full task context and execution instructions.

Examples:
  lw spec generate abc123
  lw spec generate abc123 --output-dir=/tmp`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		// Get full task context
		tc, err := db.GetTaskContext(ctx, pool, taskID)
		if err != nil {
			return err
		}

		// Generate spec
		spec := generateSpec(tc)

		// Determine output directory
		outDir := specOutputDir
		if outDir == "" {
			outDir = "/tmp"
		}

		// Create spec file
		specFilename := filepath.Join(outDir, fmt.Sprintf("spec-%s.md", tc.ShortID))
		if err := os.WriteFile(specFilename, []byte(spec), 0644); err != nil {
			return fmt.Errorf("failed to write spec: %w", err)
		}

		fmt.Printf("Spec generated: %s\n", color.GreenString(specFilename))
		fmt.Printf("Spec size: %d bytes\n", len(spec))
		return nil
	},
}

// generateSpec creates a comprehensive spec from task context
func generateSpec(tc *db.TaskContext) string {
	var sb strings.Builder

	sb.WriteString("# Execution Spec\n\n")
	sb.WriteString(fmt.Sprintf("**Task:** %s  \n", tc.Title))
	sb.WriteString(fmt.Sprintf("**Task ID:** %s  \n", tc.ID))
	sb.WriteString(fmt.Sprintf("**Status:** %s  \n", tc.Status))
	sb.WriteString(fmt.Sprintf("**Priority:** %s  \n\n", tc.PriorityDisplay()))

	// Epic context
	if tc.EpicName != nil && *tc.EpicName != "" {
		sb.WriteString("## Epic Context\n\n")
		sb.WriteString(fmt.Sprintf("**Epic:** %s\n", *tc.EpicName))
		if tc.EpicID != nil {
			sb.WriteString(fmt.Sprintf("**Epic ID:** %s\n", *tc.EpicID))
		}
		if tc.EpicStatus != nil {
			sb.WriteString(fmt.Sprintf("**Epic Status:** %s\n\n", *tc.EpicStatus))
		}
	}

	// Sprint context
	if tc.SprintName != nil && *tc.SprintName != "" {
		sb.WriteString("## Sprint Context\n\n")
		sb.WriteString(fmt.Sprintf("**Sprint:** %s\n", *tc.SprintName))
		if tc.SprintID != nil {
			sb.WriteString(fmt.Sprintf("**Sprint ID:** %s\n", *tc.SprintID))
		}
		if tc.SprintStatus != nil {
			sb.WriteString(fmt.Sprintf("**Sprint Status:** %s\n", *tc.SprintStatus))
		}
		if tc.SprintStart != nil && tc.SprintEnd != nil {
			sb.WriteString(fmt.Sprintf("**Sprint Dates:** %s to %s\n\n",
				tc.SprintStart.Format("2006-01-02"),
				tc.SprintEnd.Format("2006-01-02")))
		}
	}

	// User story
	if tc.StoryName != nil && *tc.StoryName != "" {
		sb.WriteString("## User Story\n\n")
		sb.WriteString(fmt.Sprintf("%s\n\n", *tc.StoryName))
	}

	// Description
	if tc.Description != nil && *tc.Description != "" {
		sb.WriteString("## Description\n\n")
		sb.WriteString(fmt.Sprintf("%s\n\n", *tc.Description))
	}

	// Acceptance criteria (placeholder - would come from task details)
	sb.WriteString("## Acceptance Criteria\n\n")
	sb.WriteString("- [ ] Requirement 1\n")
	sb.WriteString("- [ ] Requirement 2\n")
	sb.WriteString("- [ ] Requirement 3\n")
	sb.WriteString("- [ ] All tests passing\n\n")

	// Anti-slop checklist from CLAUDE.md
	sb.WriteString("## Anti-Slop Checklist\n\n")
	sb.WriteString("Before implementing, answer these:\n\n")
	sb.WriteString("- [ ] **What breaks if I do nothing?** If nothing breaks, don't build it.\n")
	sb.WriteString("- [ ] **Can an existing thing absorb this?** Don't create new files/models/services.\n")
	sb.WriteString("- [ ] **What can I DELETE to make this unnecessary?** Solve by subtraction first.\n")
	sb.WriteString("- [ ] **Am I automating something that should be manual?** Don't automate the unnecessary.\n")
	sb.WriteString("- [ ] **Am I optimizing something that shouldn't exist?** Simplify or delete first.\n")
	sb.WriteString("- [ ] **What existing code becomes dead if I build this?** Delete it in the same PR.\n\n")

	// Testing strategy
	sb.WriteString("## Testing Strategy\n\n")
	sb.WriteString("- [ ] Unit tests for new logic\n")
	sb.WriteString("- [ ] Integration tests with existing systems\n")
	sb.WriteString("- [ ] Pre-commit passes (ruff, prettier, detect-secrets, tests)\n")
	sb.WriteString("- [ ] No dead code left behind\n\n")

	// Files that will likely change
	sb.WriteString("## Expected Changes\n\n")
	sb.WriteString("Based on task type, these file patterns will likely change:\n\n")
	switch tc.TaskType {
	case "feature":
		sb.WriteString("- New models/services (if necessary)\n")
		sb.WriteString("- CLI commands\n")
		sb.WriteString("- Tests\n")
		sb.WriteString("- Documentation\n")
	case "fix":
		sb.WriteString("- Bug-containing module\n")
		sb.WriteString("- Related tests\n")
	case "chore":
		sb.WriteString("- Infrastructure/build files\n")
		sb.WriteString("- Configuration\n")
	}
	sb.WriteString("\n")

	// Generated metadata
	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("**Generated:** %s  \n", time.Now().Format("2006-01-02 15:04:05 MST")))
	sb.WriteString(fmt.Sprintf("**Task Type:** %s  \n", tc.TaskType))
	if tc.TaskCategory != "" {
		sb.WriteString(fmt.Sprintf("**Category:** %s  \n", tc.TaskCategory))
	}

	return sb.String()
}

var specFromIssueCmd = &cobra.Command{
	Use:   "from-issue <issue-number>",
	Short: "Generate spec from GitHub Issue body",
	Long: `Generate an execution spec by reading a GitHub Issue body directly.

Extracts user story, acceptance criteria, dependencies, and priority
from the issue body. Merges with SST schema definitions for context.
Includes anti-slop checklist.

Examples:
  lw spec from-issue 58
  lw spec from-issue 58 --output-dir=./specs`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issueNum := args[0]
		return generateSpecFromIssue(issueNum, specOutputDir)
	},
}

func generateSpecFromIssue(issueNum string, outDir string) error {
	// Fetch issue from GitHub
	ghCmd := exec.Command("gh", "issue", "view", issueNum,
		"--repo", defaultGHRepo,
		"--json", "number,title,body,labels,milestone,url",
	)
	out, err := ghCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh issue view failed: %w\n%s", err, string(out))
	}

	var issue ghIssue
	if err := json.Unmarshal(out, &issue); err != nil {
		return fmt.Errorf("parse issue: %w", err)
	}

	fields := parseIssueBody(issue)
	title := stripSprintPrefix(issue.Title)

	var sb strings.Builder

	// Header
	sb.WriteString("# Execution Spec\n\n")
	sb.WriteString(fmt.Sprintf("**Task:** %s  \n", title))
	if fields.taskID != "" {
		sb.WriteString(fmt.Sprintf("**Task ID:** %s  \n", fields.taskID))
	}
	sb.WriteString(fmt.Sprintf("**GitHub Issue:** #%d  \n", issue.Number))
	sb.WriteString(fmt.Sprintf("**URL:** %s  \n", issue.URL))
	if fields.priority != "" {
		sb.WriteString(fmt.Sprintf("**Priority:** %s  \n", fields.priority))
	}
	sb.WriteString(fmt.Sprintf("**Type:** %s  \n\n", fields.taskType))

	// Epic
	if fields.epic != "" {
		sb.WriteString(fmt.Sprintf("**Epic:** %s  \n\n", fields.epic))
	}

	// User Story — extract from body
	userStoryRe := regexp.MustCompile(`(?s)\*\*User Story:\*\*\s*([^\n]+(?:\n(?!\*\*)[^\n]+)*)`)
	if m := userStoryRe.FindStringSubmatch(issue.Body); len(m) >= 2 {
		sb.WriteString("## User Story\n\n")
		sb.WriteString(strings.TrimSpace(m[1]))
		sb.WriteString("\n\n")
	}

	// Description — everything between known fields
	descRe := regexp.MustCompile(`(?s)\n\n([^*][^\n]+(?:\n(?!\*\*)[^\n]+)*)`)
	if m := descRe.FindStringSubmatch(issue.Body); len(m) >= 2 {
		desc := strings.TrimSpace(m[1])
		if desc != "" && !strings.HasPrefix(desc, "Synced from") {
			sb.WriteString("## Description\n\n")
			sb.WriteString(desc)
			sb.WriteString("\n\n")
		}
	}

	// Acceptance Criteria
	if fields.acceptanceCriteria != "" {
		sb.WriteString("## Acceptance Criteria\n\n")
		for _, line := range strings.Split(fields.acceptanceCriteria, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				// Convert "- item" to "- [ ] item" for checkboxes
				if strings.HasPrefix(line, "- ") {
					sb.WriteString("- [ ] " + line[2:] + "\n")
				} else if strings.HasPrefix(line, "* ") {
					sb.WriteString("- [ ] " + line[2:] + "\n")
				} else {
					sb.WriteString("- [ ] " + line + "\n")
				}
			}
		}
		sb.WriteString("- [ ] All tests passing\n\n")
	} else {
		sb.WriteString("## Acceptance Criteria\n\n")
		sb.WriteString("- [ ] (extract from issue body)\n")
		sb.WriteString("- [ ] All tests passing\n\n")
	}

	// Dependencies
	if len(fields.deps) > 0 {
		sb.WriteString("## Dependencies\n\n")
		for _, dep := range fields.deps {
			sb.WriteString(fmt.Sprintf("- `%s`\n", dep))
		}
		sb.WriteString("\n")
	}

	// SST Schema Context
	sb.WriteString("## SST Schema Context\n\n")
	sb.WriteString("Before implementing, check relevant SST definitions:\n\n")
	sb.WriteString("```\n")
	sb.WriteString("packages/lightwave-core/lightwave/schema/definitions/__index.yaml\n")
	sb.WriteString("```\n\n")
	sb.WriteString("Key schemas to review based on task type:\n\n")
	switch fields.taskType {
	case "feature":
		sb.WriteString("- `schemas.apps` — app registry\n")
		sb.WriteString("- `schemas.cli` — CLI command definitions\n")
		sb.WriteString("- `schemas.field_options` — enum/status constraints\n")
	case "bug":
		sb.WriteString("- `schemas.field_options` — valid statuses/enum values\n")
	case "chore":
		sb.WriteString("- `schemas.cli` — CLI command definitions\n")
		sb.WriteString("- `schemas.apps` — app registry\n")
	}
	sb.WriteString("\nAlways check `__index.yaml` before hardcoding values.\n\n")

	// Anti-slop checklist
	sb.WriteString("## Anti-Slop Checklist\n\n")
	sb.WriteString("Before implementing, answer these:\n\n")
	sb.WriteString("- [ ] **What breaks if I do nothing?** If nothing breaks, don't build it.\n")
	sb.WriteString("- [ ] **Can an existing thing absorb this?** Don't create new files/models/services.\n")
	sb.WriteString("- [ ] **What can I DELETE to make this unnecessary?** Solve by subtraction first.\n")
	sb.WriteString("- [ ] **Am I automating something that should be manual?** Don't automate the unnecessary.\n")
	sb.WriteString("- [ ] **Am I optimizing something that shouldn't exist?** Simplify or delete first.\n")
	sb.WriteString("- [ ] **What existing code becomes dead if I build this?** Delete it in the same PR.\n\n")

	// Testing strategy
	sb.WriteString("## Testing Strategy\n\n")
	sb.WriteString("- [ ] Unit tests for new logic\n")
	sb.WriteString("- [ ] Integration tests with existing systems\n")
	sb.WriteString("- [ ] Pre-commit passes (ruff, prettier, detect-secrets, tests)\n")
	sb.WriteString("- [ ] No dead code left behind\n\n")

	// Expected changes
	sb.WriteString("## Expected Changes\n\n")
	switch fields.taskType {
	case "feature":
		sb.WriteString("- New models/services (if necessary)\n")
		sb.WriteString("- CLI commands\n")
		sb.WriteString("- Tests\n")
	case "bug":
		sb.WriteString("- Bug-containing module\n")
		sb.WriteString("- Related tests\n")
	case "chore":
		sb.WriteString("- Infrastructure/build files\n")
		sb.WriteString("- Configuration\n")
	}
	sb.WriteString("\n")

	// Metadata
	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("**Generated:** %s  \n", time.Now().Format("2006-01-02 15:04:05 MST")))
	sb.WriteString(fmt.Sprintf("**Source:** GitHub Issue #%d  \n", issue.Number))

	spec := sb.String()

	// Write to file
	if outDir == "" {
		outDir = "/tmp"
	}
	filename := fmt.Sprintf("spec-issue-%s.md", issueNum)
	if fields.taskID != "" {
		filename = fmt.Sprintf("spec-%s.md", fields.taskID)
	}
	specPath := filepath.Join(outDir, filename)

	if err := os.WriteFile(specPath, []byte(spec), 0644); err != nil {
		return fmt.Errorf("failed to write spec: %w", err)
	}

	fmt.Printf("Spec generated: %s\n", color.GreenString(specPath))
	fmt.Printf("Spec size: %d bytes\n", len(spec))
	return nil
}

func init() {
	specGenerateCmd.Flags().StringVar(&specOutputDir, "output-dir", "", "Output directory (defaults to /tmp)")
	specFromIssueCmd.Flags().StringVar(&specOutputDir, "output-dir", "", "Output directory (defaults to /tmp)")

	specCmd.AddCommand(specGenerateCmd)
	specCmd.AddCommand(specFromIssueCmd)
}
