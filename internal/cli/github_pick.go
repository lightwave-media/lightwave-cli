package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/fatih/color"
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

Returns the highest-priority "ready" issue. Used by the orchestrator
as the primary task picker, replacing lw task next-approved.

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

// pickedIssue is the JSON output for orchestrator consumption
type pickedIssue struct {
	Found     bool     `json:"found"`
	Number    int      `json:"number,omitempty"`
	Title     string   `json:"title,omitempty"`
	URL       string   `json:"url,omitempty"`
	TaskID    string   `json:"task_id,omitempty"`
	Priority  string   `json:"priority,omitempty"`
	TaskType  string   `json:"task_type,omitempty"`
	Epic      string   `json:"epic,omitempty"`
	Deps      []string `json:"deps,omitempty"`
	HasAC     bool     `json:"has_ac,omitempty"`
	Milestone string   `json:"milestone,omitempty"`
	Labels    []string `json:"labels,omitempty"`
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

	// Sort by priority: p1 > p2 > p3 > p4 > unlabeled
	sort.SliceStable(issues, func(i, j int) bool {
		return issuePriorityRank(issues[i]) < issuePriorityRank(issues[j])
	})

	picked := issues[0]
	fields := parseIssueBody(picked)
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
			Found:     true,
			Number:    picked.Number,
			Title:     title,
			URL:       picked.URL,
			TaskID:    fields.taskID,
			Priority:  fields.priority,
			TaskType:  fields.taskType,
			Epic:      fields.epic,
			Deps:      fields.deps,
			HasAC:     fields.acceptanceCriteria != "",
			Milestone: ms,
			Labels:    labels,
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
		fmt.Printf("  Deps: %s\n", strings.Join(fields.deps, ", "))
	}
	if fields.acceptanceCriteria != "" {
		fmt.Printf("  AC: %s\n", color.GreenString("yes"))
	}

	if len(issues) > 1 {
		fmt.Printf("\n  %s other ready issues in queue\n", color.HiBlackString("%d", len(issues)-1))
	}

	return nil
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

func init() {
	githubPickCmd.Flags().StringVar(&githubPickMilestone, "milestone", "", "Filter by milestone (e.g. 'Sprint 6')")
	githubPickCmd.Flags().BoolVar(&githubPickJSON, "json", false, "Output structured JSON (for orchestrator)")

	githubCmd.AddCommand(githubPickCmd)
}
