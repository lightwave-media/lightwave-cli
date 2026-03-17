package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/agent"
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
	Long:  `Automated sprint orchestration - picks tasks, validates with architect, spawns Claude Code sessions.`,
}

var orchestratorStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start scrum manager orchestrator",
	Long: `Run the scrum manager loop continuously.
Every 30 minutes: check for approved tasks, validate with architect, spawn Claude Code session.

Loop sequence:
  1. Check if active coding session exists
     → If yes: poll PR status (call github monitor-pr)
     → If no: continue to step 2
  2. Get next approved task from active sprint
  3. If task found:
     - Generate spec
     - Validate with architect (5min timeout)
     - If approved: spawn Claude Code session, mark task in_progress
     - If rejected: mark task blocked, notify Joel
  4. If no tasks: idle, repeat in interval

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
	Action       string    `json:"action"` // "idle", "monitoring_pr", "spawned", "blocked"
	SpawnedAgent string    `json:"spawned_agent,omitempty"`
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

// runOrchestrationIteration executes one iteration of the orchestration loop
func runOrchestrationIteration(ctx context.Context, dryRun bool, result *iterationResult) error {
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	// Step 1: Check for active coding session
	fmt.Println(color.CyanString("Step 1: Checking for active coding session"))
	activeAgent, err := checkActiveSession()
	if err != nil {
		fmt.Printf("  %s checking sessions: %v\n", color.YellowString("Warning:"), err)
	}

	if activeAgent != "" {
		fmt.Printf("  Active session found: %s\n", color.GreenString(activeAgent))
		fmt.Println(color.CyanString("  → Switching to PR monitoring"))
		result.ActiveAgent = activeAgent
		result.Action = "monitoring_pr"
		// Find the task assigned to this agent and monitor its PR
		tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{
			Status: "in_progress",
			Limit:  10,
		})
		if err != nil {
			return fmt.Errorf("failed to query in_progress tasks: %w", err)
		}
		for _, t := range tasks {
			if t.AssignedAgent != nil && *t.AssignedAgent == activeAgent {
				result.TaskID = t.ShortID
				result.TaskTitle = t.Title
				branch := agent.BranchName(activeAgent)
				return monitorPRForAgent(ctx, pool, &t, branch, dryRun)
			}
		}
		fmt.Println("  No matching in_progress task found for agent — continuing")
	} else {
		fmt.Println("  No active session found")
	}

	// Step 2: Get next approved task
	fmt.Println(color.CyanString("Step 2: Getting next approved task"))
	tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{
		Status: "approved,next_up",
		Limit:  1,
	})
	if err != nil {
		return fmt.Errorf("task query failed: %w", err)
	}

	if len(tasks) == 0 {
		fmt.Println("  No approved tasks found - idling")
		result.Action = "idle"
		return nil
	}

	task := tasks[0]
	result.TaskID = task.ShortID
	result.TaskTitle = task.Title
	fmt.Printf("  Found task: %s (%s)\n", color.YellowString(task.ShortID), task.Title)

	// Step 3: Get task context
	tc, err := db.GetTaskContext(ctx, pool, task.ID)
	if err != nil {
		return fmt.Errorf("failed to get task context: %w", err)
	}

	// Step 4: Generate spec
	fmt.Println(color.CyanString("Step 3: Generating execution spec"))
	spec := generateSpec(tc)
	specFile := fmt.Sprintf("/tmp/spec-%s.md", task.ShortID)
	if err := os.WriteFile(specFile, []byte(spec), 0644); err != nil {
		return fmt.Errorf("failed to write spec: %w", err)
	}
	fmt.Printf("  Spec written: %s (%d bytes)\n", specFile, len(spec))

	// Step 5: Validate with architect
	fmt.Println(color.CyanString("Step 4: Validating with software architect"))
	approved, reason, err := validateWithArchitect(ctx, spec)
	if err != nil {
		fmt.Printf("  %s Architect validation failed: %v\n", color.YellowString("Warning:"), err)
		fmt.Println("  Proceeding without architect validation")
		approved = true
	}

	if !approved {
		fmt.Printf("  %s Architect rejected: %s\n", color.RedString("REJECTED"), reason)
		result.Action = "blocked"
		if !dryRun {
			status := "blocked"
			desc := fmt.Sprintf("Architect rejected: %s", reason)
			if _, err := db.UpdateTask(ctx, pool, task.ID, db.TaskUpdateOptions{
				Status:      &status,
				Description: &desc,
			}); err != nil {
				fmt.Printf("  %s Failed to update task: %v\n", color.RedString("Error:"), err)
			}
			notifyJoel(fmt.Sprintf("Task %s BLOCKED - Architect rejected: %s", task.ShortID, reason))
		}
		return nil
	}
	fmt.Println(color.GreenString("  Architect approved"))

	// Step 6: Pre-flight quality gate
	fmt.Println(color.CyanString("Step 5: Running pre-flight quality gate"))
	if err := runQualityGate(ctx); err != nil {
		fmt.Printf("  %s Quality gate failed: %v\n", color.YellowString("Warning:"), err)
		fmt.Println("  Proceeding despite quality gate failure (non-blocking)")
	} else {
		fmt.Println(color.GreenString("  Quality gate passed"))
	}

	// Step 7: Spawn Claude Code session
	fmt.Println(color.CyanString("Step 6: Spawning Claude Code session"))
	if dryRun {
		fmt.Printf("  [DRY RUN] Would spawn agent for task %s\n", task.ShortID)
		result.Action = "spawned"
		return nil
	}

	role := roleFromCategory(task.TaskCategory)
	mgr := agent.NewManager()
	spawned, err := mgr.Spawn(agent.SpawnOptions{
		Role:   role,
		Repo:   "https://github.com/lightwave-media/lightwave-platform.git",
		TaskID: task.ShortID,
		Prompt: spec,
	})
	if err != nil {
		return fmt.Errorf("failed to spawn agent: %w", err)
	}

	fmt.Printf("  Spawned agent: %s (role: %s)\n", color.GreenString(spawned.Name), role)
	result.Action = "spawned"
	result.SpawnedAgent = spawned.Name

	// Mark task in_progress with agent name and branch
	status := "in_progress"
	agentName := spawned.Name
	branch := spawned.Branch
	if _, err := db.UpdateTask(ctx, pool, task.ID, db.TaskUpdateOptions{
		Status: &status,
		Agent:  &agentName,
		Branch: &branch,
	}); err != nil {
		return fmt.Errorf("failed to mark task in_progress: %w", err)
	}
	fmt.Printf("  Marked task %s as in_progress (agent: %s)\n", color.YellowString(task.ShortID), agentName)

	// Step 8: Notify Joel
	fmt.Println(color.CyanString("Step 7: Notifying Joel"))
	notifyJoel(fmt.Sprintf("Task %s started: %s (agent: %s)", task.ShortID, task.Title, agentName))

	fmt.Println(color.GreenString("Iteration complete"))
	return nil
}

// runQualityGate runs lw check to verify codebase health before spawning a session.
func runQualityGate(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "lw", "check")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("lw check failed: %w (stderr: %s)", err, stderr.String())
	}

	output := stdout.String()
	if strings.Contains(output, "Failed") {
		return fmt.Errorf("quality checks have failures:\n%s", output)
	}

	return nil
}

// checkActiveSession checks if there's an active coding session.
// Returns the agent name if found, empty string otherwise.
func checkActiveSession() (string, error) {
	mgr := agent.NewManager()
	agents, err := mgr.List()
	if err != nil {
		return "", fmt.Errorf("list agents: %w", err)
	}
	for _, a := range agents {
		if a.State == agent.StateWorking {
			return a.Name, nil
		}
	}
	return "", nil
}

// validateWithArchitect shells out to claude CLI to validate a spec.
// Returns (approved, reason, error).
func validateWithArchitect(ctx context.Context, spec string) (bool, string, error) {
	prompt := `Review this spec for alignment with the tech stack (Django + TanStack React + Go CLI). Check for: anti-slop violations, missing acceptance criteria, scope creep. Reply APPROVED or REJECTED with reasoning.

--- SPEC ---
` + spec

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude",
		"--permission-mode", "bypassPermissions",
		"--print",
		"-p", prompt,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return false, "", fmt.Errorf("claude architect call failed: %w (stderr: %s)", err, stderr.String())
	}

	output := stdout.String()
	upper := strings.ToUpper(output)
	if strings.Contains(upper, "APPROVED") {
		return true, "", nil
	}
	// Extract rejection reason — everything after REJECTED
	reason := strings.TrimSpace(output)
	if idx := strings.Index(upper, "REJECTED"); idx >= 0 {
		reason = strings.TrimSpace(output[idx+len("REJECTED"):])
	}
	return false, reason, nil
}

// roleFromCategory maps a task category to an agent role.
func roleFromCategory(category string) agent.Role {
	switch strings.ToLower(category) {
	case "frontend":
		return agent.RoleFrontend
	case "infra", "infrastructure", "devops":
		return agent.RoleInfra
	default:
		return agent.RoleBackend
	}
}

// notifyJoel sends a notification via openclaw system event.
func notifyJoel(message string) {
	cmd := exec.Command("openclaw", "system", "event", "--text", message, "--mode", "now")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("  %s notification failed: %v (%s)\n", color.YellowString("Warning:"), err, strings.TrimSpace(string(out)))
	} else {
		fmt.Printf("  Notification sent: %s\n", message)
	}
}

// monitorPRForAgent checks PR status for an active agent's branch.
func monitorPRForAgent(ctx context.Context, pool interface{}, task *db.Task, branch string, dryRun bool) error {
	fmt.Printf("  Monitoring PR for branch: %s\n", color.CyanString(branch))

	pr, err := findPRByBranch(branch)
	if err != nil {
		fmt.Printf("  %s PR lookup: %v\n", color.YellowString("Warning:"), err)
		return nil
	}

	if pr == nil {
		fmt.Println("  No PR found yet — agent still working")
		return nil
	}

	fmt.Printf("  PR #%d: %s (%s)\n", pr.Number, pr.Title, color.CyanString(pr.State))

	if dryRun {
		fmt.Printf("  [DRY RUN] Would update task based on PR state: %s\n", pr.State)
		return nil
	}

	// Need pgxpool.Pool for db calls — use db.Connect
	dbPool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection for PR update: %w", err)
	}

	prURL := pr.URL
	if task.PRUrl == nil || *task.PRUrl != prURL {
		if _, err := db.UpdateTask(ctx, dbPool, task.ID, db.TaskUpdateOptions{
			PRUrl: &prURL,
		}); err != nil {
			fmt.Printf("  %s Failed to store PR URL: %v\n", color.RedString("Error:"), err)
		} else {
			fmt.Printf("  Stored PR URL: %s\n", prURL)
		}
	}

	switch {
	case pr.Merged:
		fmt.Println(color.GreenString("  PR merged — marking task done"))
		status := "done"
		if _, err := db.UpdateTask(ctx, dbPool, task.ID, db.TaskUpdateOptions{
			Status: &status,
		}); err != nil {
			fmt.Printf("  %s Failed to mark done: %v\n", color.RedString("Error:"), err)
		}
		// Kill agent
		if task.AssignedAgent != nil {
			mgr := agent.NewManager()
			if err := mgr.Kill(*task.AssignedAgent, false); err != nil {
				fmt.Printf("  %s Failed to kill agent: %v\n", color.YellowString("Warning:"), err)
			}
		}
		notifyJoel(fmt.Sprintf("Task %s DONE — PR #%d merged", task.ShortID, pr.Number))

	case pr.ReviewDecision == "CHANGES_REQUESTED":
		fmt.Println(color.YellowString("  Changes requested — marking task blocked"))
		status := "blocked"
		if _, err := db.UpdateTask(ctx, dbPool, task.ID, db.TaskUpdateOptions{
			Status: &status,
		}); err != nil {
			fmt.Printf("  %s Failed to mark blocked: %v\n", color.RedString("Error:"), err)
		}
		notifyJoel(fmt.Sprintf("Task %s needs changes — PR #%d has review feedback", task.ShortID, pr.Number))

	default:
		fmt.Printf("  PR state: %s, review: %s — no action needed\n", pr.State, pr.ReviewDecision)
	}

	return nil
}

// showOrchestratorStatus displays the current orchestrator state.
func showOrchestratorStatus(ctx context.Context) error {
	fmt.Println(color.GreenString("Orchestrator Status"))
	fmt.Println()

	// Active coding session
	fmt.Println(color.CyanString("Active Session:"))
	activeAgent, err := checkActiveSession()
	if err != nil {
		fmt.Printf("  %s %v\n", color.YellowString("Warning:"), err)
	} else if activeAgent != "" {
		fmt.Printf("  Agent: %s\n", color.GreenString(activeAgent))
	} else {
		fmt.Println("  None")
	}
	fmt.Println()

	// Active sprint and task count
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	sprints, err := db.ListSprints(ctx, pool, db.SprintListOptions{
		Status: "active",
		Limit:  1,
	})
	if err != nil {
		fmt.Printf("  %s Sprint query: %v\n", color.YellowString("Warning:"), err)
	} else if len(sprints) > 0 {
		sprint := sprints[0]
		fmt.Println(color.CyanString("Active Sprint:"))
		fmt.Printf("  Name: %s\n", color.YellowString(sprint.Name))

		// Count tasks in this sprint
		tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{
			SprintID: sprint.ID,
			Limit:    100,
		})
		if err == nil {
			fmt.Printf("  Tasks: %d\n", len(tasks))
		}
		fmt.Println()
	} else {
		fmt.Println(color.CyanString("Active Sprint:"))
		fmt.Println("  None")
		fmt.Println()
	}

	// Next approved task in queue
	fmt.Println(color.CyanString("Next Task in Queue:"))
	tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{
		Status: "approved,next_up",
		Limit:  1,
	})
	if err != nil {
		fmt.Printf("  %s Task query: %v\n", color.YellowString("Warning:"), err)
	} else if len(tasks) > 0 {
		t := tasks[0]
		fmt.Printf("  %s: %s (%s)\n", color.YellowString(t.ShortID), t.Title, t.StatusDisplay())
	} else {
		fmt.Println("  None — queue empty")
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
}
