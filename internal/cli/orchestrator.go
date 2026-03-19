package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/spf13/cobra"
)

var (
	orchestratorInterval string
	orchestratorDryRun   bool
)

var orchestratorCmd = &cobra.Command{
	Use:   "orchestrator",
	Short: "Scrum manager orchestration",
	Long:  `Automated sprint orchestration — delegates to Elixir orchestrator via HTTP.`,
}

var orchestratorStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start scrum manager orchestrator",
	Long: `Run the orchestration loop continuously, delegating each iteration
to the Elixir orchestrator (SprintExecutorJob + ScrumManager).

Examples:
  lw orchestrator start
  lw orchestrator start --interval=30m
  lw orchestrator start --interval=5m --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return runOrchestrator(ctx, orchestratorInterval, orchestratorDryRun)
	},
}

var orchestratorRunOnceCmd = &cobra.Command{
	Use:   "run-once",
	Short: "Execute one orchestration iteration",
	Long: `Run exactly one iteration of the orchestrator loop (for cron/testing).
Exit code 0 on success, 1 on error. Prints structured JSON output.

Examples:
  lw orchestrator run-once
  lw orchestrator run-once --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		result := &iterationResult{
			Timestamp: time.Now(),
		}
		if err := runOrchestrationIteration(ctx, orchestratorDryRun, result); err != nil {
			result.Error = err.Error()
			out, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(out))
			return err
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(out))
		return nil
	},
}

var orchestratorStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current orchestrator state",
	Long: `Display the current orchestrator state including active sprint,
coding session, and next task in queue.

Examples:
  lw orchestrator status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return showOrchestratorStatus(ctx)
	},
}

// iterationResult captures the outcome of a single orchestration iteration for structured output.
type iterationResult struct {
	Timestamp    time.Time `json:"timestamp"`
	ActiveAgent  string    `json:"active_agent,omitempty"`
	TaskID       string    `json:"task_id,omitempty"`
	TaskTitle    string    `json:"task_title,omitempty"`
	SessionType  string    `json:"session_type,omitempty"`
	Action       string    `json:"action"` // "idle", "monitoring_pr", "spawned", "blocked"
	SpawnedAgent string    `json:"spawned_agent,omitempty"`
	SprintAction string    `json:"sprint_action,omitempty"` // "sprint_completed", "sprint_started", "sprint_planned"
	SprintID     string    `json:"sprint_id,omitempty"`
	SprintName   string    `json:"sprint_name,omitempty"`
	TasksDone    int       `json:"tasks_done,omitempty"`
	TasksTotal   int       `json:"tasks_total,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// runOrchestrator executes the main orchestration loop
func runOrchestrator(ctx context.Context, intervalStr string, dryRun bool) error {
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return fmt.Errorf("invalid interval: %w", err)
	}

	fmt.Println(color.GreenString("Scrum Manager Orchestrator Starting"))
	fmt.Printf("Interval: %s\n", color.CyanString(interval.String()))
	if dryRun {
		fmt.Println(color.YellowString("DRY RUN MODE - no changes will be made"))
	}
	fmt.Println()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	iteration := 0
	for {
		iteration++
		fmt.Printf("[%s] Iteration %d\n", time.Now().Format("2006-01-02 15:04:05"), iteration)

		result := &iterationResult{Timestamp: time.Now()}
		if err := runOrchestrationIteration(ctx, dryRun, result); err != nil {
			fmt.Printf("%s %v\n", color.RedString("Error:"), err)
		}

		fmt.Println()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// runOrchestrationIteration syncs GitHub Issues then delegates to the Elixir orchestrator via HTTP.
func runOrchestrationIteration(ctx context.Context, dryRun bool, result *iterationResult) error {
	// Step 0: Sync GitHub Issues → lw task DB (background cache, non-critical)
	fmt.Println(color.CyanString("Step 0: DB cache sync (non-critical)"))
	if err := runGitHubSync(ctx, dryRun, false, false); err != nil {
		fmt.Printf("  %s db cache sync: %v (continuing — GitHub Issues is source of truth)\n", color.YellowString("Info:"), err)
	}
	fmt.Println()

	// Step 1: Pick next ready issue from GitHub (priority-sorted, dep-checked)
	fmt.Println(color.CyanString("Step 1: GitHub Issues pick"))
	picked, pickErr := pickNextReady(ctx, "")
	if pickErr != nil {
		fmt.Printf("  %s github pick: %v (continuing)\n", color.YellowString("Warning:"), pickErr)
	} else if picked == nil {
		fmt.Println("  No actionable ready issues")
	} else {
		fields := parseIssueBody(*picked)
		title := stripSprintPrefix(picked.Title)
		fmt.Printf("  Next: #%d %s", picked.Number, truncate(title, 45))
		if fields.priority != "" {
			fmt.Printf(" [%s]", fields.priority)
		}
		fmt.Println()
		result.TaskTitle = title
		if fields.taskID != "" {
			result.TaskID = fields.taskID
		}
		sessionType := inferSessionTypeFromIssue(*picked)
		result.SessionType = sessionType.String()
		fmt.Printf("  Session type: %s (%s)\n", color.CyanString(sessionType.String()), sessionType.WorkingDir())
	}
	fmt.Println()

	// Step 2: Architect validation (if we picked an issue)
	if picked != nil {
		issueNum := fmt.Sprintf("%d", picked.Number)
		fmt.Println(color.CyanString("Step 2: Architect validation"))
		valResult, valErr := validateIssueSpec(ctx, issueNum, dryRun)
		if valErr != nil {
			fmt.Printf("  %s validation: %v (continuing)\n", color.YellowString("Warning:"), valErr)
		} else if !valResult.passed {
			fmt.Println("  Issue failed validation — skipping spawn")
			result.Action = "blocked"
		}
		fmt.Println()
	}

	// Step 3: Sync task statuses to GitHub Projects board
	fmt.Println(color.CyanString("Step 3: GitHub Projects sync"))
	if err := runProjectsSync(ctx, dryRun); err != nil {
		fmt.Printf("  %s projects sync: %v (continuing)\n", color.YellowString("Warning:"), err)
	}
	fmt.Println()

	// Step 4: Sweep merged PRs — close issues + move Projects cards
	fmt.Println(color.CyanString("Step 4: Sweep merged PRs"))
	if err := runSweepMerged(ctx, dryRun); err != nil {
		fmt.Printf("  %s sweep merged: %v (continuing)\n", color.YellowString("Warning:"), err)
	}
	fmt.Println()

	// Step 5: Sprint lifecycle — auto-complete/start/plan sprints
	fmt.Println(color.CyanString("Step 5: Sprint lifecycle"))
	if err := sprintLifecycleCheck(ctx, dryRun, result); err != nil {
		fmt.Printf("  %s sprint lifecycle: %v (continuing)\n", color.YellowString("Warning:"), err)
	}
	fmt.Println()

	// Skip Elixir delegation if no issue picked or validation blocked
	if picked == nil || result.Action == "blocked" {
		if result.Action == "" {
			result.Action = "idle"
		}
		fmt.Println(color.GreenString("Iteration complete"))
		return nil
	}

	if dryRun {
		fmt.Println(color.YellowString("[DRY RUN] Would call Elixir orchestrator run-once"))
		result.Action = "idle"
		return nil
	}

	fmt.Println(color.CyanString("Delegating to Elixir orchestrator..."))

	endpoint, err := orchestratorEndpoint("/api/orchestrator/run-once")
	if err != nil {
		return fmt.Errorf("orchestrator endpoint: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	setServiceToken(req.Header)

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("orchestrator request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("orchestrator returned %d: %s", resp.StatusCode, string(body))
	}

	var respData struct {
		Action        string `json:"action"`
		SprintID      string `json:"sprint_id"`
		CurrentTaskID string `json:"current_task_id"`
		Message       string `json:"message"`
		Error         string `json:"error"`
	}
	if err := json.Unmarshal(body, &respData); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	result.Action = respData.Action
	result.TaskID = respData.CurrentTaskID

	switch respData.Action {
	case "spawned":
		fmt.Printf("  %s Task %s started via Elixir\n", color.GreenString("SPAWNED"), respData.CurrentTaskID)
	case "idle":
		fmt.Println("  No tasks to execute — idle")
	case "busy":
		fmt.Printf("  %s %s\n", color.YellowString("BUSY"), respData.Message)
	default:
		fmt.Printf("  Action: %s\n", respData.Action)
	}

	fmt.Println(color.GreenString("Iteration complete"))
	return nil
}

// notifyJoel logs a notification message. Notifications are handled by Elixir PubSub.
func notifyJoel(message string) {
	fmt.Printf("  Notification: %s\n", message)
}

// showOrchestratorStatus queries the Elixir orchestrator for current state.
func showOrchestratorStatus(ctx context.Context) error {
	fmt.Println(color.GreenString("Orchestrator Status"))
	fmt.Println()

	endpoint, err := orchestratorEndpoint("/api/orchestrator/status")
	if err != nil {
		return fmt.Errorf("orchestrator endpoint: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	setServiceToken(req.Header)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("orchestrator request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("orchestrator returned %d: %s", resp.StatusCode, string(body))
	}

	var status struct {
		State            string `json:"state"`
		SprintID         string `json:"sprint_id"`
		CurrentTaskID    string `json:"current_task_id"`
		CurrentTaskTitle string `json:"current_task_title"`
		QueueDepth       int    `json:"queue_depth"`
		Idle             bool   `json:"idle"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	fmt.Println(color.CyanString("State:"))
	fmt.Printf("  %s\n", status.State)
	fmt.Println()

	if status.SprintID != "" {
		fmt.Println(color.CyanString("Active Sprint:"))
		fmt.Printf("  ID: %s\n", color.YellowString(status.SprintID))
		fmt.Println()
	}

	if status.CurrentTaskID != "" {
		fmt.Println(color.CyanString("Current Task:"))
		fmt.Printf("  %s: %s\n", color.YellowString(status.CurrentTaskID), status.CurrentTaskTitle)
		fmt.Println()
	}

	fmt.Println(color.CyanString("Queue:"))
	fmt.Printf("  Depth: %d\n", status.QueueDepth)
	fmt.Printf("  Idle: %v\n", status.Idle)

	return nil
}

// sprintLifecycleCheck manages the automated sprint lifecycle:
// 1. Active sprint with all tasks done → complete it
// 2. No active sprint + planned sprint exists → activate it
// 3. No active/planned sprint + approved tasks exist → auto-plan a new sprint
func sprintLifecycleCheck(ctx context.Context, dryRun bool, result *iterationResult) error {
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection: %w", err)
	}

	// Check for active sprint
	activeSprints, err := db.ListSprints(ctx, pool, db.SprintListOptions{Status: "active", Limit: 1})
	if err != nil {
		return fmt.Errorf("listing active sprints: %w", err)
	}

	if len(activeSprints) > 0 {
		s := activeSprints[0]
		result.SprintID = s.ShortID
		result.SprintName = s.Name

		tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{SprintID: s.ShortID, Limit: 100})
		if err != nil {
			return fmt.Errorf("listing sprint tasks: %w", err)
		}

		done := 0
		for _, t := range tasks {
			if t.Status == "done" {
				done++
			}
		}
		result.TasksDone = done
		result.TasksTotal = len(tasks)

		if done == len(tasks) && len(tasks) > 0 {
			fmt.Printf("  Sprint %s: all %d tasks done\n", color.CyanString(s.Name), done)
			if dryRun {
				fmt.Printf("  [DRY RUN] Would complete sprint %s\n", s.ShortID)
				result.SprintAction = "sprint_completed"
				return nil
			}
			today := time.Now().Format("2006-01-02")
			completed := "completed"
			_, err = db.UpdateSprint(ctx, pool, s.ShortID, db.SprintUpdateOptions{
				Status:  &completed,
				EndDate: &today,
			})
			if err != nil {
				return fmt.Errorf("completing sprint: %w", err)
			}

			// Close linked GitHub Issues and sync Projects board
			for _, t := range tasks {
				if t.Status == "done" {
					if issueNum := taskIssueNumber(ctx, &t); issueNum > 0 {
						closeLinkedIssue(issueNum)
						syncProjectStatus(issueNum, "done")
					}
				}
			}

			specPath, _, specErr := FindSprintSpec(s.ShortID)
			if specErr == nil {
				MoveSpec(specPath, "done")
			}
			fmt.Printf("  %s Sprint %s completed\n", color.GreenString("✓"), s.ShortID)
			result.SprintAction = "sprint_completed"
		} else {
			fmt.Printf("  Sprint %s: %d/%d tasks done — continuing\n", color.CyanString(s.Name), done, len(tasks))
		}
		return nil
	}

	// No active sprint — check for planned
	plannedSprints, err := db.ListSprints(ctx, pool, db.SprintListOptions{Status: "planned", Limit: 1})
	if err != nil {
		return fmt.Errorf("listing planned sprints: %w", err)
	}

	if len(plannedSprints) > 0 {
		s := plannedSprints[0]
		result.SprintID = s.ShortID
		result.SprintName = s.Name
		fmt.Printf("  Planned sprint found: %s (%s)\n", color.CyanString(s.Name), s.ShortID)
		if dryRun {
			fmt.Printf("  [DRY RUN] Would start sprint %s\n", s.ShortID)
			result.SprintAction = "sprint_started"
			return nil
		}
		today := time.Now().Format("2006-01-02")
		active := "active"
		_, err = db.UpdateSprint(ctx, pool, s.ShortID, db.SprintUpdateOptions{
			Status:    &active,
			StartDate: &today,
		})
		if err != nil {
			return fmt.Errorf("activating sprint: %w", err)
		}
		sprint, _ := db.GetSprint(ctx, pool, s.ShortID)
		if sprint != nil {
			if syncErr := syncSprintToGitHub(ctx, pool, sprint); syncErr != nil {
				fmt.Printf("  %s GitHub sync: %v\n", color.YellowString("Warning:"), syncErr)
			}
		}
		fmt.Printf("  %s Sprint %s activated\n", color.GreenString("✓"), s.ShortID)
		result.SprintAction = "sprint_started"
		return nil
	}

	// No planned sprint — check for approved tasks without a sprint
	tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{Status: "approved", Limit: 100})
	if err != nil {
		return fmt.Errorf("listing approved tasks: %w", err)
	}

	// Group unsprinted tasks by epic
	epicTasks := map[string][]db.Task{}
	for _, t := range tasks {
		if t.SprintID == nil && t.EpicID != nil {
			epicTasks[*t.EpicID] = append(epicTasks[*t.EpicID], t)
		}
	}

	if len(epicTasks) == 0 {
		fmt.Println("  No approved tasks without a sprint — idle")
		return nil
	}

	// Pick the epic with the most unsprinted tasks
	var bestEpicID string
	var bestCount int
	for epicID, epTasks := range epicTasks {
		if len(epTasks) > bestCount {
			bestEpicID = epicID
			bestCount = len(epTasks)
		}
	}

	epicShort := bestEpicID
	if len(epicShort) > 8 {
		epicShort = epicShort[:8]
	}

	fmt.Printf("  Found %d approved tasks for epic %s\n", bestCount, epicShort)

	if dryRun {
		fmt.Printf("  [DRY RUN] Would auto-plan sprint for epic %s\n", epicShort)
		result.SprintAction = "sprint_planned"
		return nil
	}

	sprint, count, err := autoPlanSprint(ctx, pool, epicShort, "", false)
	if err != nil {
		return fmt.Errorf("auto-planning sprint: %w", err)
	}
	if sprint != nil {
		fmt.Printf("  %s Created %s with %d tasks\n", color.GreenString("✓"), sprint.Name, count)
		result.SprintAction = "sprint_planned"
	}

	return nil
}

var orchestratorE2ECmd = &cobra.Command{
	Use:   "e2e-test",
	Short: "Run end-to-end test of the orchestration pipeline",
	Long: `Creates a test GitHub Issue, runs the pipeline steps (pick, validate, spec),
verifies each step, then cleans up. Tests the controllable pipeline stages.

Examples:
  lw orchestrator e2e-test`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return runE2ETest(ctx)
	},
}

func runE2ETest(ctx context.Context) error {
	start := time.Now()
	var passed, failed int

	fmt.Println(color.CyanString("E2E Pipeline Test"))
	fmt.Println()

	// Step 1: Create test issue
	fmt.Println(color.CyanString("Step 1: Create test issue"))
	testBody := `**Task ID:** e2e00000
**Priority:** P1 Urgent
**Type:** chore

E2E test issue — auto-generated, will be cleaned up.

**Acceptance Criteria:**
- Pipeline picks this issue
- Spec generates correctly
- Validation passes

**Dependencies:** None`

	createCmd := exec.Command("gh", "issue", "create",
		"--repo", defaultGHRepo,
		"--title", "[E2E Test] Pipeline verification",
		"--body", testBody,
		"--label", "ready,p1,enhancement",
	)
	createOut, err := createCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create test issue: %w\n%s", err, string(createOut))
	}

	// Extract issue number from URL
	issueURL := strings.TrimSpace(string(createOut))
	parts := strings.Split(issueURL, "/")
	issueNum := parts[len(parts)-1]
	fmt.Printf("  Created test issue #%s\n", issueNum)
	passed++

	// Cleanup function
	defer func() {
		fmt.Println()
		fmt.Println(color.CyanString("Cleanup"))
		cleanCmd := exec.Command("gh", "issue", "close", issueNum,
			"--repo", defaultGHRepo, "--reason", "not planned")
		cleanCmd.Run()
		delCmd := exec.Command("gh", "issue", "delete", issueNum,
			"--repo", defaultGHRepo, "--yes")
		delCmd.Run()
		fmt.Printf("  Cleaned up test issue #%s\n", issueNum)
	}()

	fmt.Println()

	// Step 2: Pick — verify the test issue is picked
	fmt.Println(color.CyanString("Step 2: Verify picker finds test issue"))
	picked, pickErr := pickNextReady(ctx, "")
	if pickErr != nil {
		fmt.Printf("  %s pick failed: %v\n", color.RedString("FAIL"), pickErr)
		failed++
	} else if picked == nil {
		fmt.Printf("  %s no issue picked\n", color.RedString("FAIL"))
		failed++
	} else {
		fmt.Printf("  %s picked #%d: %s\n", color.GreenString("PASS"), picked.Number, picked.Title)
		passed++
	}
	fmt.Println()

	// Step 3: Validate spec
	fmt.Println(color.CyanString("Step 3: Architect validation"))
	valResult, valErr := validateIssueSpec(ctx, issueNum, true)
	if valErr != nil {
		fmt.Printf("  %s validation error: %v\n", color.RedString("FAIL"), valErr)
		failed++
	} else if !valResult.passed {
		fmt.Printf("  %s validation failed: %v\n", color.RedString("FAIL"), valResult.failures)
		failed++
	} else {
		fmt.Printf("  %s validation passed\n", color.GreenString("PASS"))
		passed++
	}
	fmt.Println()

	// Step 4: Generate spec
	fmt.Println(color.CyanString("Step 4: Spec generation"))
	specErr := generateSpecFromIssue(issueNum, "/tmp")
	if specErr != nil {
		fmt.Printf("  %s spec generation failed: %v\n", color.RedString("FAIL"), specErr)
		failed++
	} else {
		specPath := fmt.Sprintf("/tmp/spec-e2e00000.md")
		if _, err := os.Stat(specPath); err == nil {
			fmt.Printf("  %s spec generated: %s\n", color.GreenString("PASS"), specPath)
			passed++
			// Cleanup spec file
			os.Remove(specPath)
		} else {
			fmt.Printf("  %s spec file not found\n", color.RedString("FAIL"))
			failed++
		}
	}
	fmt.Println()

	// Step 5: Verify branch name generation
	fmt.Println(color.CyanString("Step 5: Branch name generation"))
	var testIssueNum int
	fmt.Sscanf(issueNum, "%d", &testIssueNum)
	branchName := IssueBranchName(testIssueNum, "[E2E Test] Pipeline verification", "chore")
	if strings.HasPrefix(branchName, "chore/issue-") {
		fmt.Printf("  %s branch: %s\n", color.GreenString("PASS"), branchName)
		passed++
	} else {
		fmt.Printf("  %s unexpected branch: %s\n", color.RedString("FAIL"), branchName)
		failed++
	}
	fmt.Println()

	// Step 6: Verify session type inference
	fmt.Println(color.CyanString("Step 6: Session type inference"))
	sessionType := InferSessionType([]string{"ready", "p1", "enhancement"})
	if sessionType == SessionBackend {
		fmt.Printf("  %s session type: %s (correct default)\n", color.GreenString("PASS"), sessionType)
		passed++
	} else {
		fmt.Printf("  %s unexpected session type: %s\n", color.RedString("FAIL"), sessionType)
		failed++
	}

	// Summary
	elapsed := time.Since(start)
	fmt.Println()
	fmt.Println(color.CyanString("E2E Test Results"))
	fmt.Printf("  Passed: %s\n", color.GreenString("%d", passed))
	if failed > 0 {
		fmt.Printf("  Failed: %s\n", color.RedString("%d", failed))
	} else {
		fmt.Printf("  Failed: %d\n", failed)
	}
	fmt.Printf("  Duration: %s\n", elapsed.Round(time.Millisecond))

	if failed > 0 {
		return fmt.Errorf("%d test(s) failed", failed)
	}
	return nil
}

func init() {
	orchestratorStartCmd.Flags().StringVar(&orchestratorInterval, "interval", "30m", "Loop interval (e.g. 30m, 5m)")
	orchestratorStartCmd.Flags().BoolVar(&orchestratorDryRun, "dry-run", false, "Dry run mode - don't make actual changes")
	orchestratorRunOnceCmd.Flags().BoolVar(&orchestratorDryRun, "dry-run", false, "Dry run mode - don't make actual changes")

	orchestratorCmd.AddCommand(orchestratorStartCmd)
	orchestratorCmd.AddCommand(orchestratorRunOnceCmd)
	orchestratorCmd.AddCommand(orchestratorStatusCmd)
	orchestratorCmd.AddCommand(orchestratorE2ECmd)
}
