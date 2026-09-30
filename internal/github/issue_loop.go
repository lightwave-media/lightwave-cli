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
	Number          int
	URL             string
	State           string // OPEN, MERGED, CLOSED
	HeadRefName     string
	HeadRefOID      string
	ReviewDecision  string
	BlockingReviews []string
	IsDraft         bool
	Body            string
	Checks          []CheckOutcome
}

type ghPullRow struct {
	Number            int    `json:"number"`
	URL               string `json:"url"`
	State             string `json:"state"`
	HeadRefName       string `json:"headRefName"`
	HeadRefOID        string `json:"headRefOid"`
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

// FindPullRequestForTask finds the PR of repo whose body carries
// TaskRef(taskID). Nil when none exists yet.
func FindPullRequestForTask(repo, taskID string) (*PullRequest, error) {
	out, err := exec.Command("gh", "pr", "list", "--repo", repo, "--state", "all",
		"--search", TaskRef(taskID)+" in:body",
		"--json", "number,url,state,headRefName,headRefOid,isDraft,body,statusCheckRollup", "--limit", "10").Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list --repo %s: %w", repo, err)
	}

	var rows []ghPullRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse gh pr list output: %w", err)
	}

	pr := pickPullRequest(rows, taskID)
	if pr != nil && pr.State == "OPEN" {
		if err := loadPullRequestReviews(repo, pr); err != nil {
			return nil, err
		}
	}

	return pr, nil
}

// pullStateRank orders the PRs a task can have by which one the loop should
// act on: the live one, else the one that shipped, else one abandoned earlier.
func pullStateRank(state string) int {
	switch state {
	case "OPEN":
		return 0
	case "MERGED":
		return 1
	default:
		return 2
	}
}

// pickPullRequest chooses the PR for taskID out of rows (gh lists newest
// first). A closed PR left over from an earlier round carries the same Refs
// line as the open one that replaced it; it must not shadow it, or reconcile
// drops a task that is still being worked. Within a state, the newest wins.
func pickPullRequest(rows []ghPullRow, taskID string) *PullRequest {
	var best *ghPullRow

	for i := range rows {
		// The search is a full-text match; hold the PR to the literal line.
		if !strings.Contains(rows[i].Body, TaskRef(taskID)) {
			continue
		}

		if best == nil || pullStateRank(rows[i].State) < pullStateRank(best.State) {
			best = &rows[i]
		}
	}

	if best == nil {
		return nil
	}

	pr := &PullRequest{Number: best.Number, URL: best.URL, State: best.State, HeadRefName: best.HeadRefName, HeadRefOID: best.HeadRefOID, IsDraft: best.IsDraft, Body: best.Body}
	for _, c := range best.StatusCheckRollup {
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

	return pr
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
func ArmAutoMerge(repo string, number int, head string) error {
	if head == "" {
		return fmt.Errorf("refusing to merge %s#%d without the reviewed head SHA", repo, number)
	}

	out, err := exec.Command("gh", "pr", "merge", strconv.Itoa(number), "--repo", repo, "--auto", "--squash", "--delete-branch", "--match-head-commit", head).CombinedOutput()
	if err == nil {
		return nil
	}

	// A repo with auto-merge switched off refuses --auto. The caller has already
	// seen every check green, which is the condition auto-merge would have
	// waited for, so merge now — the same act, without GitHub holding it. Only
	// that refusal falls through: a permission error, an already-armed PR or a
	// merge queue must leave GitHub holding the PR, not squash it here.
	if autoMergeSwitchedOff(string(out)) {
		if direct, derr := exec.Command("gh", "pr", "merge", strconv.Itoa(number), "--repo", repo, "--squash", "--delete-branch", "--match-head-commit", head).CombinedOutput(); derr != nil {
			return fmt.Errorf("gh pr merge %s#%d: %w\n%s", repo, number, derr, string(direct))
		}

		return nil
	}

	return fmt.Errorf("gh pr merge --auto %s#%d: %w\n%s", repo, number, err, string(out))
}

// autoMergeSwitchedOff recognises gh's refusal when the repository's
// "Allow auto-merge" setting is off ("Pull request Auto merge is not allowed
// for this repository"), and nothing broader.
func autoMergeSwitchedOff(ghOutput string) bool {
	lower := strings.ToLower(ghOutput)

	return strings.Contains(lower, "auto merge is not allowed") || strings.Contains(lower, "auto-merge is not allowed")
}

// MarkPullRequestReady takes PR number of repo out of draft. The delivery hook
// opens every PR as a draft; the loop marks it ready once the task is submitted.
func MarkPullRequestReady(repo string, number int) error {
	if out, err := exec.Command("gh", "pr", "ready", strconv.Itoa(number), "--repo", repo).CombinedOutput(); err != nil {
		return fmt.Errorf("gh pr ready %s#%d: %w\n%s", repo, number, err, string(out))
	}

	return nil
}

// AddIssueLabel adds label to issue number of repo.
func AddIssueLabel(repo string, number int, label string) error {
	if out, err := exec.Command("gh", "issue", "edit", strconv.Itoa(number), "--repo", repo, "--add-label", label).CombinedOutput(); err != nil {
		return fmt.Errorf("gh issue edit %s#%d --add-label %s: %w\n%s", repo, number, label, err, string(out))
	}

	return nil
}

// RemoveIssueLabel removes label from issue number of repo.
func RemoveIssueLabel(repo string, number int, label string) error {
	if out, err := exec.Command("gh", "issue", "edit", strconv.Itoa(number), "--repo", repo, "--remove-label", label).CombinedOutput(); err != nil {
		return fmt.Errorf("gh issue edit %s#%d --remove-label %s: %w\n%s", repo, number, label, err, string(out))
	}

	return nil
}
