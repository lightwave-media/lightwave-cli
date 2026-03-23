package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/spf13/cobra"
)

var githubProjectsSyncDryRun bool

var githubProjectsSyncCmd = &cobra.Command{
	Use:   "projects-sync",
	Short: "Sync task status to GitHub Projects board",
	Long: `Bi-directional sync between lw task status and GitHub Projects board columns.

Task status → Board column:
  in_progress → In Progress
  in_review   → In Progress
  done        → Done
  cancelled   → Done
  approved    → Todo
  next_up     → Todo

Also syncs board → task status for manual board moves.

Examples:
  lw github projects-sync
  lw github projects-sync --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return runProjectsSync(ctx, githubProjectsSyncDryRun)
	},
}

// Default GitHub Project V2 IDs — override via flags if the project changes
var (
	projectID     = "PVT_kwDODlnoUM4BJWJU"
	statusFieldID = "PVTSSF_lADODlnoUM4BJWJUzg5iZUk"
	statusTodo    = "f75ad846"
	statusInProg  = "47fc9ee4"
	statusDone    = "98236657"
)

// projectItem represents an item from gh project item-list
type projectItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Content struct {
		Number int    `json:"number"`
		Type   string `json:"type"`
		Body   string `json:"body"`
		URL    string `json:"url"`
	} `json:"content"`
}

func runProjectsSync(ctx context.Context, dryRun bool) error {
	if dryRun {
		fmt.Println(color.YellowString("DRY RUN — no changes will be made"))
		fmt.Println()
	}

	// 1. Fetch project items
	fmt.Println(color.CyanString("Fetching project board items..."))
	items, err := fetchProjectItems()
	if err != nil {
		return fmt.Errorf("fetching project items: %w", err)
	}
	fmt.Printf("Found %d items\n\n", len(items))

	// 2. Connect to DB
	pool, err := db.GetPool(ctx)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	var updated, skipped, errors int

	for _, item := range items {
		if item.Content.Type != "Issue" || item.Content.Number == 0 {
			continue
		}

		// Find the task by parsing the issue body for Task ID
		fields := parseIssueBody(ghIssue{Body: item.Content.Body})
		if fields.taskID == "" {
			// Try notion_id fallback
			ghRef := fmt.Sprintf("gh-%d", item.Content.Number)
			task, _ := db.GetTaskByNotionID(ctx, pool, ghRef)
			if task != nil && len(task.ID) >= 8 {
				fields.taskID = task.ID[:8]
			}
		}

		if fields.taskID == "" {
			continue
		}

		task, err := db.GetTask(ctx, pool, fields.taskID)
		if err != nil {
			continue
		}

		// Map task status → expected board column
		expectedColumn := taskStatusToColumn(task.Status)
		currentColumn := item.Status

		if expectedColumn == currentColumn {
			skipped++
			continue
		}

		// Determine sync direction:
		// If the board was manually moved and task is still "approved",
		// sync board → task. Otherwise sync task → board.
		if currentColumn == "In Progress" && task.Status == "approved" {
			// Board was moved manually — sync to task
			fmt.Printf("  #%-4d %s → task %s (board→task: %s → in_progress)\n",
				item.Content.Number,
				color.CyanString("SYNC"),
				color.YellowString(task.ShortID),
				task.Status)
			if !dryRun {
				status := "in_progress"
				if _, err := db.UpdateTask(ctx, pool, task.ID, db.TaskUpdateOptions{Status: &status}); err != nil {
					fmt.Printf("    %s %v\n", color.RedString("Error:"), err)
					errors++
					continue
				}
			}
			updated++
		} else if currentColumn == "Done" && task.Status != "done" && task.Status != "cancelled" {
			// Board moved to Done manually
			fmt.Printf("  #%-4d %s → task %s (board→task: %s → done)\n",
				item.Content.Number,
				color.CyanString("SYNC"),
				color.YellowString(task.ShortID),
				task.Status)
			if !dryRun {
				status := "done"
				if _, err := db.UpdateTask(ctx, pool, task.ID, db.TaskUpdateOptions{Status: &status}); err != nil {
					fmt.Printf("    %s %v\n", color.RedString("Error:"), err)
					errors++
					continue
				}
			}
			updated++
		} else {
			// Task status changed — sync task → board
			optionID := columnToOptionID(expectedColumn)
			if optionID == "" {
				continue
			}
			fmt.Printf("  #%-4d %s → %s (task→board: %s → %s)\n",
				item.Content.Number,
				color.YellowString("MOVE"),
				color.CyanString(expectedColumn),
				currentColumn, expectedColumn)
			if !dryRun {
				if err := moveProjectItem(item.ID, optionID); err != nil {
					fmt.Printf("    %s %v\n", color.RedString("Error:"), err)
					errors++
					continue
				}
			}
			updated++
		}
	}

	fmt.Println()
	fmt.Println(color.CyanString("Projects Sync Summary"))
	fmt.Printf("  Updated: %s\n", color.GreenString("%d", updated))
	fmt.Printf("  Skipped: %s\n", color.HiBlackString("%d", skipped))
	if errors > 0 {
		fmt.Printf("  Errors:  %s\n", color.RedString("%d", errors))
	}

	return nil
}

func fetchProjectItems() ([]projectItem, error) {
	cmd := exec.Command("gh", "project", "item-list", "2",
		"--owner", "lightwave-media",
		"--format", "json",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh project item-list failed: %w\n%s", err, string(out))
	}

	var result struct {
		Items []projectItem `json:"items"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return result.Items, nil
}

func taskStatusToColumn(status string) string {
	switch status {
	case "in_progress", "in_review":
		return "In Progress"
	case "done", "cancelled", "archived":
		return "Done"
	default:
		return "Todo"
	}
}

func columnToOptionID(column string) string {
	switch column {
	case "Todo":
		return statusTodo
	case "In Progress":
		return statusInProg
	case "Done":
		return statusDone
	default:
		return ""
	}
}

func moveProjectItem(itemID, optionID string) error {
	cmd := exec.Command("gh", "project", "item-edit",
		"--project-id", projectID,
		"--id", itemID,
		"--field-id", statusFieldID,
		"--single-select-option-id", optionID,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh project item-edit failed: %w\n%s", err, string(out))
	}
	return nil
}

// syncProjectStatus is called by other commands (e.g. task update) to keep the board in sync.
// It finds the project item for a given issue number and moves it to the right column.
func syncProjectStatus(issueNumber int, taskStatus string) {
	expectedColumn := taskStatusToColumn(taskStatus)
	optionID := columnToOptionID(expectedColumn)
	if optionID == "" {
		return
	}

	items, err := fetchProjectItems()
	if err != nil {
		return
	}

	for _, item := range items {
		if item.Content.Number == issueNumber && item.Status != expectedColumn {
			_ = moveProjectItem(item.ID, optionID)
			return
		}
	}
}

func init() {
	githubProjectsSyncCmd.Flags().BoolVar(&githubProjectsSyncDryRun, "dry-run", false, "Show what would change without making changes")
	githubProjectsSyncCmd.Flags().StringVar(&projectID, "project-id", projectID, "GitHub Project V2 node ID")
	githubProjectsSyncCmd.Flags().StringVar(&statusFieldID, "status-field-id", statusFieldID, "Status field ID on the project")
	githubCmd.AddCommand(githubProjectsSyncCmd)
}
