package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/spf13/cobra"
)

var (
	githubPickMilestone string
	githubPickJSON      bool
)

var githubPickCmd = &cobra.Command{
	Use:   "pick",
	Short: "Pick next task from GitHub Issues",
	Long: `Query GitHub Issues to find the next task ready for work.

Filters:
  - Label "ready" required (marks an issue as eligible for pickup)
  - Milestone filter (optional, matches sprint milestone)
  - Priority ordering: p1 > p2 > p3 > p4 (via issue labels)
  - Dependency check: skips issues whose deps are not yet done

Returns the highest-priority "ready" issue with satisfied dependencies.
Used by the orchestrator as the primary task picker.

With --json, outputs structured JSON for orchestrator consumption.

Examples:
  lw github pick
  lw github pick --milestone="Sprint 6"
  lw github pick --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return runGitHubPick(ctx, githubPickMilestone, githubPickJSON)
	},
}

var githubQueueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Show all ready issues sorted by priority",
	Long: `Display the full queue of GitHub Issues labeled "ready",
sorted by priority with dependency status.

Examples:
  lw github queue
  lw github queue --milestone="Sprint 6"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return runGitHubQueue(ctx, githubPickMilestone)
	},
}

var githubReadyCmd = &cobra.Command{
	Use:   "ready <issue-number> [priority]",
	Short: "Mark a GitHub Issue as ready for pickup",
	Long: `Add the "ready" label (and optional priority label) to a GitHub Issue.

Priority labels: p1, p2, p3, p4

Examples:
  lw github ready 58
  lw github ready 58 p1
  lw github ready 58 p2`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		issueNum := args[0]
		labels := []string{"ready"}
		if len(args) > 1 {
			pLabel := strings.ToLower(args[1])
			if pLabel == "p1" || pLabel == "p2" || pLabel == "p3" || pLabel == "p4" {
				labels = append(labels, pLabel)
			} else {
				return fmt.Errorf("invalid priority %q — use p1, p2, p3, or p4", args[1])
			}
		}
		return markIssueReady(issueNum, labels)
	},
}

// pickedIssue is the JSON output for orchestrator consumption
type pickedIssue struct {
	Found      bool     `json:"found"`
	Number     int      `json:"number,omitempty"`
	Title      string   `json:"title,omitempty"`
	URL        string   `json:"url,omitempty"`
	TaskID     string   `json:"task_id,omitempty"`
	Priority   string   `json:"priority,omitempty"`
	TaskType   string   `json:"task_type,omitempty"`
	Epic       string   `json:"epic,omitempty"`
	Deps       []string `json:"deps,omitempty"`
	DepsStatus string   `json:"deps_status,omitempty"` // "satisfied", "blocked", "unknown"
	HasAC      bool     `json:"has_ac,omitempty"`
	Milestone  string   `json:"milestone,omitempty"`
	Labels     []string `json:"labels,omitempty"`
}

// pickNextReady returns the highest-priority ready issue with satisfied deps.
// Used by both the CLI command and the orchestrator.
func pickNextReady(ctx context.Context, milestone string) (*ghIssue, error) {
	issues, err := fetchReadyIssues(milestone)
	if err != nil {
		return nil, err
	}
	if len(issues) == 0 {
		return nil, nil
	}

	// Sort by priority
	sortByPriority(issues)

	// Find first issue with satisfied dependencies
	pool, _ := db.GetPool(ctx)
	for i := range issues {
		if depsOK(ctx, pool, issues[i]) {
			return &issues[i], nil
		}
	}

	return nil, nil
}

func runGitHubPick(ctx context.Context, milestone string, jsonOut bool) error {
	issues, err := fetchReadyIssues(milestone)
	if err != nil {
		return fmt.Errorf("fetching ready issues: %w", err)
	}

	if len(issues) == 0 {
		if jsonOut {
			out, _ := json.MarshalIndent(pickedIssue{Found: false}, "", "  ")
			fmt.Println(string(out))
			return nil
		}
		fmt.Println(color.YellowString("No ready issues found"))
		if milestone != "" {
			fmt.Printf("  milestone: %s\n", milestone)
		}
		return nil
	}

	sortByPriority(issues)

	// Check deps to find the first actionable issue
	pool, _ := db.GetPool(ctx)
	var picked *ghIssue
	var skippedBlocked int
	for i := range issues {
		if depsOK(ctx, pool, issues[i]) {
			picked = &issues[i]
			break
		}
		skippedBlocked++
	}

	if picked == nil {
		if jsonOut {
			out, _ := json.MarshalIndent(pickedIssue{Found: false}, "", "  ")
			fmt.Println(string(out))
			return nil
		}
		fmt.Printf("%s — %d ready issues all blocked by unresolved deps\n",
			color.YellowString("No actionable issues"), len(issues))
		return nil
	}

	fields := parseIssueBody(*picked)
	title := stripSprintPrefix(picked.Title)

	if jsonOut {
		var labels []string
		for _, l := range picked.Labels {
			labels = append(labels, l.Name)
		}
		ms := ""
		if picked.Milestone != nil {
			ms = picked.Milestone.Title
		}
		out, _ := json.MarshalIndent(pickedIssue{
			Found:      true,
			Number:     picked.Number,
			Title:      title,
			URL:        picked.URL,
			TaskID:     fields.taskID,
			Priority:   fields.priority,
			TaskType:   fields.taskType,
			Epic:       fields.epic,
			Deps:       fields.deps,
			DepsStatus: "satisfied",
			HasAC:      fields.acceptanceCriteria != "",
			Milestone:  ms,
			Labels:     labels,
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	// Human-readable output
	fmt.Println(color.GreenString("Next ready issue:"))
	fmt.Printf("  #%d: %s\n", picked.Number, color.CyanString(title))
	fmt.Printf("  URL: %s\n", picked.URL)
	if fields.taskID != "" {
		fmt.Printf("  Task ID: %s\n", color.YellowString(fields.taskID))
	}
	if fields.priority != "" {
		fmt.Printf("  Priority: %s\n", fields.priority)
	}
	if fields.epic != "" {
		fmt.Printf("  Epic: %s\n", fields.epic)
	}
	if fields.taskType != "" {
		fmt.Printf("  Type: %s\n", fields.taskType)
	}
	if len(fields.deps) > 0 {
		fmt.Printf("  Deps: %s\n", color.GreenString(strings.Join(fields.deps, ", ")+" (satisfied)"))
	}
	if fields.acceptanceCriteria != "" {
		fmt.Printf("  AC: %s\n", color.GreenString("yes"))
	}

	queueSize := len(issues) - 1 - skippedBlocked
	if skippedBlocked > 0 {
		fmt.Printf("\n  %s blocked by deps, %s actionable in queue\n",
			color.YellowString("%d", skippedBlocked),
			color.HiBlackString("%d", queueSize))
	} else if queueSize > 0 {
		fmt.Printf("\n  %s other ready issues in queue\n", color.HiBlackString("%d", queueSize))
	}

	return nil
}

func runGitHubQueue(ctx context.Context, milestone string) error {
	issues, err := fetchReadyIssues(milestone)
	if err != nil {
		return fmt.Errorf("fetching ready issues: %w", err)
	}

	if len(issues) == 0 {
		fmt.Println(color.YellowString("No ready issues in queue"))
		return nil
	}

	sortByPriority(issues)

	pool, _ := db.GetPool(ctx)

	fmt.Printf(color.CyanString("Ready Queue")+" (%d issues)\n\n", len(issues))
	for i, issue := range issues {
		fields := parseIssueBody(issue)
		title := stripSprintPrefix(issue.Title)
		rank := issuePriorityRank(issue)
		priStr := fmt.Sprintf("p%d", rank)
		if rank == 5 {
			priStr = "  "
		}

		ok := depsOK(ctx, pool, issue)
		statusIcon := color.GreenString("*")
		if !ok {
			statusIcon = color.YellowString("~")
		}

		fmt.Printf("  %s %d. #%-4d [%s] %s", statusIcon, i+1, issue.Number, priStr, truncate(title, 42))
		if fields.taskID != "" {
			fmt.Printf(" (%s)", color.YellowString(fields.taskID))
		}
		if !ok && len(fields.deps) > 0 {
			fmt.Printf(" %s", color.YellowString("blocked:[%s]", strings.Join(fields.deps, ",")))
		}
		fmt.Println()
	}
	fmt.Printf("\n  %s = deps satisfied   %s = blocked by deps\n",
		color.GreenString("*"), color.YellowString("~"))

	return nil
}

// depsOK checks whether all dependencies of an issue are satisfied (done/cancelled).
// If no DB pool is available or the issue has no deps, returns true.
func depsOK(ctx context.Context, pool *pgxpool.Pool, issue ghIssue) bool {
	fields := parseIssueBody(issue)
	if len(fields.deps) == 0 {
		return true
	}

	if pool == nil {
		return true
	}

	for _, depID := range fields.deps {
		task, err := db.GetTask(ctx, pool, depID)
		if err != nil {
			// Dep not found in DB — can't verify, assume not blocking
			continue
		}
		switch task.Status {
		case "done", "cancelled", "archived":
			continue
		default:
			return false
		}
	}
	return true
}

// sortByPriority sorts issues in place by priority rank (p1 first).
func sortByPriority(issues []ghIssue) {
	sort.SliceStable(issues, func(i, j int) bool {
		return issuePriorityRank(issues[i]) < issuePriorityRank(issues[j])
	})
}

// fetchReadyIssues queries GitHub for open issues with the "ready" label.
// Optionally filters by milestone.
func fetchReadyIssues(milestone string) ([]ghIssue, error) {
	args := []string{"issue", "list",
		"--repo", defaultGHRepo,
		"--state", "open",
		"--label", "ready",
		"--json", "number,title,body,state,labels,milestone,url",
		"--limit", "50",
	}
	if milestone != "" {
		args = append(args, "--milestone", milestone)
	}

	cmd := exec.Command("gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh issue list failed: %w\n%s", err, string(out))
	}

	var issues []ghIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}

	return issues, nil
}

// issuePriorityRank returns a sort rank from issue labels.
// Lower rank = higher priority. P1=1, P2=2, P3=3, P4=4, unlabeled=5.
func issuePriorityRank(issue ghIssue) int {
	for _, l := range issue.Labels {
		lower := strings.ToLower(l.Name)
		switch {
		case strings.HasPrefix(lower, "p1"):
			return 1
		case strings.HasPrefix(lower, "p2"):
			return 2
		case strings.HasPrefix(lower, "p3"):
			return 3
		case strings.HasPrefix(lower, "p4"):
			return 4
		}
	}
	// Check body for priority field as fallback
	fields := parseIssueBody(issue)
	switch fields.priority {
	case "p1_urgent":
		return 1
	case "p2_high":
		return 2
	case "p3_medium":
		return 3
	case "p4_low":
		return 4
	}
	return 5
}

// markIssueReady adds the specified labels to a GitHub Issue.
func markIssueReady(issueNum string, labels []string) error {
	ghCmd := exec.Command("gh", "issue", "edit", issueNum,
		"--repo", defaultGHRepo,
		"--add-label", strings.Join(labels, ","),
	)
	out, err := ghCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh issue edit failed: %w\n%s", err, string(out))
	}
	fmt.Printf("Issue #%s labeled: %s\n", issueNum, color.GreenString(strings.Join(labels, ", ")))
	return nil
}

func init() {
	githubPickCmd.Flags().StringVar(&githubPickMilestone, "milestone", "", "Filter by milestone (e.g. 'Sprint 6')")
	githubPickCmd.Flags().BoolVar(&githubPickJSON, "json", false, "Output structured JSON (for orchestrator)")
	githubQueueCmd.Flags().StringVar(&githubPickMilestone, "milestone", "", "Filter by milestone")

	githubCmd.AddCommand(githubPickCmd)
	githubCmd.AddCommand(githubQueueCmd)
	githubCmd.AddCommand(githubReadyCmd)
}
