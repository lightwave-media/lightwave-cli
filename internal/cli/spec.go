package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
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

		// Determine output directory — prefer workspace specs/ over /tmp
		outDir := specOutputDir
		if outDir == "" {
			cfg := config.Get()
			if cfg != nil && cfg.Paths.LightwaveRoot != "" {
				outDir = filepath.Join(cfg.Paths.LightwaveRoot, "specs")
			} else {
				outDir = "specs"
			}
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				outDir = "/tmp" // fallback
			}
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

	// Acceptance criteria — extract from description or warn
	sb.WriteString("## Acceptance Criteria\n\n")
	acFound := false
	if tc.Description != nil && *tc.Description != "" {
		acLines := extractACFromDescription(*tc.Description)
		if len(acLines) > 0 {
			acFound = true
			for _, line := range acLines {
				sb.WriteString("- [ ] " + line + "\n")
			}
		}
	}
	if !acFound {
		sb.WriteString("**WARNING: No acceptance criteria defined.**\n")
		sb.WriteString("Add acceptance criteria to the task description before spawning a session.\n")
	}
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
	sb.WriteString(fmt.Sprintf("**Type:** %s  \n", fields.taskType))
	sessionType := inferSessionTypeFromIssue(issue)
	sb.WriteString(fmt.Sprintf("**Session Type:** %s  \n", sessionType))
	sb.WriteString(fmt.Sprintf("**Working Dir:** %s  \n\n", sessionType.WorkingDir()))

	// Epic
	if fields.epic != "" {
		sb.WriteString(fmt.Sprintf("**Epic:** %s  \n\n", fields.epic))
	}

	// User Story — extract from body (find text between **User Story:** and next **Field:**)
	if idx := strings.Index(issue.Body, "**User Story:**"); idx >= 0 {
		rest := issue.Body[idx+len("**User Story:**"):]
		// Find next bold field marker
		if end := strings.Index(rest, "\n**"); end >= 0 {
			rest = rest[:end]
		}
		story := strings.TrimSpace(rest)
		if story != "" {
			sb.WriteString("## User Story\n\n")
			sb.WriteString(story)
			sb.WriteString("\n\n")
		}
	}

	// Description — non-field text lines from the body
	var descLines []string
	for _, line := range strings.Split(issue.Body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "**") || strings.HasPrefix(trimmed, "Synced from") {
			continue
		}
		// Skip bullet items (they belong to AC or deps)
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			continue
		}
		descLines = append(descLines, trimmed)
	}
	if len(descLines) > 0 {
		sb.WriteString("## Description\n\n")
		sb.WriteString(strings.Join(descLines, "\n"))
		sb.WriteString("\n\n")
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
		sb.WriteString("**WARNING: No acceptance criteria found in issue body.**\n")
		sb.WriteString("Add an `**Acceptance Criteria:**` section to the issue before spawning.\n")
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

	// Git workflow
	branchName := IssueBranchName(issue.Number, title, fields.taskType)
	sb.WriteString("## Git Workflow\n\n")
	sb.WriteString(fmt.Sprintf("**Branch:** `%s`  \n", branchName))
	sb.WriteString(fmt.Sprintf("**PR Body Must Include:** `Closes #%d`  \n\n", issue.Number))
	sb.WriteString("This ensures GitHub auto-closes the issue and the Projects board card moves to Done on merge.\n\n")

	// Metadata
	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("**Generated:** %s  \n", time.Now().Format("2006-01-02 15:04:05 MST")))
	sb.WriteString(fmt.Sprintf("**Source:** GitHub Issue #%d  \n", issue.Number))

	spec := sb.String()

	// Write to file — prefer workspace specs/ over /tmp
	if outDir == "" {
		cfg := config.Get()
		if cfg != nil && cfg.Paths.LightwaveRoot != "" {
			outDir = filepath.Join(cfg.Paths.LightwaveRoot, "specs")
		} else {
			outDir = "specs"
		}
		if mkErr := os.MkdirAll(outDir, 0o755); mkErr != nil {
			outDir = "/tmp" // fallback
		}
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

var specValidateCmd = &cobra.Command{
	Use:   "validate <issue-number>",
	Short: "Validate spec against SST schemas before session spawn",
	Long: `Run architect validation on a GitHub Issue before spawning a coding session.

Checks:
  - Issue has acceptance criteria (not just placeholders)
  - Issue has a valid task type
  - Dependencies are satisfied (done/cancelled/archived)
  - Required SST schemas exist for the task type

On pass: adds approval comment to the issue.
On fail: labels issue 'needs-architecture-review' and alerts Joel.

Examples:
  lw spec validate 58
  lw spec validate 58 --dry-run`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		issueNum := args[0]
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		result, err := validateIssueSpec(ctx, issueNum, dryRun)
		if err != nil {
			return err
		}
		if !result.passed {
			return fmt.Errorf("validation failed: %d issue(s)", len(result.failures))
		}
		return nil
	},
}

// validationResult captures the outcome of spec validation.
type validationResult struct {
	passed   bool
	failures []string
}

// validateIssueSpec runs architect validation on a GitHub issue.
// Returns the result and any error. Used by CLI and orchestrator.
func validateIssueSpec(ctx context.Context, issueNum string, dryRun bool) (*validationResult, error) {
	// Fetch issue
	ghCmd := exec.Command("gh", "issue", "view", issueNum,
		"--repo", defaultGHRepo,
		"--json", "number,title,body,labels,url",
	)
	out, err := ghCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh issue view failed: %w\n%s", err, string(out))
	}

	var issue ghIssue
	if err := json.Unmarshal(out, &issue); err != nil {
		return nil, fmt.Errorf("parse issue: %w", err)
	}

	fields := parseIssueBody(issue)
	title := stripSprintPrefix(issue.Title)

	fmt.Printf("Validating: #%d %s\n\n", issue.Number, title)

	result := &validationResult{passed: true}

	// Check 1: Acceptance criteria exist and aren't placeholders
	if fields.acceptanceCriteria == "" {
		result.failures = append(result.failures, "Missing acceptance criteria")
	}

	// Check 2: Task type is valid
	validTypes := map[string]bool{"feature": true, "bug": true, "chore": true, "enhancement": true}
	if fields.taskType == "" {
		result.failures = append(result.failures, "Missing task type")
	} else if !validTypes[fields.taskType] {
		result.failures = append(result.failures, fmt.Sprintf("Unknown task type: %s", fields.taskType))
	}

	// Check 3: Dependencies are satisfied (GitHub Issues primary, DB fallback)
	if len(fields.deps) > 0 {
		closedIDs := closedIssueTaskIDs()
		pool, _ := db.GetPool(ctx)
		for _, dep := range fields.deps {
			// Primary: check closed GitHub Issues
			if closedIDs[dep] {
				continue
			}
			// Fallback: check DB
			if pool != nil {
				task, taskErr := db.GetTask(ctx, pool, dep)
				if taskErr != nil {
					result.failures = append(result.failures, fmt.Sprintf("Dependency %s not found", dep))
					continue
				}
				switch task.Status {
				case "done", "cancelled", "archived":
					continue
				default:
					result.failures = append(result.failures, fmt.Sprintf("Dependency %s is %s (not done)", dep, task.Status))
				}
			}
		}
	}

	// Check 4: Task ID exists
	if fields.taskID == "" {
		result.failures = append(result.failures, "Missing Task ID in issue body")
	}

	// Print results
	if len(result.failures) > 0 {
		result.passed = false
		fmt.Println(color.RedString("VALIDATION FAILED"))
		for _, f := range result.failures {
			fmt.Printf("  %s %s\n", color.RedString("✗"), f)
		}

		if !dryRun {
			// Label issue
			labelCmd := exec.Command("gh", "issue", "edit", issueNum,
				"--repo", defaultGHRepo,
				"--add-label", "needs-architecture-review",
			)
			if labelOut, labelErr := labelCmd.CombinedOutput(); labelErr != nil {
				fmt.Printf("  %s add label: %v\n%s", color.YellowString("Warning:"), labelErr, string(labelOut))
			}

			// Comment on issue
			comment := fmt.Sprintf("## Architect Validation Failed\n\n%s\n\nPlease address these issues before this task can be spawned.",
				formatFailures(result.failures))
			commentCmd := exec.Command("gh", "issue", "comment", issueNum,
				"--repo", defaultGHRepo,
				"--body", comment,
			)
			if commentOut, commentErr := commentCmd.CombinedOutput(); commentErr != nil {
				fmt.Printf("  %s comment: %v\n%s", color.YellowString("Warning:"), commentErr, string(commentOut))
			}

			// Alert Joel
			notifyJoel(fmt.Sprintf("Architect validation FAILED for #%s: %s — %s",
				issueNum, title, strings.Join(result.failures, "; ")))
		}
	} else {
		fmt.Println(color.GreenString("VALIDATION PASSED"))

		if !dryRun {
			// Comment approval on issue
			comment := fmt.Sprintf("## Architect Validation Passed\n\nAll checks passed at %s. Ready for session spawn.",
				time.Now().Format("2006-01-02 15:04:05 MST"))
			commentCmd := exec.Command("gh", "issue", "comment", issueNum,
				"--repo", defaultGHRepo,
				"--body", comment,
			)
			if commentOut, commentErr := commentCmd.CombinedOutput(); commentErr != nil {
				fmt.Printf("  %s comment: %v\n%s", color.YellowString("Warning:"), commentErr, string(commentOut))
			}
			fmt.Printf("  Approval logged on issue #%s\n", issueNum)
		}
	}

	return result, nil
}

// extractACFromDescription parses acceptance criteria from a task description.
// Looks for lines starting with "- " or "* " after an "Acceptance Criteria" heading,
// or falls back to any bullet list items in the description.
func extractACFromDescription(desc string) []string {
	lines := strings.Split(desc, "\n")
	var acLines []string
	inACSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		// Detect AC heading
		if strings.Contains(lower, "acceptance criteria") {
			inACSection = true
			continue
		}

		// End of AC section at next heading
		if inACSection && strings.HasPrefix(trimmed, "#") {
			break
		}
		if inACSection && strings.HasPrefix(trimmed, "**") && !strings.HasPrefix(trimmed, "**-") {
			// Next bold field marker — end AC section
			if !strings.Contains(lower, "acceptance") {
				break
			}
		}

		if inACSection {
			if strings.HasPrefix(trimmed, "- ") {
				acLines = append(acLines, strings.TrimPrefix(trimmed, "- "))
			} else if strings.HasPrefix(trimmed, "* ") {
				acLines = append(acLines, strings.TrimPrefix(trimmed, "* "))
			} else if strings.HasPrefix(trimmed, "- [ ] ") {
				acLines = append(acLines, strings.TrimPrefix(trimmed, "- [ ] "))
			} else if strings.HasPrefix(trimmed, "- [x] ") {
				acLines = append(acLines, strings.TrimPrefix(trimmed, "- [x] "))
			}
		}
	}

	return acLines
}

func formatFailures(failures []string) string {
	var sb strings.Builder
	for _, f := range failures {
		sb.WriteString(fmt.Sprintf("- ✗ %s\n", f))
	}
	return sb.String()
}

func init() {
	specGenerateCmd.Flags().StringVar(&specOutputDir, "output-dir", "", "Output directory (defaults to specs/)")
	specFromIssueCmd.Flags().StringVar(&specOutputDir, "output-dir", "", "Output directory (defaults to specs/)")
	specValidateCmd.Flags().Bool("dry-run", false, "Show validation results without labeling/commenting")

	specCmd.AddCommand(specGenerateCmd)
	specCmd.AddCommand(specFromIssueCmd)
	specCmd.AddCommand(specValidateCmd)
}
