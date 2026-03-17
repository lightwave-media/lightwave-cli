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

// ghIssue represents a created GitHub issue.
type ghIssue struct {
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
// Returns a map of task ShortID → issue URL for tasks that were successfully created.
func SyncSprintTasks(repo, org string, projectNumber int, tasks []TaskInfo) (map[string]string, error) {
	results := make(map[string]string)
	var errors []string

	for _, task := range tasks {
		issueURL, err := CreateIssue(repo, task)
		if err != nil {
			errors = append(errors, fmt.Sprintf("  %s: %v", task.ShortID, err))
			continue
		}
		results[task.ShortID] = issueURL

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
		labels = append(labels, task.Priority)
	}
	if task.TaskCategory != "" {
		labels = append(labels, task.TaskCategory)
	}
	return labels
}
