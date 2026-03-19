package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

const (
	DefaultRepo = "lightwave-media/lightwave-platform"
	DefaultOrg  = "lightwave-media"
)

// TaskInfo holds the minimal task data needed for issue creation.
type TaskInfo struct {
	ShortID      string
	Title        string
	Description  string
	Priority     string
	TaskType     string
	TaskCategory string
}

// IssueRef holds the URL and number of a GitHub issue.
type IssueRef struct {
	URL    string `json:"url"`
	Number int    `json:"number"`
}

// CreateIssue creates a GitHub issue for a task using the gh CLI.
// Returns the issue URL.
func CreateIssue(repo string, task TaskInfo) (string, error) {
	body := buildIssueBody(task)

	args := []string{"issue", "create",
		"--repo", repo,
		"--title", fmt.Sprintf("[%s] %s", task.ShortID, task.Title),
		"--body", body,
	}

	labels := buildLabels(task)
	if len(labels) > 0 {
		args = append(args, "--label", strings.Join(labels, ","))
	}

	cmd := exec.Command("gh", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("gh issue create failed: %s", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("gh issue create failed: %w", err)
	}

	// gh issue create outputs the URL on stdout
	issueURL := strings.TrimSpace(string(out))
	return issueURL, nil
}

// AddToProject adds an issue to a GitHub org project by URL.
func AddToProject(org string, projectNumber int, issueURL string) error {
	cmd := exec.Command("gh", "project", "item-add",
		fmt.Sprintf("%d", projectNumber),
		"--owner", org,
		"--url", issueURL,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh project item-add failed: %s", strings.TrimSpace(string(out)))
	}

	return nil
}

// SyncSprintTasks creates GitHub issues for each task and adds them to the org project.
// Deduplicates by checking existing open issues for [shortID] title prefix.
// Returns a map of task ShortID → IssueRef for tasks that were created or already existed.
func SyncSprintTasks(repo, org string, projectNumber int, tasks []TaskInfo) (map[string]IssueRef, error) {
	results := make(map[string]IssueRef)
	var errors []string

	// Fetch existing open issues to dedup
	existing, err := fetchExistingIssues(repo)
	if err != nil {
		// Non-fatal — fall through to create all
		existing = map[string]IssueRef{}
	}

	for _, task := range tasks {
		// Dedup: skip if issue already exists for this task
		if ref, ok := existing[task.ShortID]; ok {
			results[task.ShortID] = ref
			continue
		}

		issueURL, err := CreateIssue(repo, task)
		if err != nil {
			errors = append(errors, fmt.Sprintf("  %s: %v", task.ShortID, err))
			continue
		}

		num := issueNumberFromURL(issueURL)
		results[task.ShortID] = IssueRef{URL: issueURL, Number: num}

		if projectNumber > 0 {
			if err := AddToProject(org, projectNumber, issueURL); err != nil {
				errors = append(errors, fmt.Sprintf("  %s (project): %v", task.ShortID, err))
			}
		}
	}

	if len(errors) > 0 {
		return results, fmt.Errorf("some tasks failed:\n%s", strings.Join(errors, "\n"))
	}

	return results, nil
}

// fetchExistingIssues returns open issues indexed by [shortID] title prefix.
func fetchExistingIssues(repo string) (map[string]IssueRef, error) {
	cmd := exec.Command("gh", "issue", "list",
		"--repo", repo,
		"--state", "open",
		"--json", "number,title,url",
		"--limit", "200",
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh issue list: %w", err)
	}

	var issues []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("parse issues: %w", err)
	}

	result := make(map[string]IssueRef)
	for _, iss := range issues {
		// Extract [shortID] from title prefix like "[abc12345] Task title"
		if strings.HasPrefix(iss.Title, "[") {
			end := strings.Index(iss.Title, "]")
			if end > 1 {
				shortID := iss.Title[1:end]
				result[shortID] = IssueRef{URL: iss.URL, Number: iss.Number}
			}
		}
	}

	return result, nil
}

// issueNumberFromURL extracts the issue number from a GitHub issue URL.
// e.g. "https://github.com/owner/repo/issues/123" → 123
func issueNumberFromURL(url string) int {
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return 0
	}
	var num int
	fmt.Sscanf(parts[len(parts)-1], "%d", &num)
	return num
}

// ListProjectItems returns issue URLs already in a project (to avoid duplicates).
func ListProjectItems(org string, projectNumber int) (map[string]bool, error) {
	cmd := exec.Command("gh", "project", "item-list",
		fmt.Sprintf("%d", projectNumber),
		"--owner", org,
		"--format", "json",
		"--limit", "200",
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh project item-list failed: %w", err)
	}

	var result struct {
		Items []struct {
			Content struct {
				Title string `json:"title"`
				URL   string `json:"url"`
			} `json:"content"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse project items: %w", err)
	}

	urls := make(map[string]bool)
	for _, item := range result.Items {
		if item.Content.URL != "" {
			urls[item.Content.URL] = true
		}
		// Also index by title prefix (task short ID)
		if item.Content.Title != "" {
			urls[item.Content.Title] = true
		}
	}

	return urls, nil
}

func buildIssueBody(task TaskInfo) string {
	var b strings.Builder
	if task.Description != "" {
		b.WriteString(task.Description)
		b.WriteString("\n\n")
	}
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("**Task ID:** `%s`\n", task.ShortID))
	if task.Priority != "" {
		b.WriteString(fmt.Sprintf("**Priority:** %s\n", task.Priority))
	}
	if task.TaskType != "" {
		b.WriteString(fmt.Sprintf("**Type:** %s\n", task.TaskType))
	}
	if task.TaskCategory != "" {
		b.WriteString(fmt.Sprintf("**Category:** %s\n", task.TaskCategory))
	}
	return b.String()
}

func buildLabels(task TaskInfo) []string {
	var labels []string
	if task.Priority != "" {
		labels = append(labels, priorityToLabel(task.Priority))
	}
	if task.TaskCategory != "" {
		labels = append(labels, task.TaskCategory)
	}
	return labels
}

// priorityToLabel maps DB priority values to GitHub label names.
// DB stores "p1_urgent", "p2_high", etc. GitHub labels are "p1", "p2", etc.
func priorityToLabel(priority string) string {
	switch {
	case strings.HasPrefix(priority, "p1"):
		return "p1"
	case strings.HasPrefix(priority, "p2"):
		return "p2"
	case strings.HasPrefix(priority, "p3"):
		return "p3"
	case strings.HasPrefix(priority, "p4"):
		return "p4"
	default:
		return priority
	}
}
