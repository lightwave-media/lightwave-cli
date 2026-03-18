package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/fatih/color"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/spf13/cobra"
)

var (
	githubSyncDryRun bool
)

var githubSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync GitHub Issues into lw task system",
	Long: `Pull open GitHub Issues from the repo and create or update
matching tasks in the local task database.

Maps issue body fields (Task ID, Priority, Dependencies) to task records.
Maps GitHub labels to task type (bug/enhancement → bug/feature).

Examples:
  lw github sync
  lw github sync --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return runGitHubSync(ctx, githubSyncDryRun)
	},
}

// ghIssue represents a GitHub Issue from gh issue list --json
type ghIssue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	Labels    []ghLabel `json:"labels"`
	Milestone *ghMile   `json:"milestone"`
	URL       string    `json:"url"`
}

type ghLabel struct {
	Name string `json:"name"`
}

type ghMile struct {
	Title string `json:"title"`
}

// syncResult tracks what happened during sync
type syncResult struct {
	Created []string
	Updated []string
	Skipped []string
	Errors  []string
}

func runGitHubSync(ctx context.Context, dryRun bool) error {
	if dryRun {
		fmt.Println(color.YellowString("DRY RUN — no changes will be made"))
		fmt.Println()
	}

	// 1. Fetch open issues from GitHub
	fmt.Printf(color.CyanString("Fetching open issues from %s...\n"), defaultGHRepo)
	issues, err := fetchOpenIssues()
	if err != nil {
		return fmt.Errorf("fetching issues: %w", err)
	}
	fmt.Printf("Found %s open issues\n\n", color.GreenString("%d", len(issues)))

	if len(issues) == 0 {
		return nil
	}

	// 2. Connect to DB
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	// 3. Sync each issue
	result := syncResult{}
	for _, issue := range issues {
		syncOneIssue(ctx, pool, issue, dryRun, &result)
	}

	// 4. Print summary
	fmt.Println()
	fmt.Println(color.CyanString("Sync Summary"))
	fmt.Printf("  Created: %s\n", color.GreenString("%d", len(result.Created)))
	fmt.Printf("  Updated: %s\n", color.YellowString("%d", len(result.Updated)))
	fmt.Printf("  Skipped: %s\n", color.HiBlackString("%d", len(result.Skipped)))
	if len(result.Errors) > 0 {
		fmt.Printf("  Errors:  %s\n", color.RedString("%d", len(result.Errors)))
		for _, e := range result.Errors {
			fmt.Printf("    %s\n", color.RedString(e))
		}
	}

	return nil
}

func fetchOpenIssues() ([]ghIssue, error) {
	cmd := exec.Command("gh", "issue", "list",
		"--repo", defaultGHRepo,
		"--state", "open",
		"--json", "number,title,body,state,labels,milestone,url",
		"--limit", "100",
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh issue list failed: %w", err)
	}

	var issues []ghIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}

	return issues, nil
}

func syncOneIssue(ctx context.Context, pool *pgxpool.Pool, issue ghIssue, dryRun bool, result *syncResult) {
	prefix := fmt.Sprintf("  #%-4d", issue.Number)

	// Extract structured fields from issue body
	taskID := extractTaskID(issue.Body)
	priority := extractPriority(issue.Body)
	taskType := mapLabelsToType(issue.Labels)
	title := stripSprintPrefix(issue.Title)

	// Try to find existing task: first by Task ID in body, then by notion_id
	ghRef := fmt.Sprintf("gh-%d", issue.Number)
	var existingTask *db.Task

	if taskID != "" {
		existingTask, _ = db.GetTask(ctx, pool, taskID)
	}
	if existingTask == nil {
		existingTask, _ = db.GetTaskByNotionID(ctx, pool, ghRef)
	}

	if existingTask != nil {
		shortID := existingTask.ShortID
		needsUpdate := false
		opts := db.TaskUpdateOptions{}

		if priority != "" && priority != existingTask.Priority {
			opts.Priority = &priority
			needsUpdate = true
		}
		if title != existingTask.Title {
			opts.Title = &title
			needsUpdate = true
		}

		if !needsUpdate {
			fmt.Printf("%s %s %s (task %s in sync)\n", prefix, color.HiBlackString("SKIP"), truncate(title, 40), color.YellowString(shortID))
			result.Skipped = append(result.Skipped, fmt.Sprintf("#%d → %s", issue.Number, shortID))
			return
		}

		fmt.Printf("%s %s %s (task %s)\n", prefix, color.YellowString("UPDATE"), truncate(title, 40), color.YellowString(shortID))
		if !dryRun {
			if _, err := db.UpdateTask(ctx, pool, existingTask.ID, opts); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("#%d: update %s: %v", issue.Number, shortID, err))
				return
			}
		}
		result.Updated = append(result.Updated, fmt.Sprintf("#%d → %s", issue.Number, shortID))
		return
	}

	// No existing task — create one
	fmt.Printf("%s %s %s\n", prefix, color.GreenString("CREATE"), truncate(title, 50))
	if dryRun {
		result.Created = append(result.Created, fmt.Sprintf("#%d → %s", issue.Number, title))
		return
	}

	desc := formatDescription(issue)
	createOpts := db.TaskCreateOptions{
		Title:       title,
		Description: desc,
		Priority:    priority,
		TaskType:    taskType,
		NotionID:    ghRef,
	}
	if priority == "" {
		createOpts.Priority = "p3_medium"
	}

	newTask, err := db.CreateTask(ctx, pool, createOpts)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("#%d: create: %v", issue.Number, err))
		return
	}

	result.Created = append(result.Created, fmt.Sprintf("#%d → %s", issue.Number, newTask.ShortID))
}

var taskIDRe = regexp.MustCompile(`\*\*Task ID:\*\*\s*([a-f0-9]{8})`)
var priorityRe = regexp.MustCompile(`\*\*Priority:\*\*\s*(P[1-4][^\n]*)`)
var sprintPrefixRe = regexp.MustCompile(`^\[Sprint \d+\]\s*`)

func extractTaskID(body string) string {
	m := taskIDRe.FindStringSubmatch(body)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

func extractPriority(body string) string {
	m := priorityRe.FindStringSubmatch(body)
	if len(m) >= 2 {
		return normalizePriority(strings.TrimSpace(m[1]))
	}
	return ""
}

func normalizePriority(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "p1") || strings.Contains(lower, "urgent"):
		return "p1_urgent"
	case strings.Contains(lower, "p2") || strings.Contains(lower, "high"):
		return "p2_high"
	case strings.Contains(lower, "p3") || strings.Contains(lower, "medium"):
		return "p3_medium"
	case strings.Contains(lower, "p4") || strings.Contains(lower, "low"):
		return "p4_low"
	default:
		return "p3_medium"
	}
}

func mapLabelsToType(labels []ghLabel) string {
	for _, l := range labels {
		switch l.Name {
		case "bug":
			return "bug"
		case "enhancement":
			return "feature"
		case "documentation":
			return "docs"
		}
	}
	return "feature"
}

func stripSprintPrefix(title string) string {
	return sprintPrefixRe.ReplaceAllString(title, "")
}

func formatDescription(issue ghIssue) string {
	parts := []string{
		fmt.Sprintf("Synced from GitHub Issue #%d", issue.Number),
		fmt.Sprintf("URL: %s", issue.URL),
	}
	if issue.Body != "" {
		parts = append(parts, "", issue.Body)
	}
	return strings.Join(parts, "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func init() {
	githubSyncCmd.Flags().BoolVar(&githubSyncDryRun, "dry-run", false, "Show what would be synced without making changes")
	githubCmd.AddCommand(githubSyncCmd)
}
