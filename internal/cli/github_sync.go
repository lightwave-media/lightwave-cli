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
	githubSyncAll    bool
)

var githubSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync GitHub Issues into lw task system",
	Long: `Pull GitHub Issues from the repo and create or update matching
tasks in the local task database.

Extracts structured fields from issue body:
  - **Task ID:**        → matches existing task by short ID
  - **Priority:**       → maps to p1_urgent/p2_high/p3_medium/p4_low
  - **Epic:**           → fuzzy-matches to existing epic by name
  - **Type:**           → overrides label-based type detection
  - **Dependencies:**   → extracted task IDs shown in output

Maps GitHub labels: bug → bug, enhancement → feature, documentation → docs.
Strips [Sprint N] prefix from titles.

With --all, also syncs closed issues (marks their tasks as done).

Examples:
  lw github sync
  lw github sync --all
  lw github sync --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return runGitHubSync(ctx, githubSyncDryRun, githubSyncAll)
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
	Closed  []string
	Skipped []string
	Errors  []string
}

// epicCache avoids repeated DB lookups for the same epic name
type epicCache struct {
	pool  *pgxpool.Pool
	cache map[string]*db.Epic // name → epic (nil = not found)
}

func newEpicCache(pool *pgxpool.Pool) *epicCache {
	return &epicCache{pool: pool, cache: make(map[string]*db.Epic)}
}

func (ec *epicCache) resolve(ctx context.Context, name string) *db.Epic {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return nil
	}
	if epic, ok := ec.cache[key]; ok {
		return epic
	}
	epic, _ := db.FindEpicByName(ctx, ec.pool, name)
	ec.cache[key] = epic
	return epic
}

func runGitHubSync(ctx context.Context, dryRun, includeAll bool) error {
	if dryRun {
		fmt.Println(color.YellowString("DRY RUN — no changes will be made"))
		fmt.Println()
	}

	// 1. Fetch issues from GitHub
	state := "open"
	if includeAll {
		state = "all"
	}
	fmt.Printf(color.CyanString("Fetching %s issues from %s...\n"), state, defaultGHRepo)
	issues, err := fetchIssues(state)
	if err != nil {
		return fmt.Errorf("fetching issues: %w", err)
	}
	fmt.Printf("Found %s issues\n\n", color.GreenString("%d", len(issues)))

	if len(issues) == 0 {
		return nil
	}

	// 2. Connect to DB
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	epics := newEpicCache(pool)

	// 3. Sync each issue
	result := syncResult{}
	for _, issue := range issues {
		syncOneIssue(ctx, pool, epics, issue, dryRun, &result)
	}

	// 4. Print summary
	fmt.Println()
	fmt.Println(color.CyanString("Sync Summary"))
	fmt.Printf("  Created: %s\n", color.GreenString("%d", len(result.Created)))
	fmt.Printf("  Updated: %s\n", color.YellowString("%d", len(result.Updated)))
	if len(result.Closed) > 0 {
		fmt.Printf("  Closed:  %s\n", color.BlueString("%d", len(result.Closed)))
	}
	fmt.Printf("  Skipped: %s\n", color.HiBlackString("%d", len(result.Skipped)))
	if len(result.Errors) > 0 {
		fmt.Printf("  Errors:  %s\n", color.RedString("%d", len(result.Errors)))
		for _, e := range result.Errors {
			fmt.Printf("    %s\n", color.RedString(e))
		}
	}

	return nil
}

func fetchIssues(state string) ([]ghIssue, error) {
	cmd := exec.Command("gh", "issue", "list",
		"--repo", defaultGHRepo,
		"--state", state,
		"--json", "number,title,body,state,labels,milestone,url",
		"--limit", "200",
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

func syncOneIssue(ctx context.Context, pool *pgxpool.Pool, epics *epicCache, issue ghIssue, dryRun bool, result *syncResult) {
	prefix := fmt.Sprintf("  #%-4d", issue.Number)

	// Extract structured fields from issue body
	fields := parseIssueBody(issue)
	title := stripSprintPrefix(issue.Title)

	// Try to find existing task: first by Task ID in body, then by notion_id
	ghRef := fmt.Sprintf("gh-%d", issue.Number)
	var existingTask *db.Task

	if fields.taskID != "" {
		existingTask, _ = db.GetTask(ctx, pool, fields.taskID)
	}
	if existingTask == nil {
		existingTask, _ = db.GetTaskByNotionID(ctx, pool, ghRef)
	}

	// Handle closed issues
	if issue.State == "CLOSED" || issue.State == "closed" {
		if existingTask != nil && existingTask.Status != "done" && existingTask.Status != "cancelled" {
			fmt.Printf("%s %s %s (task %s → done)\n", prefix, color.BlueString("CLOSE"), truncate(title, 40), color.YellowString(existingTask.ShortID))
			if !dryRun {
				status := "done"
				if _, err := db.UpdateTask(ctx, pool, existingTask.ID, db.TaskUpdateOptions{Status: &status}); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("#%d: close %s: %v", issue.Number, existingTask.ShortID, err))
					return
				}
			}
			result.Closed = append(result.Closed, fmt.Sprintf("#%d → %s", issue.Number, existingTask.ShortID))
		} else {
			fmt.Printf("%s %s %s (closed)\n", prefix, color.HiBlackString("SKIP"), truncate(title, 40))
			result.Skipped = append(result.Skipped, fmt.Sprintf("#%d (closed)", issue.Number))
		}
		return
	}

	// Resolve epic from body
	var epicID string
	if fields.epic != "" {
		if epic := epics.resolve(ctx, fields.epic); epic != nil {
			epicID = epic.ID
		}
	}

	if existingTask != nil {
		shortID := existingTask.ShortID
		needsUpdate := false
		opts := db.TaskUpdateOptions{}

		if fields.priority != "" && fields.priority != existingTask.Priority {
			opts.Priority = &fields.priority
			needsUpdate = true
		}
		if title != existingTask.Title {
			opts.Title = &title
			needsUpdate = true
		}
		if epicID != "" && (existingTask.EpicID == nil || *existingTask.EpicID != epicID) {
			opts.EpicID = &epicID
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
	suffix := ""
	if len(fields.deps) > 0 {
		suffix = color.HiBlackString(" deps:[%s]", strings.Join(fields.deps, ","))
	}
	fmt.Printf("%s %s %s%s\n", prefix, color.GreenString("CREATE"), truncate(title, 45), suffix)
	if dryRun {
		result.Created = append(result.Created, fmt.Sprintf("#%d → %s", issue.Number, title))
		return
	}

	desc := formatDescription(issue)
	createOpts := db.TaskCreateOptions{
		Title:       title,
		Description: desc,
		Priority:    fields.priority,
		TaskType:    fields.taskType,
		NotionID:    ghRef,
	}
	if createOpts.Priority == "" {
		createOpts.Priority = "p3_medium"
	}
	if epicID != "" {
		createOpts.EpicID = epicID
	}

	newTask, err := db.CreateTask(ctx, pool, createOpts)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("#%d: create: %v", issue.Number, err))
		return
	}

	result.Created = append(result.Created, fmt.Sprintf("#%d → %s", issue.Number, newTask.ShortID))
}

// parsedFields holds all structured data extracted from an issue body
type parsedFields struct {
	taskID   string
	priority string
	epic     string
	taskType string
	deps     []string
}

var (
	taskIDRe       = regexp.MustCompile(`\*\*Task ID:\*\*\s*([a-f0-9]{8})`)
	priorityRe     = regexp.MustCompile(`\*\*Priority:\*\*\s*(P[1-4][^\n]*)`)
	epicRe         = regexp.MustCompile(`\*\*Epic:\*\*\s*([^\n]+)`)
	typeRe         = regexp.MustCompile(`\*\*Type:\*\*\s*([^\n]+)`)
	depsRe         = regexp.MustCompile(`\*\*Dependencies:\*\*\s*([^\n]+)`)
	depTaskIDRe    = regexp.MustCompile(`([a-f0-9]{8})`)
	sprintPrefixRe = regexp.MustCompile(`^\[Sprint \d+\]\s*`)
)

func parseIssueBody(issue ghIssue) parsedFields {
	body := issue.Body
	f := parsedFields{}

	if m := taskIDRe.FindStringSubmatch(body); len(m) >= 2 {
		f.taskID = m[1]
	}
	if m := priorityRe.FindStringSubmatch(body); len(m) >= 2 {
		f.priority = normalizePriority(strings.TrimSpace(m[1]))
	}
	if m := epicRe.FindStringSubmatch(body); len(m) >= 2 {
		f.epic = strings.TrimSpace(m[1])
	}

	// Type: body field overrides label detection
	if m := typeRe.FindStringSubmatch(body); len(m) >= 2 {
		f.taskType = normalizeType(strings.TrimSpace(m[1]))
	} else {
		f.taskType = mapLabelsToType(issue.Labels)
	}

	// Dependencies: extract task IDs
	if m := depsRe.FindStringSubmatch(body); len(m) >= 2 {
		raw := m[1]
		lowerRaw := strings.ToLower(raw)
		if !strings.Contains(lowerRaw, "none") {
			for _, dm := range depTaskIDRe.FindAllString(raw, -1) {
				f.deps = append(f.deps, dm)
			}
		}
	}

	return f
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

func normalizeType(raw string) string {
	switch strings.ToLower(raw) {
	case "bug", "fix", "hotfix":
		return "bug"
	case "feature", "enhancement":
		return "feature"
	case "chore":
		return "chore"
	case "docs", "documentation":
		return "docs"
	default:
		return raw
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
	githubSyncCmd.Flags().BoolVar(&githubSyncAll, "all", false, "Include closed issues (marks their tasks as done)")
	githubCmd.AddCommand(githubSyncCmd)
}
