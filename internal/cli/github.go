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
				if task.AssignedAgent != nil {
					mgr := agent.NewManager()
					_ = mgr.Kill(*task.AssignedAgent, false)
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

func init() {
	githubMonitorPRCmd.Flags().IntVar(&githubMonitorTimeout, "timeout", 300, "Timeout in seconds")

	githubCmd.AddCommand(githubMonitorPRCmd)
}
