package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/agent"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/spf13/cobra"
)

var (
	githubMonitorTimeout int
)

const defaultGHRepo = "lightwave-media/lightwave-platform"

var githubCmd = &cobra.Command{
	Use:   "github",
	Short: "GitHub integration commands",
	Long:  `Monitor and integrate with GitHub PRs for task orchestration.`,
}

var githubMonitorPRCmd = &cobra.Command{
	Use:   "monitor-pr <task-id>",
	Short: "Monitor PR status and update task",
	Long: `Poll GitHub for PR matching task ID and update task status.
Watches PR lifecycle: draft → ready_for_review → approved → merged.

Updates task: in_review on PR detect, done on merge, blocked on request changes.

Examples:
  lw github monitor-pr abc123
  lw github monitor-pr abc123 --timeout=300`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		task, err := db.GetTask(ctx, pool, taskID)
		if err != nil {
			return err
		}

		// Determine branch to search for
		var branch string
		if task.AssignedAgent != nil && *task.AssignedAgent != "" {
			branch = agent.BranchName(*task.AssignedAgent)
		} else if task.BranchName != nil && *task.BranchName != "" {
			branch = *task.BranchName
		}

		if task.PRUrl != nil && *task.PRUrl != "" {
			return monitorExistingPR(ctx, pool, task)
		}

		fmt.Println(color.YellowString("Searching for PR..."))
		return monitorNewPR(ctx, pool, task, branch, githubMonitorTimeout)
	},
}

// ghPR represents a PR returned by gh pr list --json
type ghPR struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	State          string `json:"state"`
	HeadRefName    string `json:"headRefName"`
	Mergeable      string `json:"mergeable"`
	ReviewDecision string `json:"reviewDecision"`
	URL            string `json:"url"`
	Merged         bool   `json:"merged"`
	MergedAt       string `json:"mergedAt"`
}

// findPRByBranch uses gh CLI to find a PR matching the given branch name.
func findPRByBranch(branch string) (*ghPR, error) {
	cmd := exec.Command("gh", "pr", "list",
		"--repo", defaultGHRepo,
		"--head", branch,
		"--state", "all",
		"--json", "number,title,state,headRefName,mergeable,reviewDecision,url,merged,mergedAt",
		"--limit", "1",
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list failed: %w", err)
	}

	var prs []ghPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}

	if len(prs) == 0 {
		return nil, nil
	}

	return &prs[0], nil
}

// findPRByNumber uses gh CLI to get a specific PR by number.
func findPRByNumber(prNumber int) (*ghPR, error) {
	cmd := exec.Command("gh", "pr", "view",
		fmt.Sprintf("%d", prNumber),
		"--repo", defaultGHRepo,
		"--json", "number,title,state,headRefName,mergeable,reviewDecision,url,merged,mergedAt",
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr view failed: %w", err)
	}

	var pr ghPR
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}

	return &pr, nil
}

// extractPRNumber extracts the PR number from a GitHub PR URL.
func extractPRNumber(prURL string) (int, error) {
	// https://github.com/owner/repo/pull/123
	parts := strings.Split(prURL, "/")
	if len(parts) < 7 {
		return 0, fmt.Errorf("invalid PR URL format: %s", prURL)
	}
	var num int
	if _, err := fmt.Sscanf(parts[len(parts)-1], "%d", &num); err != nil {
		return 0, fmt.Errorf("invalid PR number in URL: %s", prURL)
	}
	return num, nil
}

// monitorExistingPR polls an existing PR until it's merged, changes are requested, or timeout.
func monitorExistingPR(ctx context.Context, pool interface{}, task *db.Task) error {
	if task.PRUrl == nil || *task.PRUrl == "" {
		return fmt.Errorf("task has no PR URL")
	}

	prURL := *task.PRUrl
	prNumber, err := extractPRNumber(prURL)
	if err != nil {
		return err
	}

	fmt.Printf("Monitoring PR #%d: %s\n", prNumber, color.BlueString(prURL))

	deadline := time.Now().Add(time.Duration(githubMonitorTimeout) * time.Second)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// Check immediately, then on each tick
	for {
		pr, err := findPRByNumber(prNumber)
		if err != nil {
			fmt.Printf("  %s PR check: %v\n", color.YellowString("Warning:"), err)
		} else {
			fmt.Printf("  [%s] PR #%d: state=%s, review=%s, merged=%v\n",
				time.Now().Format("15:04:05"), pr.Number, pr.State, pr.ReviewDecision, pr.Merged)

			dbPool, dbErr := db.Connect(ctx)
			if dbErr != nil {
				fmt.Printf("  %s DB: %v\n", color.RedString("Error:"), dbErr)
			}

			if pr.Merged {
				fmt.Println(color.GreenString("  PR merged!"))
				if dbPool != nil {
					status := "done"
					if _, err := db.UpdateTask(ctx, dbPool, task.ID, db.TaskUpdateOptions{Status: &status}); err != nil {
						fmt.Printf("  %s update task: %v\n", color.RedString("Error:"), err)
					}
				}

				// Close linked GitHub Issue and sync Projects board
				if issueNum := taskIssueNumber(ctx, task); issueNum > 0 {
					closeLinkedIssue(issueNum)
					syncProjectStatus(issueNum, "done")
				}

				notifyJoel(fmt.Sprintf("Task %s DONE — PR #%d merged", task.ShortID, prNumber))
				return nil
			}

			if pr.ReviewDecision == "CHANGES_REQUESTED" {
				fmt.Println(color.YellowString("  Changes requested"))
				if dbPool != nil {
					status := "blocked"
					if _, err := db.UpdateTask(ctx, dbPool, task.ID, db.TaskUpdateOptions{Status: &status}); err != nil {
						fmt.Printf("  %s update task: %v\n", color.RedString("Error:"), err)
					}
				}
				notifyJoel(fmt.Sprintf("Task %s needs changes — PR #%d has review feedback", task.ShortID, prNumber))
				return nil
			}
		}

		if time.Now().After(deadline) {
			fmt.Println(color.YellowString("  Timeout reached — stopping monitor"))
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// monitorNewPR searches for a new PR by branch name, polling until found or timeout.
func monitorNewPR(ctx context.Context, pool interface{}, task *db.Task, branch string, timeout int) error {
	if branch == "" {
		fmt.Println("  No branch name available — cannot search for PR")
		return nil
	}

	fmt.Printf("Searching for PR on branch: %s\n", color.CyanString(branch))

	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		pr, err := findPRByBranch(branch)
		if err != nil {
			fmt.Printf("  %s PR search: %v\n", color.YellowString("Warning:"), err)
		} else if pr != nil {
			fmt.Printf("  %s PR found: #%d %s\n", color.GreenString("Found!"), pr.Number, pr.Title)

			dbPool, dbErr := db.Connect(ctx)
			if dbErr != nil {
				fmt.Printf("  %s DB: %v\n", color.RedString("Error:"), dbErr)
			} else {
				prURL := pr.URL
				status := "in_review"
				if _, err := db.UpdateTask(ctx, dbPool, task.ID, db.TaskUpdateOptions{
					PRUrl:  &prURL,
					Status: &status,
				}); err != nil {
					fmt.Printf("  %s update task: %v\n", color.RedString("Error:"), err)
				}
			}

			notifyJoel(fmt.Sprintf("Task %s — PR #%d opened: %s", task.ShortID, pr.Number, pr.Title))

			// Now switch to monitoring the existing PR
			task.PRUrl = &pr.URL
			return monitorExistingPR(ctx, pool, task)
		} else {
			fmt.Printf("  [%s] No PR found yet — agent still working\n", time.Now().Format("15:04:05"))
		}

		if time.Now().After(deadline) {
			fmt.Println(color.YellowString("  Timeout reached — stopping search"))
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// taskIssueNumber resolves the GitHub Issue number linked to a task.
// Checks notion_id (gh-N) first, then falls back to parsing the description.
func taskIssueNumber(ctx context.Context, task *db.Task) int {
	pool, err := db.GetPool(ctx)
	if err != nil {
		return parseIssueNumFromDesc(task)
	}

	// Try notion_id first
	var notionID string
	row := pool.QueryRow(ctx, "SELECT COALESCE(notion_id, '') FROM createos_task WHERE id = $1", task.ID)
	if err := row.Scan(&notionID); err == nil && strings.HasPrefix(notionID, "gh-") {
		var num int
		if _, err := fmt.Sscanf(notionID[3:], "%d", &num); err == nil {
			return num
		}
	}

	return parseIssueNumFromDesc(task)
}

// parseIssueNumFromDesc extracts issue number from "Synced from GitHub Issue #N" in description.
func parseIssueNumFromDesc(task *db.Task) int {
	if task.Description == nil {
		return 0
	}
	desc := *task.Description
	prefix := "Synced from GitHub Issue #"
	idx := strings.Index(desc, prefix)
	if idx < 0 {
		return 0
	}
	rest := desc[idx+len(prefix):]
	var num int
	if _, err := fmt.Sscanf(rest, "%d", &num); err == nil {
		return num
	}
	return 0
}

// closeLinkedIssue closes a GitHub Issue as completed.
func closeLinkedIssue(issueNumber int) {
	cmd := exec.Command("gh", "issue", "close",
		fmt.Sprintf("%d", issueNumber),
		"--repo", defaultGHRepo,
		"--reason", "completed",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("  %s close issue #%d: %v\n%s", color.YellowString("Warning:"), issueNumber, err, string(out))
	} else {
		fmt.Printf("  Closed issue #%d\n", issueNumber)
	}
}

var githubSweepMergedDryRun bool

var githubSweepMergedCmd = &cobra.Command{
	Use:   "sweep-merged",
	Short: "Close Issues and move Projects cards for all merged PRs",
	Long: `Find recently merged PRs (last 7 days), resolve their linked tasks,
and run close-issue + projects-sync for any still-open issues. Idempotent.

Examples:
  lw github sweep-merged
  lw github sweep-merged --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return runSweepMerged(ctx, githubSweepMergedDryRun)
	},
}

// mergedPR represents a merged PR from gh pr list
type mergedPR struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	HeadRefName string `json:"headRefName"`
	MergedAt    string `json:"mergedAt"`
}

func runSweepMerged(ctx context.Context, dryRun bool) error {
	if dryRun {
		fmt.Println(color.YellowString("DRY RUN — no changes will be made"))
		fmt.Println()
	}

	// Fetch recently merged PRs
	cmd := exec.Command("gh", "pr", "list",
		"--repo", defaultGHRepo,
		"--state", "merged",
		"--json", "number,title,headRefName,mergedAt",
		"--limit", "20",
	)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("gh pr list failed: %w", err)
	}

	var prs []mergedPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return fmt.Errorf("parse gh output: %w", err)
	}

	// Filter to last 7 days
	cutoff := time.Now().AddDate(0, 0, -7)
	var recent []mergedPR
	for _, pr := range prs {
		mergedAt, err := time.Parse(time.RFC3339, pr.MergedAt)
		if err != nil {
			continue
		}
		if mergedAt.After(cutoff) {
			recent = append(recent, pr)
		}
	}

	fmt.Printf("Found %d merged PRs in last 7 days\n\n", len(recent))

	pool, err := db.GetPool(ctx)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	var closed, synced, skipped int

	for _, pr := range recent {
		// Try to find linked task by PR URL or branch name
		task := findTaskForPR(ctx, pr)
		if task == nil {
			skipped++
			continue
		}

		issueNum := taskIssueNumber(ctx, task)
		if issueNum == 0 {
			skipped++
			continue
		}

		// Check if issue is still open
		issueOpen, err := isIssueOpen(issueNum)
		if err != nil {
			fmt.Printf("  PR #%-4d %s check issue #%d: %v\n", pr.Number, color.YellowString("WARN"), issueNum, err)
			continue
		}

		if !issueOpen {
			skipped++
			continue
		}

		fmt.Printf("  PR #%-4d → Issue #%d → Task %s: %s\n",
			pr.Number, issueNum,
			color.YellowString(task.ShortID),
			truncate(pr.Title, 40))

		if !dryRun {
			closeLinkedIssue(issueNum)
			closed++
			syncProjectStatus(issueNum, "done")
			synced++

			// Update task status to done if not already
			if task.Status != "done" && task.Status != "cancelled" && task.Status != "archived" {
				status := "done"
				if _, err := db.UpdateTask(ctx, pool, task.ID, db.TaskUpdateOptions{Status: &status}); err != nil {
					fmt.Printf("    %s update task: %v\n", color.RedString("Error:"), err)
				} else {
					fmt.Printf("    Task status → done\n")
				}
			}
		} else {
			fmt.Printf("    Would close issue #%d and move Projects card to Done\n", issueNum)
			closed++
			synced++
		}
	}

	fmt.Println()
	fmt.Println(color.CyanString("Sweep Summary"))
	fmt.Printf("  Closed:  %s\n", color.GreenString("%d", closed))
	fmt.Printf("  Synced:  %s\n", color.GreenString("%d", synced))
	fmt.Printf("  Skipped: %s\n", color.HiBlackString("%d", skipped))

	return nil
}

// findTaskForPR tries to locate a task linked to a merged PR via PR URL or branch name.
func findTaskForPR(ctx context.Context, pr mergedPR) *db.Task {
	pool, err := db.GetPool(ctx)
	if err != nil {
		return nil
	}

	// Try by PR URL
	prURL := fmt.Sprintf("https://github.com/%s/pull/%d", defaultGHRepo, pr.Number)
	var taskID string
	row := pool.QueryRow(ctx, "SELECT id FROM createos_task WHERE pr_url = $1 LIMIT 1", prURL)
	if err := row.Scan(&taskID); err == nil {
		task, err := db.GetTask(ctx, pool, taskID)
		if err == nil {
			return task
		}
	}

	// Try by branch name
	row = pool.QueryRow(ctx, "SELECT id FROM createos_task WHERE branch_name = $1 LIMIT 1", pr.HeadRefName)
	if err := row.Scan(&taskID); err == nil {
		task, err := db.GetTask(ctx, pool, taskID)
		if err == nil {
			return task
		}
	}

	return nil
}

// isIssueOpen checks if a GitHub Issue is currently open.
func isIssueOpen(issueNumber int) (bool, error) {
	cmd := exec.Command("gh", "issue", "view",
		fmt.Sprintf("%d", issueNumber),
		"--repo", defaultGHRepo,
		"--json", "state",
	)
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("gh issue view failed: %w", err)
	}
	var result struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return false, fmt.Errorf("parse issue state: %w", err)
	}
	return result.State == "OPEN", nil
}

var githubCloseDoneCmd = &cobra.Command{
	Use:   "close-done <task-id>",
	Short: "Close GitHub Issue and move Projects card for a done task",
	Long: `Manually trigger the post-merge actions for a task:
  1. Close the linked GitHub Issue
  2. Move the Projects board card to Done

Useful when a PR was merged outside the monitor-pr workflow.

Examples:
  lw github close-done abc123`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]

		pool, err := db.GetPool(ctx)
		if err != nil {
			return fmt.Errorf("database: %w", err)
		}

		task, err := db.GetTask(ctx, pool, taskID)
		if err != nil {
			return err
		}

		issueNum := taskIssueNumber(ctx, task)
		if issueNum == 0 {
			return fmt.Errorf("no linked GitHub Issue found for task %s", taskID)
		}

		fmt.Printf("Task: %s (%s)\n", task.Title, task.ShortID)
		fmt.Printf("Issue: #%d\n\n", issueNum)

		closeLinkedIssue(issueNum)
		syncProjectStatus(issueNum, "done")

		// Update task status to done if not already
		if task.Status != "done" && task.Status != "cancelled" && task.Status != "archived" {
			status := "done"
			if _, err := db.UpdateTask(ctx, pool, task.ID, db.TaskUpdateOptions{Status: &status}); err != nil {
				fmt.Printf("  %s update task: %v\n", color.RedString("Error:"), err)
			} else {
				fmt.Printf("  Task status → done\n")
			}
		}

		return nil
	},
}

func init() {
	githubMonitorPRCmd.Flags().IntVar(&githubMonitorTimeout, "timeout", 300, "Timeout in seconds")
	githubSweepMergedCmd.Flags().BoolVar(&githubSweepMergedDryRun, "dry-run", false, "Show what would change without making changes")

	githubCmd.AddCommand(githubMonitorPRCmd)
	githubCmd.AddCommand(githubCloseDoneCmd)
	githubCmd.AddCommand(githubSweepMergedCmd)
}
