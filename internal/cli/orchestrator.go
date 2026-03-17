package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/fatih/color"
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

// runOrchestrationIteration delegates to the Elixir orchestrator via HTTP.
func runOrchestrationIteration(ctx context.Context, dryRun bool, result *iterationResult) error {
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

func init() {
	orchestratorStartCmd.Flags().StringVar(&orchestratorInterval, "interval", "30m", "Loop interval (e.g. 30m, 5m)")
	orchestratorStartCmd.Flags().BoolVar(&orchestratorDryRun, "dry-run", false, "Dry run mode - don't make actual changes")
	orchestratorRunOnceCmd.Flags().BoolVar(&orchestratorDryRun, "dry-run", false, "Dry run mode - don't make actual changes")

	orchestratorCmd.AddCommand(orchestratorStartCmd)
	orchestratorCmd.AddCommand(orchestratorRunOnceCmd)
	orchestratorCmd.AddCommand(orchestratorStatusCmd)
}
