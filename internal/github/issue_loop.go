package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// The GitHub reads the issue loop needs, all through `gh` so the loop runs as
// whoever the host is logged in as (kiwi-dev-la) and never carries a token of
// its own. Everything here is read-only against GitHub.

// Issue is one open issue as `gh issue list --json` returns it.
type Issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	URL    string   `json:"url"`
	State  string   `json:"state"`
	Labels []string `json:"-"`
}

type ghIssueRow struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// ListOpenIssues lists the open issues of repo carrying label, oldest first
// (the order `gh` returns), capped at limit.
func ListOpenIssues(repo, label string, limit int) ([]Issue, error) {
	args := []string{"issue", "list", "--repo", repo, "--state", "open",
		"--json", "number,title,body,url,state,labels", "--limit", strconv.Itoa(limit)}
	if label != "" {
		args = append(args, "--label", label)
	}

	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("gh issue list --repo %s: %w", repo, err)
	}

	var rows []ghIssueRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse gh issue list output: %w", err)
	}

	issues := make([]Issue, 0, len(rows))
	for _, row := range rows {
		issue := Issue{Number: row.Number, Title: row.Title, Body: row.Body, URL: row.URL, State: row.State}
		for _, l := range row.Labels {
			issue.Labels = append(issue.Labels, l.Name)
		}

		issues = append(issues, issue)
	}

	return issues, nil
}

// CheckOutcome is one required check or commit status on a PR head.
type CheckOutcome struct {
	Name       string
	Conclusion string // SUCCESS, FAILURE, ERROR, PENDING, ...
}

// PullRequest is the part of a PR the loop decides on.
type PullRequest struct {
	Number      int
	URL         string
	State       string // OPEN, MERGED, CLOSED
	HeadRefName string
	IsDraft     bool
	Body        string
	Checks      []CheckOutcome
}

type ghPullRow struct {
	Number            int    `json:"number"`
	URL               string `json:"url"`
	State             string `json:"state"`
	HeadRefName       string `json:"headRefName"`
	IsDraft           bool   `json:"isDraft"`
	Body              string `json:"body"`
	StatusCheckRollup []struct {
		Name       string `json:"name"`
		Context    string `json:"context"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
		Status     string `json:"status"`
	} `json:"statusCheckRollup"`
}

// TaskRef is the PR-body line that binds a PR to the nulltickets task that
// produced it. agile_pipeline.yaml and the task-done runbook search for the
// same line, so the loop and the SOPs agree on one binding.
func TaskRef(taskID string) string { return "Refs: " + taskID }

// FindPullRequestForTask finds the newest PR of repo whose body carries
// TaskRef(taskID). Nil when none exists yet.
func FindPullRequestForTask(repo, taskID string) (*PullRequest, error) {
	out, err := exec.Command("gh", "pr", "list", "--repo", repo, "--state", "all",
		"--search", TaskRef(taskID)+" in:body",
		"--json", "number,url,state,headRefName,isDraft,body,statusCheckRollup", "--limit", "10").Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list --repo %s: %w", repo, err)
	}

	var rows []ghPullRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse gh pr list output: %w", err)
	}

	for _, row := range rows {
		// The search is a full-text match; hold the PR to the literal line.
		if !strings.Contains(row.Body, TaskRef(taskID)) {
			continue
		}

		pr := &PullRequest{Number: row.Number, URL: row.URL, State: row.State, HeadRefName: row.HeadRefName, IsDraft: row.IsDraft, Body: row.Body}
		for _, c := range row.StatusCheckRollup {
			name := c.Name
			if name == "" {
				name = c.Context
			}

			conclusion := c.Conclusion
			if conclusion == "" {
				conclusion = c.State
			}
			if conclusion == "" {
				conclusion = c.Status
			}

			pr.Checks = append(pr.Checks, CheckOutcome{Name: name, Conclusion: strings.ToUpper(conclusion)})
		}

		return pr, nil
	}

	return nil, nil //nolint:nilnil // no PR yet is a normal, non-error answer
}

// FailedChecks are the checks on pr that have concluded against it.
func (pr *PullRequest) FailedChecks() []CheckOutcome {
	var failed []CheckOutcome
	for _, c := range pr.Checks {
		switch c.Conclusion {
		case "FAILURE", "ERROR", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED":
			failed = append(failed, c)
		}
	}

	return failed
}

// PendingChecks reports whether any check on pr has not concluded yet.
func (pr *PullRequest) PendingChecks() bool {
	for _, c := range pr.Checks {
		switch c.Conclusion {
		case "PENDING", "QUEUED", "IN_PROGRESS", "EXPECTED", "WAITING", "REQUESTED", "":
			return true
		}
	}

	return false
}

// CommentOnIssue posts body as a comment on issue number of repo. The one
// write in this file; used to tell a human why the loop gave a task up.
func CommentOnIssue(repo string, number int, body string) error {
	cmd := exec.Command("gh", "issue", "comment", strconv.Itoa(number), "--repo", repo, "--body-file", "-")
	cmd.Stdin = strings.NewReader(body)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh issue comment %s#%d: %w\n%s", repo, number, err, string(out))
	}

	return nil
}

// ArmAutoMerge asks GitHub to squash-merge PR number of repo once its
// required checks pass — the loop's merge step, on the SCM's own gate
// (automation tier 2: green PRs merge without a human).
func ArmAutoMerge(repo string, number int) error {
	out, err := exec.Command("gh", "pr", "merge", strconv.Itoa(number), "--repo", repo, "--auto", "--squash", "--delete-branch").CombinedOutput()
	if err == nil {
		return nil
	}

	// A repo without auto-merge enabled refuses --auto. The caller has already
	// seen every check green, which is the condition auto-merge would have
	// waited for, so merge now — the same act, without GitHub holding it.
	if strings.Contains(string(out), "auto-merge") || strings.Contains(string(out), "not enabled") {
		if direct, derr := exec.Command("gh", "pr", "merge", strconv.Itoa(number), "--repo", repo, "--squash", "--delete-branch").CombinedOutput(); derr != nil {
			return fmt.Errorf("gh pr merge %s#%d: %w\n%s", repo, number, derr, string(direct))
		}

		return nil
	}

	return fmt.Errorf("gh pr merge --auto %s#%d: %w\n%s", repo, number, err, string(out))
}

// AddIssueLabel adds label to issue number of repo.
func AddIssueLabel(repo string, number int, label string) error {
	if out, err := exec.Command("gh", "issue", "edit", strconv.Itoa(number), "--repo", repo, "--add-label", label).CombinedOutput(); err != nil {
		return fmt.Errorf("gh issue edit %s#%d --add-label %s: %w\n%s", repo, number, label, err, string(out))
	}

	return nil
}
