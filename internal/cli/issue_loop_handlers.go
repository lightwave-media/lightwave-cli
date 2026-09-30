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
	// The operator's explicit "run this again": add it to an issue and the next
	// tick queues a fresh attempt at it. Retry is never automatic, because an
	// agent that drops an issue as unclear or already done would otherwise be
	// re-queued and re-dropped every tick.
	issueLoopRetryLabel = "status:retry"
	// The work stage: a task in it that has used every attempt is parked here,
	// unclaimable, and no other stage reads it.
	issueLoopWorkStage = "in_development"
	// The stages a pipeline ends in: merged work, and a withdrawn or dropped task.
	issueLoopDoneStage      = "done"
	issueLoopWithdrawnStage = "not_doing"
	// A task whose next eligible time is further away than this has no attempts
	// left (nulltickets parks it at the end of time).
	exhaustedHorizon = 365 * 24 * time.Hour
	// An open, green PR the delivery hook opened is merged even when its task was
	// dropped after delivery (issue #575); only tasks that ended within this
	// window are looked at, so the scan costs a handful of PR lookups per tick.
	orphanWindow   = 24 * time.Hour
	issueLoopPRTag = "Opened by the issue loop"
	// promoteMaxGenerations bounds how many times one issue can be re-queued.
	promoteMaxGenerations = 5
	// How long a task may sit in in_review with no PR before that is a failed
	// round. The push and the PR search are not instant, but a task that was
	// submitted ten minutes ago with nothing on GitHub was not delivered: on the
	// first live run a model reported success having pushed nothing.
	missingPullRequestGrace = 10 * time.Minute

	reconcileWaitingForPR = "waiting_for_pr"
	reconcileMarkedReady  = "marked_ready"
	reconcilePending      = "pending"
	reconcileApproved     = "approved"
	reconcileRejected     = "rejected"
	reconcileDropped      = "dropped"
	reconcileExhausted    = "exhausted"
	reconcileOrphanMerged = "orphan_merged"
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
	listOpenIssues       = gh.ListOpenIssues
	findPullRequest      = gh.FindPullRequestForTask
	commentOnIssue       = gh.CommentOnIssue
	addIssueLabel        = gh.AddIssueLabel
	removeIssueLabel     = gh.RemoveIssueLabel
	armPullRequestMerge  = gh.ArmAutoMerge
	markPullRequestReady = gh.MarkPullRequestReady
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

	retries, err := listOpenIssues(repo, issueLoopRetryLabel, limit)
	if err != nil {
		return err
	}

	for i := range retries {
		report.Changes = append(report.Changes, retryIssue(ctx, queue, repo, pipeline, &retries[i], dryRun))
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
// generationKey is the binding key of the n-th attempt at an issue. Generation
// 1 is the plain binding, so the webhook and the first poll agree.
func generationKey(repo string, number, generation int) string {
	if generation <= 1 {
		return issueBindingKey(repo, number)
	}

	return fmt.Sprintf("%s:g%d", issueBindingKey(repo, number), generation)
}

// taskIsExhausted reports whether nulltickets has parked the task with no
// attempts left: its next eligible time is further out than any retry delay.
func taskIsExhausted(task *nulltickets.Task) bool {
	return task.NextEligibleAtMs > 0 && time.Until(time.UnixMilli(task.NextEligibleAtMs)) >= exhaustedHorizon
}

// taskIsLive reports whether a task can still be worked, that is, is not in a
// stage the pipeline ends in.
func taskIsLive(task *nulltickets.Task) bool {
	switch task.Stage {
	case issueLoopDoneStage, issueLoopWithdrawnStage:
		return false
	}

	// Parked with no attempts left: dead, whether or not reconcile has
	// withdrawn it yet. Promote and reconcile are separate jobs, so retry can
	// run first.
	if taskIsExhausted(task) {
		return false
	}

	return true
}

// retryIssue queues a fresh attempt at an issue the operator labelled
// status:retry: the first generation whose task is still live, creating it if
// it does not exist. The idempotency key of a dead task is spent, so every
// retry moves to the next generation. A 409 (the issue was edited after that
// generation was queued) is read the same way, because editing an issue and
// asking for a retry is the usual order of events.
func retryIssue(ctx context.Context, queue *nulltickets.Client, repo, pipeline string, issue *gh.Issue, dryRun bool) promotion {
	change := promotion{Issue: issue.Number, Title: issue.Title}
	if dryRun {
		change.Action = reconcileDryRun + ":retry"
		return change
	}

	for generation := 1; generation <= promoteMaxGenerations; generation++ {
		taskID, err := queue.CreateTask(ctx, generationKey(repo, issue.Number, generation), promotionRequest(repo, pipeline, issue))
		if err != nil {
			if strings.Contains(err.Error(), "HTTP 409") {
				continue
			}

			change.Action, change.Reason = reconcileError, err.Error()

			return change
		}

		task, err := queue.GetTask(ctx, taskID)
		if err != nil {
			change.Action, change.Reason = reconcileError, err.Error()
			return change
		}

		if !taskIsLive(task) {
			continue
		}

		change.Action, change.TaskID = "retried", taskID
		change.Reason = fmt.Sprintf("generation %d", generation)

		if err := removeIssueLabel(repo, issue.Number, issueLoopRetryLabel); err != nil {
			change.Reason += "; label not removed: " + err.Error()
		}

		return change
	}

	change.Action, change.Reason = reconcileError, fmt.Sprintf("no live generation within %d attempts; a person needs to look", promoteMaxGenerations)

	return change
}

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

	report := reconcileReport{StartedAt: time.Now().UTC(), Repo: repo, Pipeline: pipeline, DryRun: dryRun, Changes: []reconciliation{}}

	exhausted, err := reconcileExhaustedTasks(ctx, queue, repo, pipeline, dryRun)
	if err != nil {
		return err
	}

	report.Changes = append(report.Changes, exhausted...)

	orphaned, err := reconcileOrphanedPullRequests(ctx, queue, repo, pipeline, dryRun)
	if err != nil {
		return err
	}

	report.Changes = append(report.Changes, orphaned...)

	tasks, err := queue.ListTasks(ctx, pipeline, issueLoopReviewStage)
	if err != nil {
		return err
	}

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

// reconcileExhaustedTasks finds tasks that used every attempt in the work stage.
// nulltickets parks such a task at the end of time, still in its stage, where
// nothing claims it and nothing tells anyone; here it is withdrawn and the
// issue is handed to a person.
func reconcileExhaustedTasks(ctx context.Context, queue *nulltickets.Client, repo, pipeline string, dryRun bool) ([]reconciliation, error) {
	tasks, err := queue.ListTasks(ctx, pipeline, issueLoopWorkStage)
	if err != nil {
		return nil, err
	}

	var changes []reconciliation

	for i := range tasks {
		task := &tasks[i]
		if !taskIsExhausted(task) {
			continue
		}

		change := reconciliation{TaskID: task.ID, Action: reconcileExhausted, Reason: "the task used every attempt without reaching review"}
		if n, ok := task.Metadata["issue_number"].(float64); ok {
			change.Issue = int(n)
		}

		if dryRun {
			change.Action = reconcileDryRun + ":" + reconcileExhausted
			changes = append(changes, change)

			continue
		}

		if err := queue.Withdraw(ctx, task.ID, change.Reason, issueLoopAgent); err != nil {
			change.Action, change.Reason = reconcileError, err.Error()
			changes = append(changes, change)

			continue
		}

		handOffToOperator(repo, &change, "The issue loop ran out of attempts on this without producing a pull request.")

		changes = append(changes, change)
	}

	return changes, nil
}

// reconcileOrphanedPullRequests merges the PR of a task that ended after the
// work was delivered. The delivery hook opens the PR on every run, but the task
// only reaches in_review on a clean NEXT-TRIGGER; a worker that fails or answers
// drop on a later run leaves a green PR nothing else would ever look at. Only a
// PR the hook opened (its body says so), still open, with every check green is
// touched; a red or pending one is left for a person.
func reconcileOrphanedPullRequests(ctx context.Context, queue *nulltickets.Client, repo, pipeline string, dryRun bool) ([]reconciliation, error) {
	tasks, err := queue.ListTasks(ctx, pipeline, issueLoopWithdrawnStage)
	if err != nil {
		return nil, err
	}

	var changes []reconciliation

	for i := range tasks {
		task := &tasks[i]
		if task.UpdatedAtMs <= 0 || time.Since(time.UnixMilli(task.UpdatedAtMs)) > orphanWindow {
			continue
		}

		pr, err := findPullRequest(repo, task.ID)
		if err != nil {
			changes = append(changes, reconciliation{TaskID: task.ID, Action: reconcileError, Reason: err.Error()})
			continue
		}

		if pr == nil || pr.State != "OPEN" || !strings.Contains(pr.Body, issueLoopPRTag) {
			continue
		}

		// Judge the checks as if it were not a draft: a draft is not a verdict.
		judged := *pr
		judged.IsDraft = false

		if decision, _, _ := decidePullRequest(&judged, 0, 0); decision != reconcileAutoMerge {
			continue
		}

		change := reconciliation{TaskID: task.ID, PR: pr.URL, Action: reconcileOrphanMerged,
			Reason: "task ended after delivery; all checks green on " + pr.URL}
		if n, ok := task.Metadata["issue_number"].(float64); ok {
			change.Issue = int(n)
		}

		if dryRun {
			change.Action = reconcileDryRun + ":" + reconcileOrphanMerged
			changes = append(changes, change)

			continue
		}

		// Leaving draft can start checks of its own, so a draft is only marked
		// ready now and judged again, as a live PR, on the next tick.
		if pr.IsDraft {
			change.Action = reconcileMarkedReady
			change.Reason = "task ended after delivery; marking draft " + pr.URL + " ready for review"

			if err := markPullRequestReady(repo, pr.Number); err != nil {
				change.Action, change.Reason = reconcileError, err.Error()
			}

			changes = append(changes, change)

			continue
		}

		if err := armPullRequestMerge(repo, pr.Number, pr.HeadRefOID); err != nil {
			change.Action, change.Reason = reconcileError, err.Error()
		}

		changes = append(changes, change)
	}

	return changes, nil
}

// handOffToOperator tells a person the loop is done with an issue: a comment
// saying why and how to run it again, and the needs-operator label.
func handOffToOperator(repo string, change *reconciliation, headline string) {
	if change.Issue <= 0 {
		return
	}

	body := fmt.Sprintf("%s\n\n%s\n\nA person needs to look. Add the label `%s` to run it again; remove `%s` when you have.",
		headline, change.Reason, issueLoopRetryLabel, issueLoopOperatorTag)
	if err := commentOnIssue(repo, change.Issue, body); err != nil {
		change.Reason += "; comment failed: " + err.Error()
	}

	if err := addIssueLabel(repo, change.Issue, issueLoopOperatorTag); err != nil {
		change.Reason += "; label failed: " + err.Error()
	}
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

	var (
		decision, trigger, instructions string
		where                           = "the task's branch, which has no PR"
	)

	if pr == nil {
		// No timestamp means unknown, not "since 1970": wait rather than fail a
		// round on a task whose age we cannot tell.
		if task.UpdatedAtMs <= 0 || time.Since(time.UnixMilli(task.UpdatedAtMs)) < missingPullRequestGrace {
			change.Action, change.Reason = reconcileWaitingForPR, "no PR carries "+gh.TaskRef(task.ID)
			return change
		}

		decision, trigger, instructions = decideMissingPullRequest(task.ID, change.Round, maxRounds)
	} else {
		change.PR, where = pr.URL, pr.URL
		decision, trigger, instructions = decidePullRequest(pr, change.Round, maxRounds)
	}

	change.Action, change.Reason = decision, instructions

	if dryRun {
		change.Action = reconcileDryRun + ":" + decision
		return change
	}

	switch decision {
	case reconcilePending, reconcileWaitingForPR:
		return change
	case reconcileMarkedReady:
		if err := markPullRequestReady(repo, pr.Number); err != nil {
			change.Action, change.Reason = reconcileError, err.Error()
		}

		return change
	case reconcileAutoMerge:
		if err := armPullRequestMerge(repo, pr.Number, pr.HeadRefOID); err != nil {
			change.Action, change.Reason = reconcileError, err.Error()
		}

		return change
	}

	return moveTask(ctx, queue, repo, role, task, &change, trigger, where)
}

// moveTask carries a decision out on the queue: the targeted claim, the
// transition with the instructions the next round reads, and, when the task is
// given up, the comment and label that tell a person why.
func moveTask(ctx context.Context, queue *nulltickets.Client, repo, role string, task *nulltickets.Task, change *reconciliation, trigger, where string) reconciliation {
	claim, err := queue.ClaimTask(ctx, issueLoopAgent, role, task.ID, issueLoopLeaseTTL)
	if err != nil {
		change.Action, change.Reason = reconcileError, err.Error()
		return *change
	}

	if err := queue.Transition(ctx, claim, trigger, change.Reason); err != nil {
		change.Action, change.Reason = reconcileError, err.Error()
		return *change
	}

	if change.Action == reconcileDropped {
		handOffToOperator(repo, change, fmt.Sprintf("The issue loop gave this up after %d review rounds on %s.", change.Round, where))
	}

	return *change
}

// decideMissingPullRequest is the decision for a task that has sat in review
// past the grace window with no PR: it was reported done and not delivered, so
// it goes back for another round, and past the cap it is given up.
func decideMissingPullRequest(taskID string, round, maxRounds int) (decision, trigger, instructions string) {
	why := fmt.Sprintf("review round %d: no pull request carries %s, so nothing was delivered. Commit the work on this branch, push it, and open a pull request whose body contains the lines %q and \"Closes #<issue>\".",
		round, gh.TaskRef(taskID), gh.TaskRef(taskID))

	if round >= maxRounds {
		return reconcileDropped, "drop", why
	}

	return reconcileRejected, "reject", why
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

	// The delivery hook opens every PR as a draft, because it cannot tell a
	// finished run from a failed attempt. A task in in_review was submitted, so
	// its draft is ready: mark it, and judge its checks on the next tick. Red CI
	// on a draft is decided before this, never as a review round.
	if pr.IsDraft {
		return reconcileMarkedReady, "", "task submitted; marking draft " + pr.URL + " ready for review"
	}

	failed := pr.FailedChecks()

	findings := append([]string{}, pr.BlockingReviews...)
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		findings = append(findings, "changes requested by review")
	}

	if len(failed) == 0 && len(findings) == 0 {
		// No checks at all is not green: CI has not reported yet, or the repo
		// has none — either way nothing has judged the change, and arming
		// auto-merge on a repo with no required checks merges it on the spot.
		if len(pr.Checks) == 0 || pr.PendingChecks() {
			return reconcilePending, "", "checks still running on " + pr.URL
		}

		if pr.ReviewDecision == "REVIEW_REQUIRED" {
			return reconcilePending, "", "review still required on " + pr.URL
		}

		return reconcileAutoMerge, "", "all checks green on " + pr.URL + "; auto-merge armed"
	}

	for _, f := range failed {
		findings = append(findings, f.Name+"="+strings.ToLower(f.Conclusion))
	}

	why := fmt.Sprintf("review round %d: failing on %s (head %s) — %s. Read the failing checks and every review comment on the PR before changing anything, then push to the same branch.",
		round, pr.URL, pr.HeadRefOID, strings.Join(findings, ", "))

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
		fmt.Printf("lw issue reconcile: %s pipeline %s — %d task(s)\n", report.Repo, report.Pipeline, len(report.Changes))

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
