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
	githubSyncJSON   bool
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
  - **Acceptance Criteria:** → populates task AC field

Maps GitHub labels: bug → bug, enhancement → feature, documentation → docs.
Strips [Sprint N] prefix from titles.

On CREATE, stamps **Task ID:** back into the GitHub Issue body.
With --all, also syncs closed issues (marks their tasks as done).
With --json, outputs structured JSON for orchestrator consumption.

Examples:
  lw github sync
  lw github sync --all
  lw github sync --dry-run
  lw github sync --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return runGitHubSync(ctx, githubSyncDryRun, githubSyncAll, githubSyncJSON)
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

// syncAction represents one sync operation for JSON output
type syncAction struct {
	IssueNumber int      `json:"issue_number"`
	IssueURL    string   `json:"issue_url,omitempty"`
	Action      string   `json:"action"` // create, update, close, skip
	TaskID      string   `json:"task_id,omitempty"`
	Title       string   `json:"title"`
	Changes     []string `json:"changes,omitempty"`
	Deps        []string `json:"deps,omitempty"`
	Epic        string   `json:"epic,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	TaskType    string   `json:"task_type,omitempty"`
	HasAC       bool     `json:"has_ac,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// syncResult tracks what happened during sync
type syncResult struct {
	Created []string
	Updated []string
	Closed  []string
	Skipped []string
	Errors  []string
	Actions []syncAction
}

// epicCache avoids repeated DB lookups for the same epic name
type epicCache struct {
	pool  *pgxpool.Pool
	cache map[string]*db.Epic
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

func runGitHubSync(ctx context.Context, dryRun, includeAll, jsonOut bool) error {
	if dryRun && !jsonOut {
		fmt.Println(color.YellowString("DRY RUN — no changes will be made"))
		fmt.Println()
	}

	// 1. Fetch issues from GitHub
	state := "open"
	if includeAll {
		state = "all"
	}
	if !jsonOut {
		fmt.Printf(color.CyanString("Fetching %s issues from %s...\n"), state, defaultGHRepo)
	}
	issues, err := fetchIssues(state)
	if err != nil {
		return fmt.Errorf("fetching issues: %w", err)
	}
	if !jsonOut {
		fmt.Printf("Found %s issues\n\n", color.GreenString("%d", len(issues)))
	}

	if len(issues) == 0 {
		if jsonOut {
			fmt.Println("[]")
		}
		return nil
	}

	// 2. Connect to DB (don't close — caller may need the pool after us)
	pool, err := db.GetPool(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}

	epics := newEpicCache(pool)

	// 3. Sync each issue
	result := syncResult{}
	for _, issue := range issues {
		syncOneIssue(ctx, pool, epics, issue, dryRun, jsonOut, &result)
	}

	// 4. Output
	if jsonOut {
		out, _ := json.MarshalIndent(result.Actions, "", "  ")
		fmt.Println(string(out))
		return nil
	}

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

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Include stderr in error for auth/network diagnostics
		return nil, fmt.Errorf("gh issue list failed: %w\n%s", err, string(out))
	}

	var issues []ghIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}

	return issues, nil
}

func syncOneIssue(ctx context.Context, pool *pgxpool.Pool, epics *epicCache, issue ghIssue, dryRun, jsonOut bool, result *syncResult) {
	prefix := fmt.Sprintf("  #%-4d", issue.Number)

	fields := parseIssueBody(issue)
	title := stripSprintPrefix(issue.Title)

	// Helper to build a syncAction with common fields pre-filled
	action := func(act, taskID string) syncAction {
		return syncAction{
			IssueNumber: issue.Number,
			IssueURL:    issue.URL,
			Action:      act,
			TaskID:      taskID,
			Title:       title,
			Deps:        fields.deps,
			Epic:        fields.epic,
			Priority:    fields.priority,
			TaskType:    fields.taskType,
			HasAC:       fields.acceptanceCriteria != "",
		}
	}

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
	if strings.EqualFold(issue.State, "closed") {
		if existingTask != nil && existingTask.Status != "done" && existingTask.Status != "cancelled" {
			if !jsonOut {
				fmt.Printf("%s %s %s (task %s → done)\n", prefix, color.BlueString("CLOSE"), truncate(title, 40), color.YellowString(existingTask.ShortID))
			}
			if !dryRun {
				status := "done"
				if _, err := db.UpdateTask(ctx, pool, existingTask.ID, db.TaskUpdateOptions{Status: &status}); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("#%d: close %s: %v", issue.Number, existingTask.ShortID, err))
					a := action("close", existingTask.ShortID)
					a.Error = err.Error()
					result.Actions = append(result.Actions, a)
					return
				}
			}
			result.Closed = append(result.Closed, fmt.Sprintf("#%d → %s", issue.Number, existingTask.ShortID))
			result.Actions = append(result.Actions, action("close", existingTask.ShortID))
		} else {
			if !jsonOut {
				fmt.Printf("%s %s %s (closed)\n", prefix, color.HiBlackString("SKIP"), truncate(title, 40))
			}
			result.Skipped = append(result.Skipped, fmt.Sprintf("#%d (closed)", issue.Number))
			result.Actions = append(result.Actions, action("skip", ""))
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
		var changes []string

		if fields.priority != "" && fields.priority != existingTask.Priority {
			opts.Priority = &fields.priority
			needsUpdate = true
			changes = append(changes, fmt.Sprintf("priority: %s → %s", existingTask.Priority, fields.priority))
		}
		if title != existingTask.Title {
			opts.Title = &title
			needsUpdate = true
			changes = append(changes, "title")
		}
		if epicID != "" && (existingTask.EpicID == nil || *existingTask.EpicID != epicID) {
			opts.EpicID = &epicID
			needsUpdate = true
			changes = append(changes, "epic")
		}

		// Sync description — only compare the issue body portion to avoid
		// false diffs from the "Synced from" header changing format
		newDesc := formatDescription(issue)
		if existingTask.Description != nil {
			existingDesc := *existingTask.Description
			// If the existing description doesn't start with our sync header,
			// it was set before sync existed. Only update if the issue body
			// contains meaningful content beyond what the task already has.
			if strings.HasPrefix(existingDesc, "Synced from GitHub Issue #") {
				// Same format — compare directly
				if existingDesc != newDesc {
					opts.Description = &newDesc
					needsUpdate = true
					changes = append(changes, "description")
				}
			} else {
				// Legacy description — adopt sync format on first sync
				opts.Description = &newDesc
				needsUpdate = true
				changes = append(changes, "description")
			}
		} else {
			opts.Description = &newDesc
			needsUpdate = true
			changes = append(changes, "description")
		}

		if !needsUpdate {
			if !jsonOut {
				fmt.Printf("%s %s %s (task %s in sync)\n", prefix, color.HiBlackString("SKIP"), truncate(title, 40), color.YellowString(shortID))
			}
			result.Skipped = append(result.Skipped, fmt.Sprintf("#%d → %s", issue.Number, shortID))
			result.Actions = append(result.Actions, action("skip", shortID))
			syncPriorityLabel(issue, fields.priority, dryRun, jsonOut)
			return
		}

		if !jsonOut {
			changeStr := color.HiBlackString(" [%s]", strings.Join(changes, ", "))
			fmt.Printf("%s %s %s (task %s)%s\n", prefix, color.YellowString("UPDATE"), truncate(title, 35), color.YellowString(shortID), changeStr)
		}
		if !dryRun {
			if _, err := db.UpdateTask(ctx, pool, existingTask.ID, opts); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("#%d: update %s: %v", issue.Number, shortID, err))
				a := action("update", shortID)
				a.Changes = changes
				a.Error = err.Error()
				result.Actions = append(result.Actions, a)
				return
			}
		}
		result.Updated = append(result.Updated, fmt.Sprintf("#%d → %s", issue.Number, shortID))
		a := action("update", shortID)
		a.Changes = changes
		result.Actions = append(result.Actions, a)
		syncPriorityLabel(issue, fields.priority, dryRun, jsonOut)
		return
	}

	// No existing task — create one
	if !jsonOut {
		suffix := ""
		if len(fields.deps) > 0 {
			suffix = color.HiBlackString(" deps:[%s]", strings.Join(fields.deps, ","))
		}
		fmt.Printf("%s %s %s%s\n", prefix, color.GreenString("CREATE"), truncate(title, 45), suffix)
	}
	if dryRun {
		result.Created = append(result.Created, fmt.Sprintf("#%d → %s", issue.Number, title))
		result.Actions = append(result.Actions, action("create", ""))
		return
	}

	desc := formatDescription(issue)
	createOpts := db.TaskCreateOptions{
		Title:              title,
		Description:        desc,
		AcceptanceCriteria: fields.acceptanceCriteria,
		Priority:           fields.priority,
		TaskType:           fields.taskType,
		NotionID:           ghRef,
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
		a := action("create", "")
		a.Error = err.Error()
		result.Actions = append(result.Actions, a)
		return
	}

	// Stamp task ID back into GitHub Issue body (only if not already present)
	if fields.taskID == "" {
		stampTaskID(issue.Number, newTask.ShortID, jsonOut)
	}

	result.Created = append(result.Created, fmt.Sprintf("#%d → %s", issue.Number, newTask.ShortID))
	result.Actions = append(result.Actions, action("create", newTask.ShortID))

	// Sync priority label to GitHub if missing
	syncPriorityLabel(issue, fields.priority, dryRun, jsonOut)
}

// stampTaskID prepends **Task ID:** to a GitHub Issue body via gh issue edit.
// Guards against double-stamping by checking if the body already contains a Task ID.
func stampTaskID(issueNumber int, taskID string, jsonOut bool) {
	cmd := exec.Command("gh", "issue", "view",
		fmt.Sprintf("%d", issueNumber),
		"--repo", defaultGHRepo,
		"--json", "body",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if !jsonOut {
			fmt.Printf("    %s stamp task ID: %v\n", color.YellowString("Warning:"), err)
		}
		return
	}

	var issueData struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(out, &issueData); err != nil {
		return
	}

	// Guard: don't double-stamp
	if taskIDRe.MatchString(issueData.Body) {
		return
	}

	newBody := fmt.Sprintf("**Task ID:** %s\n%s", taskID, issueData.Body)

	editCmd := exec.Command("gh", "issue", "edit",
		fmt.Sprintf("%d", issueNumber),
		"--repo", defaultGHRepo,
		"--body", newBody,
	)
	if editErr := editCmd.Run(); editErr != nil {
		if !jsonOut {
			fmt.Printf("    %s stamp task ID: %v\n", color.YellowString("Warning:"), editErr)
		}
		return
	}

	if !jsonOut {
		fmt.Printf("    stamped Task ID %s into issue #%d\n", color.GreenString(taskID), issueNumber)
	}
}

// parsedFields holds all structured data extracted from an issue body
type parsedFields struct {
	taskID             string
	priority           string
	epic               string
	taskType           string
	deps               []string
	acceptanceCriteria string
}

var (
	taskIDRe        = regexp.MustCompile(`\*\*Task ID:\*\*\s*([a-f0-9]{8})`)
	priorityRe      = regexp.MustCompile(`\*\*Priority:\*\*\s*(P[1-4][^\n]*)`)
	epicRe          = regexp.MustCompile(`\*\*Epic:\*\*\s*([^\n]+)`)
	typeRe          = regexp.MustCompile(`\*\*Type:\*\*\s*([^\n]+)`)
	depsRe          = regexp.MustCompile(`\*\*Dependencies:\*\*\s*([^\n]+)`)
	sprintPrefixRe  = regexp.MustCompile(`^\[Sprint \d+\]\s*`)
	shortIDPrefixRe = regexp.MustCompile(`^\[[a-f0-9]{8}\]\s*`)
	// Match bulleted AC lists: bold (**Acceptance Criteria:**) or heading (## Acceptance Criteria)
	acRe = regexp.MustCompile(`(?s)(?:\*\*Acceptance Criteria:\*\*|##\s+Acceptance Criteria)\s*\n((?:[-*] (?:\[.\] )?[^\n]+\n?)+)`)
	// Dep task IDs: exactly 8 hex chars bounded by word boundaries to avoid
	// false positives on hex substrings in URLs or prose
	depTaskIDRe = regexp.MustCompile(`\b([a-f0-9]{8})\b`)
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

	// Dependencies: extract task IDs (deduplicated)
	if m := depsRe.FindStringSubmatch(body); len(m) >= 2 {
		raw := m[1]
		if !strings.Contains(strings.ToLower(raw), "none") {
			seen := map[string]bool{}
			for _, dm := range depTaskIDRe.FindAllString(raw, -1) {
				if !seen[dm] {
					f.deps = append(f.deps, dm)
					seen[dm] = true
				}
			}
		}
	}

	// Acceptance Criteria: extract bulleted list (- or * markers)
	if m := acRe.FindStringSubmatch(body); len(m) >= 2 {
		f.acceptanceCriteria = strings.TrimSpace(m[1])
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
	title = sprintPrefixRe.ReplaceAllString(title, "")
	title = shortIDPrefixRe.ReplaceAllString(title, "")
	return title
}

func formatDescription(issue ghIssue) string {
	parts := []string{
		fmt.Sprintf("Synced from GitHub Issue #%d", issue.Number),
		fmt.Sprintf("URL: %s", issue.URL),
	}
	if issue.Body != "" {
		// Strip metadata fields that are already stored as task columns
		cleaned := stripIssueMeta(issue.Body)
		if cleaned != "" {
			parts = append(parts, "", cleaned)
		}
	}
	return strings.Join(parts, "\n")
}

// stripIssueMeta removes structured metadata fields from issue body that are
// already stored in dedicated task columns (Task ID, Priority, Type, Session, Epic).
// Keeps content sections like Acceptance Criteria, Dependencies, and free-form text.
func stripIssueMeta(body string) string {
	lines := strings.Split(body, "\n")
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "**Task ID:**") ||
			strings.HasPrefix(trimmed, "**Priority:**") ||
			strings.HasPrefix(trimmed, "**Type:**") ||
			strings.HasPrefix(trimmed, "**Session:**") ||
			strings.HasPrefix(trimmed, "**Epic:**") {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func truncate(s string, n int) string {
	if n < 4 {
		n = 4
	}
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// syncPriorityLabel ensures an issue has a matching p1/p2/p3/p4 label
// based on its body priority field. This enables label-based priority sorting
// in `lw github pick` without requiring manual labeling.
func syncPriorityLabel(issue ghIssue, priority string, dryRun, jsonOut bool) {
	if priority == "" {
		return
	}

	// Determine which label should exist
	var wantLabel string
	switch priority {
	case "p1_urgent":
		wantLabel = "p1"
	case "p2_high":
		wantLabel = "p2"
	case "p3_medium":
		wantLabel = "p3"
	case "p4_low":
		wantLabel = "p4"
	default:
		return
	}

	// Check if any priority label already exists
	for _, l := range issue.Labels {
		lower := strings.ToLower(l.Name)
		if lower == "p1" || lower == "p2" || lower == "p3" || lower == "p4" {
			return // Already has a priority label
		}
	}

	if dryRun {
		return
	}

	cmd := exec.Command("gh", "issue", "edit",
		fmt.Sprintf("%d", issue.Number),
		"--repo", defaultGHRepo,
		"--add-label", wantLabel,
	)
	if err := cmd.Run(); err != nil {
		if !jsonOut {
			fmt.Printf("    %s label sync: %v\n", color.YellowString("Warning:"), err)
		}
	}
}

func init() {
	githubSyncCmd.Flags().BoolVar(&githubSyncDryRun, "dry-run", false, "Show what would be synced without making changes")
	githubSyncCmd.Flags().BoolVar(&githubSyncAll, "all", false, "Include closed issues (marks their tasks as done)")
	githubSyncCmd.Flags().BoolVar(&githubSyncJSON, "json", false, "Output structured JSON (for orchestrator)")
	githubCmd.AddCommand(githubSyncCmd)
}
