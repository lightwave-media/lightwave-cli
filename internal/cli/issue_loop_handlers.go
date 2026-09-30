package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	gh "github.com/lightwave-media/lightwave-cli/internal/github"
	"github.com/lightwave-media/lightwave-cli/internal/nulltickets"
)

// The issue loop: `lw issue promote` is the GitHub -> nulltickets hop (the
// sibling of `lw knowledge promote`, Notion -> nulltickets, and of the
// lw-webhook receiver's promote_issue), and `lw issue reconcile` is its return
// path — a task in in_review is decided by the state of its PR, never by an
// agent. Both are polled from launchd, so the loop needs no public ingress and
// no webhook secret; the receiver stays the low-latency path for when those
// exist. Same idempotency key as the receiver, github:issue:<repo>#<n>, so
// both intakes are one task.

const (
	issueLoopDefaultLabel = "status:ready"
	issueLoopDefaultLimit = 50
	issueLoopMaxRounds    = 3
	issueLoopReviewStage  = "in_review"
	issueLoopAgent        = "lw-issue-reconcile"
	issueLoopLeaseTTL     = 2 * time.Minute
	issueLoopOperatorTag  = "needs-operator"

	reconcileWaitingForPR = "waiting_for_pr"
	reconcilePending      = "pending"
	reconcileApproved     = "approved"
	reconcileRejected     = "rejected"
	reconcileDropped      = "dropped"
	reconcileAutoMerge    = "auto_merge_armed"
	reconcileDryRun       = "dry_run"
	reconcileError        = "error"

	// repoKey names the repo both on the command line and in task metadata.
	repoKey = "repo"
	// Every submit and every reject bumps task_version by one, so one review
	// round is two versions.
	versionsPerReviewRound = 2
)

// Seams for tests: every GitHub read and write the loop makes.
var (
	listOpenIssues      = gh.ListOpenIssues
	findPullRequest     = gh.FindPullRequestForTask
	commentOnIssue      = gh.CommentOnIssue
	addIssueLabel       = gh.AddIssueLabel
	armPullRequestMerge = gh.ArmAutoMerge
)

func init() {
	RegisterHandler("issue.promote", issuePromoteHandler)
	RegisterHandler("issue.reconcile", issueReconcileHandler)
}

// issueBindingKey is the one binding both intakes use. Changing it here
// without changing webhook.py would let a webhook delivery and a poll of the
// same issue create two tasks.
func issueBindingKey(repo string, number int) string {
	return fmt.Sprintf("github:issue:%s#%d", repo, number)
}

type promotion struct {
	Title  string `json:"title"`
	Action string `json:"action"`
	TaskID string `json:"task_id,omitempty"`
	Reason string `json:"reason,omitempty"`
	Issue  int    `json:"issue"`
}

type promoteReport struct {
	StartedAt time.Time   `json:"started_at"`
	Repo      string      `json:"repo"`
	Pipeline  string      `json:"pipeline"`
	Label     string      `json:"label"`
	Changes   []promotion `json:"changes"`
	DryRun    bool        `json:"dry_run"`
}

func issuePromoteHandler(ctx context.Context, _ []string, flags map[string]any) error {
	repo := gh.QualifyRepo(flagStr(flags, repoKey), gh.DefaultOrg)
	if repo == "" {
		repo = gh.CurrentRepo(".")
	}

	pipeline := flagStr(flags, "pipeline")
	if repo == "" || pipeline == "" {
		return errors.New("usage: lw issue promote --repo <owner/repo> --pipeline <nulltickets_pipeline_id> [--label status:ready] [--limit N] [--dry-run] [--json]")
	}

	label := flagStrOr(flags, "label", issueLoopDefaultLabel)
	dryRun := flagBool(flags, "dry-run")

	limit, err := flagIntOr(flags, "limit", issueLoopDefaultLimit)
	if err != nil {
		return err
	}

	queue, err := issueLoopQueue(ctx, dryRun)
	if err != nil {
		return err
	}

	issues, err := listOpenIssues(repo, label, limit)
	if err != nil {
		return err
	}

	report := promoteReport{StartedAt: time.Now().UTC(), Repo: repo, Pipeline: pipeline, Label: label, DryRun: dryRun, Changes: []promotion{}}

	for _, issue := range issues {
		change := promotion{Issue: issue.Number, Title: issue.Title}

		switch {
		case dryRun:
			change.Action = reconcileDryRun
		default:
			taskID, err := queue.CreateTask(ctx, issueBindingKey(repo, issue.Number), promotionRequest(repo, pipeline, &issue))
			switch {
			case err == nil:
				change.Action, change.TaskID = "promoted", taskID
			case strings.Contains(err.Error(), "HTTP 409"):
				// The key is taken with a different body: the issue was edited
				// after promotion. The task that exists is the task.
				change.Action, change.Reason = "already_promoted", "issue changed since it was promoted; existing task stands"
			default:
				change.Action, change.Reason = reconcileError, err.Error()
			}
		}

		report.Changes = append(report.Changes, change)
	}

	return printIssueLoopReport(report, flags, func() {
		fmt.Printf("lw issue promote: %s label=%s -> pipeline %s\n", repo, label, pipeline)

		for _, c := range report.Changes {
			line := fmt.Sprintf("  #%-5d %-18s %s", c.Issue, c.Action, c.Title)
			if c.TaskID != "" {
				line += "  task " + c.TaskID
			}

			if c.Reason != "" {
				line += "  (" + c.Reason + ")"
			}

			fmt.Println(line)
		}
	})
}

// promotionRequest is the task the receiver's promote_issue would create for
// the same issue, plus the labels the executor's branch prefix is chosen from.
func promotionRequest(repo, pipeline string, issue *gh.Issue) nulltickets.TaskRequest {
	labels := make([]any, 0, len(issue.Labels))
	for _, l := range issue.Labels {
		labels = append(labels, l)
	}

	return nulltickets.TaskRequest{
		PipelineID:  pipeline,
		Title:       fmt.Sprintf("%s#%d: %s", repo, issue.Number, issue.Title),
		Description: strings.TrimSpace(issue.Body + "\n\n" + issue.URL),
		Metadata: map[string]any{
			"source":       "github",
			repoKey:        repo,
			"issue_number": issue.Number,
			"issue_url":    issue.URL,
			"labels":       labels,
			"binding_key":  issueBindingKey(repo, issue.Number),
			"promoted_by":  "lw issue promote",
		},
	}
}

type reconciliation struct {
	TaskID string `json:"task_id"`
	PR     string `json:"pr,omitempty"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
	Issue  int    `json:"issue,omitempty"`
	Round  int    `json:"round,omitempty"`
}

type reconcileReport struct {
	StartedAt time.Time        `json:"started_at"`
	Repo      string           `json:"repo"`
	Pipeline  string           `json:"pipeline"`
	Changes   []reconciliation `json:"changes"`
	DryRun    bool             `json:"dry_run"`
}

func issueReconcileHandler(ctx context.Context, _ []string, flags map[string]any) error {
	repo := gh.QualifyRepo(flagStr(flags, repoKey), gh.DefaultOrg)
	if repo == "" {
		repo = gh.CurrentRepo(".")
	}

	pipeline := flagStr(flags, "pipeline")
	if repo == "" || pipeline == "" {
		return errors.New("usage: lw issue reconcile --repo <owner/repo> --pipeline <nulltickets_pipeline_id> [--max-rounds 3] [--dry-run] [--json]")
	}

	dryRun := flagBool(flags, "dry-run")

	maxRounds, err := flagIntOr(flags, "max-rounds", issueLoopMaxRounds)
	if err != nil {
		return err
	}

	queue, err := issueLoopQueue(ctx, dryRun)
	if err != nil {
		return err
	}

	tasks, err := queue.ListTasks(ctx, pipeline, issueLoopReviewStage)
	if err != nil {
		return err
	}

	report := reconcileReport{StartedAt: time.Now().UTC(), Repo: repo, Pipeline: pipeline, DryRun: dryRun, Changes: []reconciliation{}}
	if len(tasks) == 0 {
		return printReconcileReport(&report, flags)
	}

	role, err := queue.StageRole(ctx, pipeline, issueLoopReviewStage)
	if err != nil {
		return err
	}

	for _, task := range tasks {
		report.Changes = append(report.Changes, reconcileTask(ctx, queue, repo, role, &task, maxRounds, dryRun))
	}

	return printReconcileReport(&report, flags)
}

// reconcileTask decides one in_review task from its PR. Round n is the n-th
// time the task has come up for review: every submit and every reject bumps
// task_version by one, so the review round is task_version / 2.
func reconcileTask(ctx context.Context, queue *nulltickets.Client, repo, role string, task *nulltickets.Task, maxRounds int, dryRun bool) reconciliation {
	change := reconciliation{TaskID: task.ID, Round: task.TaskVersion / versionsPerReviewRound}
	if n, ok := task.Metadata["issue_number"].(float64); ok {
		change.Issue = int(n)
	}

	pr, err := findPullRequest(repo, task.ID)
	if err != nil {
		change.Action, change.Reason = reconcileError, err.Error()
		return change
	}

	if pr == nil {
		change.Action, change.Reason = reconcileWaitingForPR, "no PR carries "+gh.TaskRef(task.ID)
		return change
	}

	change.PR = pr.URL

	decision, trigger, instructions := decidePullRequest(pr, change.Round, maxRounds)
	change.Action, change.Reason = decision, instructions

	if dryRun {
		change.Action = reconcileDryRun + ":" + decision
		return change
	}

	switch decision {
	case reconcilePending, reconcileWaitingForPR:
		return change
	case reconcileAutoMerge:
		if err := armPullRequestMerge(repo, pr.Number); err != nil {
			change.Action, change.Reason = reconcileError, err.Error()
		}

		return change
	}

	claim, err := queue.ClaimTask(ctx, issueLoopAgent, role, task.ID, issueLoopLeaseTTL)
	if err != nil {
		change.Action, change.Reason = reconcileError, err.Error()
		return change
	}

	if err := queue.Transition(ctx, claim, trigger, instructions); err != nil {
		change.Action, change.Reason = reconcileError, err.Error()
		return change
	}

	if decision == reconcileDropped && change.Issue > 0 {
		body := fmt.Sprintf("The issue loop gave this up after %d review rounds on %s.\n\n%s\n\nA person needs to look. Remove `%s` and re-add `%s` to run it again.",
			change.Round, pr.URL, instructions, issueLoopOperatorTag, issueLoopDefaultLabel)
		if err := commentOnIssue(repo, change.Issue, body); err != nil {
			change.Reason += "; comment failed: " + err.Error()
		}

		if err := addIssueLabel(repo, change.Issue, issueLoopOperatorTag); err != nil {
			change.Reason += "; label failed: " + err.Error()
		}
	}

	return change
}

// decidePullRequest maps a PR's state to the loop's decision, the nulltickets
// trigger that carries it out, and the instructions the next round gets.
func decidePullRequest(pr *gh.PullRequest, round, maxRounds int) (decision, trigger, instructions string) {
	switch pr.State {
	case "MERGED":
		return reconcileApproved, "approve", "merged as " + pr.URL
	case "CLOSED":
		return reconcileDropped, "drop", "PR " + pr.URL + " was closed without merging"
	}

	// A draft is work in progress: red CI on it is expected, not a review
	// round, and must not spend rounds toward the cap.
	if pr.IsDraft {
		return reconcilePending, "", "draft " + pr.URL + " is not ready for review"
	}

	failed := pr.FailedChecks()
	if len(failed) == 0 {
		// No checks at all is not green: CI has not reported yet, or the repo
		// has none — either way nothing has judged the change, and arming
		// auto-merge on a repo with no required checks merges it on the spot.
		if len(pr.Checks) == 0 || pr.PendingChecks() {
			return reconcilePending, "", "checks still running on " + pr.URL
		}

		return reconcileAutoMerge, "", "all checks green on " + pr.URL + "; auto-merge armed"
	}

	names := make([]string, 0, len(failed))
	for _, f := range failed {
		names = append(names, f.Name+"="+strings.ToLower(f.Conclusion))
	}

	why := fmt.Sprintf("review round %d: failing on %s — %s. Read the failing checks and every review comment on the PR before changing anything, then push to the same branch.",
		round, pr.URL, strings.Join(names, ", "))

	if round >= maxRounds {
		return reconcileDropped, "drop", why
	}

	return reconcileRejected, "reject", why
}

// issueLoopQueue is the nulltickets client, located like the receiver and
// knowledge promote do. A dry run reads no secret.
func issueLoopQueue(ctx context.Context, dryRun bool) (*nulltickets.Client, error) {
	token, err := nullticketsToken(ctx, dryRun)
	if err != nil {
		return nil, err
	}

	return nulltickets.New(os.Getenv("NULLTICKETS_URL"), token), nil
}

func flagIntOr(flags map[string]any, key string, fallback int) (int, error) {
	raw := flagStr(flags, key)
	if raw == "" {
		return fallback, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid --%s %q", key, raw)
	}

	return n, nil
}

func printIssueLoopReport(report any, flags map[string]any, human func()) error {
	if !flagBool(flags, "json") {
		human()
		return nil
	}

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(encoded))

	return nil
}

func printReconcileReport(report *reconcileReport, flags map[string]any) error {
	return printIssueLoopReport(report, flags, func() {
		fmt.Printf("lw issue reconcile: %s pipeline %s — %d task(s) in %s\n", report.Repo, report.Pipeline, len(report.Changes), issueLoopReviewStage)

		for _, c := range report.Changes {
			line := fmt.Sprintf("  %s %-18s", c.TaskID, c.Action)
			if c.PR != "" {
				line += " " + c.PR
			}

			if c.Reason != "" {
				line += "  (" + c.Reason + ")"
			}

			fmt.Println(line)
		}
	})
}
