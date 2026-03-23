package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/spf13/cobra"
)

var (
	heartbeatJSON   bool
	heartbeatWindow string
)

var heartbeatCmd = &cobra.Command{
	Use:   "heartbeat",
	Short: "Dev tracking heartbeat for WhatsApp standup",
	Long: `Collect sprint status, recent PRs, and issue activity
into a concise summary for WhatsApp standup.

Includes:
  - Active sprint progress (tasks by status)
  - PRs created/updated in window (default 30m)
  - Issues opened/closed in window
  - Action items (blockers, review needed)

With --json, outputs structured data for worker consumption.

Examples:
  lw heartbeat
  lw heartbeat --window=1h
  lw heartbeat --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		return runHeartbeat(ctx, heartbeatWindow, heartbeatJSON)
	},
}

type heartbeatData struct {
	Timestamp   string        `json:"timestamp"`
	Window      string        `json:"window"`
	Sprint      *sprintStatus `json:"sprint,omitempty"`
	PRs         []prSummary   `json:"prs"`
	Issues      issueSummary  `json:"issues"`
	ActionItems []string      `json:"action_items,omitempty"`
	Warnings    []string      `json:"warnings,omitempty"`
	WhatsApp    string        `json:"whatsapp,omitempty"`
}

type sprintStatus struct {
	Name       string `json:"name"`
	ID         string `json:"id"`
	InProgress int    `json:"in_progress"`
	Done       int    `json:"done"`
	Approved   int    `json:"approved"`
	Blocked    int    `json:"blocked"`
	Total      int    `json:"total"`
}

type prSummary struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Branch    string `json:"branch"`
	UpdatedAt string `json:"updated_at"`
}

type issueSummary struct {
	Opened []issueRef `json:"opened,omitempty"`
	Closed []issueRef `json:"closed,omitempty"`
}

type issueRef struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

func runHeartbeat(ctx context.Context, windowStr string, jsonOut bool) error {
	window, err := time.ParseDuration(windowStr)
	if err != nil {
		return fmt.Errorf("invalid window: %w", err)
	}

	data := heartbeatData{
		Timestamp: time.Now().Format("2006-01-02 15:04"),
		Window:    windowStr,
	}

	// 1. Sprint status
	data.Sprint, err = collectSprintStatus(ctx)
	if err != nil {
		data.Warnings = append(data.Warnings, fmt.Sprintf("Sprint data unavailable: %v", err))
	}

	// 2. Recent PRs
	data.PRs, err = collectRecentPRs(window)
	if err != nil {
		data.Warnings = append(data.Warnings, fmt.Sprintf("PR data unavailable: %v", err))
	}

	// 3. Recent issue activity
	data.Issues = collectIssueActivity(window)

	// 4. Action items
	data.ActionItems = deriveActionItems(data)

	// 5. Format WhatsApp message
	data.WhatsApp = formatWhatsApp(data)

	if jsonOut {
		out, _ := json.MarshalIndent(data, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	fmt.Println(data.WhatsApp)
	return nil
}

func collectSprintStatus(ctx context.Context) (*sprintStatus, error) {
	pool, err := db.GetPool(ctx)
	if err != nil {
		return nil, fmt.Errorf("DB connection failed: %v", err)
	}

	sprints, err := db.ListSprints(ctx, pool, db.SprintListOptions{
		Status: "active",
		Limit:  1,
	})
	if err != nil {
		return nil, fmt.Errorf("listing sprints: %v", err)
	}
	if len(sprints) == 0 {
		return nil, nil // no active sprint is not an error
	}

	sprint := sprints[0]
	tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{
		SprintID: sprint.ID,
		Limit:    200,
	})
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %v", err)
	}

	ss := &sprintStatus{
		Name:  sprint.Name,
		ID:    sprint.ShortID,
		Total: len(tasks),
	}
	for _, t := range tasks {
		switch t.Status {
		case "in_progress":
			ss.InProgress++
		case "done", "archived":
			ss.Done++
		case "approved", "next_up":
			ss.Approved++
		case "blocked", "on_hold":
			ss.Blocked++
		}
	}
	return ss, nil
}

func collectRecentPRs(window time.Duration) ([]prSummary, error) {
	cmd := exec.Command("gh", "pr", "list",
		"--repo", defaultGHRepo,
		"--state", "all",
		"--json", "number,title,state,headRefName,updatedAt",
		"--limit", "20",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh pr list failed: %v", err)
	}

	var prs []struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		State       string `json:"state"`
		HeadRefName string `json:"headRefName"`
		UpdatedAt   string `json:"updatedAt"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parsing PR list: %v", err)
	}

	cutoff := time.Now().Add(-window)
	var recent []prSummary
	for _, pr := range prs {
		updated, err := time.Parse(time.RFC3339, pr.UpdatedAt)
		if err != nil {
			continue
		}
		if updated.After(cutoff) {
			recent = append(recent, prSummary{
				Number:    pr.Number,
				Title:     pr.Title,
				State:     pr.State,
				Branch:    pr.HeadRefName,
				UpdatedAt: pr.UpdatedAt,
			})
		}
	}
	return recent, nil
}

func collectIssueActivity(window time.Duration) issueSummary {
	var summary issueSummary
	cutoff := time.Now().Add(-window)

	// Recently opened
	cmd := exec.Command("gh", "issue", "list",
		"--repo", defaultGHRepo,
		"--state", "open",
		"--json", "number,title,createdAt",
		"--limit", "20",
	)
	if out, err := cmd.CombinedOutput(); err == nil {
		var issues []struct {
			Number    int    `json:"number"`
			Title     string `json:"title"`
			CreatedAt string `json:"createdAt"`
		}
		if json.Unmarshal(out, &issues) == nil {
			for _, i := range issues {
				created, err := time.Parse(time.RFC3339, i.CreatedAt)
				if err != nil {
					continue
				}
				if created.After(cutoff) {
					summary.Opened = append(summary.Opened, issueRef{
						Number: i.Number,
						Title:  stripSprintPrefix(i.Title),
					})
				}
			}
		}
	}

	// Recently closed
	cmd = exec.Command("gh", "issue", "list",
		"--repo", defaultGHRepo,
		"--state", "closed",
		"--json", "number,title,closedAt",
		"--limit", "20",
	)
	if out, err := cmd.CombinedOutput(); err == nil {
		var issues []struct {
			Number   int    `json:"number"`
			Title    string `json:"title"`
			ClosedAt string `json:"closedAt"`
		}
		if json.Unmarshal(out, &issues) == nil {
			for _, i := range issues {
				closed, err := time.Parse(time.RFC3339, i.ClosedAt)
				if err != nil {
					continue
				}
				if closed.After(cutoff) {
					summary.Closed = append(summary.Closed, issueRef{
						Number: i.Number,
						Title:  stripSprintPrefix(i.Title),
					})
				}
			}
		}
	}

	return summary
}

func deriveActionItems(data heartbeatData) []string {
	var items []string

	if data.Sprint != nil && data.Sprint.Blocked > 0 {
		items = append(items, fmt.Sprintf("%d task(s) blocked in sprint", data.Sprint.Blocked))
	}

	for _, pr := range data.PRs {
		if pr.State == "OPEN" {
			items = append(items, fmt.Sprintf("PR #%d needs review: %s", pr.Number, truncate(pr.Title, 40)))
		}
	}

	return items
}

func formatWhatsApp(data heartbeatData) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("*Dev Heartbeat — %s*\n\n", data.Timestamp))

	// Sprint
	if data.Sprint != nil {
		s := data.Sprint
		b.WriteString(fmt.Sprintf("*Sprint:* %s\n", s.Name))
		b.WriteString(fmt.Sprintf("  Done: %d | In Progress: %d | Queued: %d", s.Done, s.InProgress, s.Approved))
		if s.Blocked > 0 {
			b.WriteString(fmt.Sprintf(" | Blocked: %d", s.Blocked))
		}
		b.WriteString(fmt.Sprintf(" (%d total)\n\n", s.Total))
	} else {
		b.WriteString("*Sprint:* No active sprint\n\n")
	}

	// PRs
	if len(data.PRs) > 0 {
		b.WriteString(fmt.Sprintf("*PRs* (last %s):\n", data.Window))
		for _, pr := range data.PRs {
			stateIcon := "~"
			if strings.EqualFold(pr.State, "merged") {
				stateIcon = "+"
			} else if strings.EqualFold(pr.State, "closed") {
				stateIcon = "x"
			}
			b.WriteString(fmt.Sprintf("  %s #%d %s\n", stateIcon, pr.Number, truncate(pr.Title, 45)))
		}
		b.WriteString("\n")
	}

	// Issues
	if len(data.Issues.Opened) > 0 || len(data.Issues.Closed) > 0 {
		b.WriteString(fmt.Sprintf("*Issues* (last %s):\n", data.Window))
		for _, i := range data.Issues.Opened {
			b.WriteString(fmt.Sprintf("  + #%d %s\n", i.Number, truncate(i.Title, 45)))
		}
		for _, i := range data.Issues.Closed {
			b.WriteString(fmt.Sprintf("  x #%d %s\n", i.Number, truncate(i.Title, 45)))
		}
		b.WriteString("\n")
	}

	// Warnings
	if len(data.Warnings) > 0 {
		b.WriteString("*Warnings:*\n")
		for _, w := range data.Warnings {
			b.WriteString(fmt.Sprintf("  ! %s\n", w))
		}
		b.WriteString("\n")
	}

	// Action items
	if len(data.ActionItems) > 0 {
		b.WriteString("*Action Required:*\n")
		for _, item := range data.ActionItems {
			b.WriteString(fmt.Sprintf("  - %s\n", item))
		}
	} else {
		b.WriteString("No action required.")
	}

	return b.String()
}

func init() {
	heartbeatCmd.Flags().BoolVar(&heartbeatJSON, "json", false, "Output structured JSON")
	heartbeatCmd.Flags().StringVar(&heartbeatWindow, "window", "30m", "Time window for recent activity (e.g. 30m, 1h)")
}
