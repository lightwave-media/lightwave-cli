package cli //nolint:testpackage // the GitHub seams are package-private on purpose

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gh "github.com/lightwave-media/lightwave-cli/internal/github"
)

// runHandler invokes a registered handler with stdout captured. testutil's
// RunHandler does the same but imports this package, and the GitHub seams
// these tests swap are package-private — so the capture is repeated here.
func runHandler(t *testing.T, key string, flags map[string]any) (string, error) {
	t.Helper()

	handler, ok := LookupHandler(key)
	require.True(t, ok, "no handler registered for %q", key)

	reader, writer, err := os.Pipe()
	require.NoError(t, err)

	saved := os.Stdout
	os.Stdout = writer

	handlerErr := handler(context.Background(), nil, flags)

	os.Stdout = saved
	require.NoError(t, writer.Close())

	captured, err := io.ReadAll(reader)
	require.NoError(t, err)

	return string(captured), handlerErr
}

const (
	testPipeline = "pipe"
	testRepo     = "lightwave-media/lightwave-cli"
	testRepoFlag = "lightwave-cli"
	stateOpen    = "OPEN"
	failure      = "FAILURE"
	labelBug     = "bug"
	jsonFlag     = "json"
	pipelineFlag = "pipeline"
)

// fakeQueue is nulltickets as the loop sees it: it records every task it is
// asked to create, serves one pipeline with a reviewer role on in_review,
// lists the in_review tasks it is seeded with, and accepts targeted claims and
// transitions — recording the trigger and instructions of each.
type fakeQueue struct {
	created     []createdTask
	inReview    []map[string]any
	inDev       []map[string]any
	notDoing    []map[string]any
	withdrawn   []string
	stages      map[string]string // task id -> stage served by GET /tasks/{id}
	conflicts   map[string]bool   // idempotency keys answered 409
	parked      map[string]bool   // task ids served with no attempts left
	transitions []transitionCall
	mu          sync.Mutex
	claimStatus int
	createCode  int
}

type createdTask struct {
	Body           map[string]any
	IdempotencyKey string
}

type transitionCall struct {
	TaskID       string
	Trigger      string
	Instructions string
}

func (q *fakeQueue) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tasks", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)

		q.mu.Lock()
		q.created = append(q.created, createdTask{IdempotencyKey: r.Header.Get("Idempotency-Key"), Body: body})
		q.mu.Unlock()

		if q.conflicts[r.Header.Get("Idempotency-Key")] {
			w.WriteHeader(http.StatusConflict)
			return
		}

		if q.createCode != 0 {
			w.WriteHeader(q.createCode)
			return
		}

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"task-` + r.Header.Get("Idempotency-Key")[len(r.Header.Get("Idempotency-Key"))-2:] + `"}`))
	})
	mux.HandleFunc("GET /tasks", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("stage") {
		case issueLoopReviewStage:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": q.inReview, "next_cursor": nil})
		case issueLoopWorkStage:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": q.inDev, "next_cursor": nil})
		case issueLoopWithdrawnStage:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": q.notDoing, "next_cursor": nil})
		default:
			_, _ = w.Write([]byte(`{"items":[],"next_cursor":null}`))
		}
	})
	mux.HandleFunc("GET /tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		stage := "in_development"
		if s, ok := q.stages[r.PathValue("id")]; ok {
			stage = s
		}

		task := map[string]any{"id": r.PathValue("id"), "stage": stage}
		if q.parked[r.PathValue("id")] {
			task["next_eligible_at_ms"] = int64(9223372036854775807)
		}

		_ = json.NewEncoder(w).Encode(task)
	})
	mux.HandleFunc("POST /tasks/{id}/withdraw", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)

		if body["reason"] == "" || body["actor"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		q.mu.Lock()
		q.withdrawn = append(q.withdrawn, r.PathValue("id"))
		q.mu.Unlock()

		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("GET /pipelines/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"pipe","definition":{"states":{"in_development":{"agent_role":"developer:cli"},"in_review":{"agent_role":"reviewer:cli"}}}}`))
	})
	mux.HandleFunc("POST /leases/claim", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)

		if q.claimStatus != 0 {
			w.WriteHeader(q.claimStatus)
			return
		}

		taskID, _ := body["task_id"].(string)
		if body["agent_role"] != "reviewer:cli" || taskID == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		for _, t := range q.inReview {
			if t["id"] == taskID {
				_ = json.NewEncoder(w).Encode(map[string]any{"task": t, "run": map[string]any{"id": "run-" + taskID}, "lease_id": "lease", "lease_token": "tok-" + taskID})
				return
			}
		}

		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /runs/{run}/transition", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)

		taskID := strings.TrimPrefix(r.PathValue("run"), "run-")
		if r.Header.Get("Authorization") != "Bearer tok-"+taskID {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		q.mu.Lock()
		q.transitions = append(q.transitions, transitionCall{TaskID: taskID, Trigger: body["trigger"], Instructions: body["instructions"]})
		q.mu.Unlock()

		_, _ = w.Write([]byte(`{"previous_stage":"in_review","new_stage":"x","trigger":"` + body["trigger"] + `"}`))
	})

	return mux
}

// reviewTask is a task that entered in_review just now.
func reviewTask(id string, issue, version int) map[string]any {
	return staleReviewTask(id, issue, version, 0)
}

// staleReviewTask is a task that entered in_review age ago.
func staleReviewTask(id string, issue, version int, age time.Duration) map[string]any {
	return map[string]any{"id": id, "pipeline_id": testPipeline, "stage": issueLoopReviewStage, "title": "t", "description": "d",
		"task_version": version, "updated_at_ms": time.Now().Add(-age).UnixMilli(),
		"metadata": map[string]any{repoKey: testRepo, "issue_number": issue, "binding_key": issueBindingKey(testRepo, issue)}}
}

// withReadySeam records every PR the loop takes out of draft.
func withReadySeam(t *testing.T) *[]int {
	t.Helper()

	ready := &[]int{}
	orig := markPullRequestReady
	markPullRequestReady = func(_ string, n int) error { *ready = append(*ready, n); return nil }
	t.Cleanup(func() { markPullRequestReady = orig })

	return ready
}

// withIssueLoopSeams swaps every GitHub seam for the test's doubles and points
// the queue at the fake server. RunHandler captures stdout globally, so these
// tests are serial.
func withIssueLoopSeams(t *testing.T, queue *fakeQueue, issues []gh.Issue, prs map[string]*gh.PullRequest) (comments *[]string, labels *[]string, armed *[]int) {
	t.Helper()

	server := httptest.NewServer(queue.handler())
	t.Cleanup(server.Close)
	t.Setenv("NULLTICKETS_URL", server.URL)
	t.Setenv("NULLTICKETS_API_TOKEN", "test-bearer")

	comments, labels, armed = &[]string{}, &[]string{}, &[]int{}

	origList, origFind, origComment, origLabel, origArm := listOpenIssues, findPullRequest, commentOnIssue, addIssueLabel, armPullRequestMerge
	listOpenIssues = func(_, label string, _ int) ([]gh.Issue, error) {
		if label == issueLoopRetryLabel {
			return nil, nil
		}

		return issues, nil
	}
	findPullRequest = func(_ string, taskID string) (*gh.PullRequest, error) { return prs[taskID], nil }
	commentOnIssue = func(_ string, n int, body string) error { *comments = append(*comments, body); return nil }
	addIssueLabel = func(_ string, _ int, label string) error { *labels = append(*labels, label); return nil }
	armPullRequestMerge = func(_ string, n int) error { *armed = append(*armed, n); return nil }
	origRemove := removeIssueLabel
	removeIssueLabel = func(string, int, string) error { return nil }
	t.Cleanup(func() { removeIssueLabel = origRemove })
	t.Cleanup(func() {
		listOpenIssues, findPullRequest, commentOnIssue, addIssueLabel, armPullRequestMerge = origList, origFind, origComment, origLabel, origArm
	})

	return comments, labels, armed
}

func TestIssuePromoteCreatesOneTaskPerIssueWithTheReceiverBinding(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{}
	issues := []gh.Issue{
		{Number: 42, Title: "tool gap: lw contract new", Body: "### Motivation\nnone", URL: "https://github.com/lightwave-media/lightwave-cli/issues/42", Labels: []string{"status:ready", "tool-gap"}},
		{Number: 7, Title: "bug: x", Body: "", URL: "https://github.com/lightwave-media/lightwave-cli/issues/7", Labels: []string{labelBug, "status:ready"}},
	}
	withIssueLoopSeams(t, queue, issues, nil)

	out, err := runHandler(t, "issue.promote", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline, jsonFlag: true})
	require.NoError(t, err)

	require.Len(t, queue.created, 2)
	first := queue.created[0]
	assert.Equal(t, "github:issue:"+testRepo+"#42", first.IdempotencyKey, "the receiver's binding key, so a webhook delivery and a poll are one task")
	assert.Equal(t, testPipeline, first.Body["pipeline_id"])
	assert.Equal(t, "lightwave-media/lightwave-cli#42: tool gap: lw contract new", first.Body["title"])
	assert.Contains(t, first.Body["description"], "issues/42")

	meta, ok := first.Body["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "github", meta["source"])
	assert.Equal(t, testRepo, meta[repoKey])
	assert.InDelta(t, 42.0, meta["issue_number"], 0)
	assert.Equal(t, []any{"status:ready", "tool-gap"}, meta["labels"])

	var report promoteReport
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	assert.Len(t, report.Changes, 2)
	assert.Equal(t, "promoted", report.Changes[0].Action)
	assert.NotEmpty(t, report.Changes[0].TaskID)
}

func TestIssuePromoteDryRunSendsNothing(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{}
	withIssueLoopSeams(t, queue, []gh.Issue{{Number: 1, Title: "x", URL: "u"}}, nil)

	out, err := runHandler(t, "issue.promote", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline, "dry-run": true})
	require.NoError(t, err)
	assert.Empty(t, queue.created)
	assert.Contains(t, out, "dry_run")
}

func TestIssuePromoteTreatsAConflictAsAlreadyPromoted(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{createCode: http.StatusConflict}
	withIssueLoopSeams(t, queue, []gh.Issue{{Number: 1, Title: "x", URL: "u"}}, nil)

	out, err := runHandler(t, "issue.promote", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline, jsonFlag: true})
	require.NoError(t, err)

	var report promoteReport
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.Len(t, report.Changes, 1)
	assert.Equal(t, "already_promoted", report.Changes[0].Action)
}

func TestIssuePromoteRequiresRepoAndPipeline(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	withIssueLoopSeams(t, &fakeQueue{}, nil, nil)

	_, err := runHandler(t, "issue.promote", map[string]any{repoKey: testRepoFlag})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--pipeline")
}

func TestIssueReconcileDecidesEachTaskFromItsPullRequest(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	green := gh.CheckOutcome{Name: "CI Required Gate", Conclusion: "SUCCESS"}
	red := gh.CheckOutcome{Name: "CI Required Gate", Conclusion: failure}
	review := gh.CheckOutcome{Name: "lightwave/local-review", Conclusion: failure}
	pending := gh.CheckOutcome{Name: "CI Required Gate", Conclusion: "PENDING"}

	queue := &fakeQueue{inReview: []map[string]any{
		reviewTask("merged", 1, 2),
		reviewTask("red", 2, 2),
		reviewTask("blocked-review", 3, 4),
		reviewTask("exhausted", 4, 6),
		reviewTask("pending", 5, 2),
		reviewTask("green", 6, 2),
		reviewTask("no-pr", 7, 2),
		reviewTask("closed", 8, 2),
	}}
	prs := map[string]*gh.PullRequest{
		"merged":         {Number: 10, URL: "pr/10", State: "MERGED"},
		"red":            {Number: 11, URL: "pr/11", State: stateOpen, Checks: []gh.CheckOutcome{red}},
		"blocked-review": {Number: 12, URL: "pr/12", State: stateOpen, Checks: []gh.CheckOutcome{green, review}},
		"exhausted":      {Number: 13, URL: "pr/13", State: stateOpen, Checks: []gh.CheckOutcome{red}},
		"pending":        {Number: 14, URL: "pr/14", State: stateOpen, Checks: []gh.CheckOutcome{pending}},
		"green":          {Number: 15, URL: "pr/15", State: stateOpen, Checks: []gh.CheckOutcome{green}},
		"closed":         {Number: 16, URL: "pr/16", State: "CLOSED"},
	}
	comments, labels, armed := withIssueLoopSeams(t, queue, nil, prs)

	out, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline, jsonFlag: true})
	require.NoError(t, err)

	var report reconcileReport
	require.NoError(t, json.Unmarshal([]byte(out), &report))

	actions := map[string]reconciliation{}
	for _, c := range report.Changes {
		actions[c.TaskID] = c
	}

	assert.Equal(t, reconcileApproved, actions["merged"].Action)
	assert.Equal(t, reconcileRejected, actions["red"].Action)
	assert.Equal(t, reconcileRejected, actions["blocked-review"].Action, "a blocking persona review is a failed check like any other")
	assert.Equal(t, reconcileDropped, actions["exhausted"].Action, "round 3 of 3 is the last")
	assert.Equal(t, reconcilePending, actions["pending"].Action)
	assert.Equal(t, reconcileAutoMerge, actions["green"].Action)
	assert.Equal(t, reconcileWaitingForPR, actions["no-pr"].Action)
	assert.Equal(t, reconcileDropped, actions["closed"].Action)

	byTask := map[string]transitionCall{}
	for _, tr := range queue.transitions {
		byTask[tr.TaskID] = tr
	}

	assert.Equal(t, "approve", byTask["merged"].Trigger)
	assert.Equal(t, "reject", byTask["red"].Trigger)
	assert.Contains(t, byTask["red"].Instructions, "CI Required Gate=failure")
	assert.Contains(t, byTask["red"].Instructions, "review round 1")
	assert.Equal(t, "reject", byTask["blocked-review"].Trigger)
	assert.Contains(t, byTask["blocked-review"].Instructions, "lightwave/local-review=failure")
	assert.Equal(t, "drop", byTask["exhausted"].Trigger)
	assert.Equal(t, "drop", byTask["closed"].Trigger)

	_, pendingTouched := byTask["pending"]
	_, greenTouched := byTask["green"]
	_, noPRTouched := byTask["no-pr"]
	assert.False(t, pendingTouched, "a PR still running its checks is left alone")
	assert.False(t, greenTouched, "a green PR is merged by GitHub, not moved by the loop")
	assert.False(t, noPRTouched)

	assert.Equal(t, []int{15}, *armed, "auto-merge armed on the green PR only")
	assert.Len(t, *comments, 2, "the two dropped tasks tell a person why")
	assert.Contains(t, (*comments)[0], "needs-operator")
	assert.Equal(t, []string{issueLoopOperatorTag, issueLoopOperatorTag}, *labels)
}

func TestIssueReconcileWithNothingInReviewMakesNoClaims(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{}
	withIssueLoopSeams(t, queue, nil, nil)

	out, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline})
	require.NoError(t, err)
	assert.Contains(t, out, "0 task(s)")
	assert.Empty(t, queue.transitions)
}

func TestIssueReconcileDryRunDecidesWithoutTouchingAnything(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{inReview: []map[string]any{reviewTask("red", 2, 2)}}
	prs := map[string]*gh.PullRequest{"red": {Number: 11, URL: "pr/11", State: stateOpen, Checks: []gh.CheckOutcome{{Name: "ci", Conclusion: failure}}}}
	comments, labels, armed := withIssueLoopSeams(t, queue, nil, prs)

	out, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline, "dry-run": true, jsonFlag: true})
	require.NoError(t, err)

	assert.Contains(t, out, "dry_run:rejected")
	assert.Empty(t, queue.transitions)
	assert.Empty(t, *comments)
	assert.Empty(t, *labels)
	assert.Empty(t, *armed)
}

func TestIssueReconcileReportsAnUnclaimableTaskAsAnError(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{inReview: []map[string]any{reviewTask("merged", 1, 2)}, claimStatus: http.StatusNoContent}
	prs := map[string]*gh.PullRequest{"merged": {Number: 10, URL: "pr/10", State: "MERGED"}}
	withIssueLoopSeams(t, queue, nil, prs)

	out, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline, jsonFlag: true})
	require.NoError(t, err)

	var report reconcileReport
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.Len(t, report.Changes, 1)
	assert.Equal(t, reconcileError, report.Changes[0].Action)
	assert.Contains(t, report.Changes[0].Reason, "not claimable")
	assert.Empty(t, queue.transitions)
}

func TestDecidePullRequestRounds(t *testing.T) {
	t.Parallel()

	red := &gh.PullRequest{URL: "u", State: stateOpen, Checks: []gh.CheckOutcome{{Name: "ci", Conclusion: failure}}}

	tests := []struct {
		name      string
		want      string
		round     int
		maxRounds int
	}{
		{"first red round is a reject", reconcileRejected, 1, 3},
		{"round below the cap is a reject", reconcileRejected, 2, 3},
		{"the cap is the last round", reconcileDropped, 3, 3},
		{"a cap of zero never retries", reconcileDropped, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decision, _, _ := decidePullRequest(red, tt.round, tt.maxRounds)
			assert.Equal(t, tt.want, decision)
		})
	}
}

func TestDecidePullRequestMarksADraftReadyWhateverItsChecksSay(t *testing.T) {
	t.Parallel()

	// The delivery hook opens every PR as a draft. A task in review was
	// submitted, so its draft is marked ready; red CI on it is judged on the
	// next tick and is never a review round while it is still a draft.
	draft := &gh.PullRequest{URL: "u", State: stateOpen, IsDraft: true, Checks: []gh.CheckOutcome{{Name: "ci", Conclusion: failure}}}
	decision, trigger, _ := decidePullRequest(draft, 3, 3)
	assert.Equal(t, reconcileMarkedReady, decision)
	assert.Empty(t, trigger)
}

func TestDecideMissingPullRequestRejectsThenGivesUp(t *testing.T) {
	t.Parallel()

	decision, trigger, instructions := decideMissingPullRequest("task-1", 1, 3)
	assert.Equal(t, reconcileRejected, decision)
	assert.Equal(t, "reject", trigger)
	assert.Contains(t, instructions, "Refs: task-1", "the next round is told the exact binding line to write")

	decision, trigger, _ = decideMissingPullRequest("task-1", 3, 3)
	assert.Equal(t, reconcileDropped, decision, "the round cap is the same as for red checks")
	assert.Equal(t, "drop", trigger)
}

func TestIssueReconcileMarksASubmittedDraftReady(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{inReview: []map[string]any{reviewTask("draft", 1, 2)}}
	prs := map[string]*gh.PullRequest{"draft": {Number: 21, URL: "pr/21", State: stateOpen, IsDraft: true}}
	_, _, armed := withIssueLoopSeams(t, queue, nil, prs)
	ready := withReadySeam(t)

	_, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline, jsonFlag: true})
	require.NoError(t, err)

	assert.Equal(t, []int{21}, *ready)
	assert.Empty(t, queue.transitions, "marking ready moves nothing on the queue")
	assert.Empty(t, *armed, "a merge is never armed on the same tick the draft is opened for review")
}

func TestIssueReconcileTreatsASubmittedTaskWithNoPRAsAFailedRound(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	unstamped := reviewTask("unstamped", 4, 2)
	delete(unstamped, "updated_at_ms")

	queue := &fakeQueue{inReview: []map[string]any{
		reviewTask("fresh", 1, 2),
		staleReviewTask("stale", 2, 2, missingPullRequestGrace+time.Minute),
		staleReviewTask("stale-exhausted", 3, 6, missingPullRequestGrace+time.Minute),
		unstamped,
	}}
	comments, labels, _ := withIssueLoopSeams(t, queue, nil, nil)
	withReadySeam(t)

	out, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline, jsonFlag: true})
	require.NoError(t, err)

	var report reconcileReport
	require.NoError(t, json.Unmarshal([]byte(out), &report))

	actions := map[string]string{}
	for _, c := range report.Changes {
		actions[c.TaskID] = c.Action
	}

	assert.Equal(t, reconcileWaitingForPR, actions["fresh"], "a task submitted a moment ago has not had time to push")
	assert.Equal(t, reconcileWaitingForPR, actions["unstamped"], "a task whose age is unknown waits; a zero timestamp is not 1970")
	assert.Equal(t, reconcileRejected, actions["stale"])
	assert.Equal(t, reconcileDropped, actions["stale-exhausted"])

	byTask := map[string]transitionCall{}
	for _, tr := range queue.transitions {
		byTask[tr.TaskID] = tr
	}

	_, freshTouched := byTask["fresh"]
	assert.False(t, freshTouched)
	assert.Equal(t, "reject", byTask["stale"].Trigger)
	assert.Contains(t, byTask["stale"].Instructions, "Refs: stale")
	assert.Equal(t, "drop", byTask["stale-exhausted"].Trigger)

	assert.Len(t, *comments, 1, "only the dropped task tells a person why")
	assert.Equal(t, []string{issueLoopOperatorTag}, *labels)
}

func TestDecidePullRequestNeverArmsMergeWithNoChecks(t *testing.T) {
	t.Parallel()

	unjudged := &gh.PullRequest{URL: "u", State: stateOpen}
	decision, trigger, _ := decidePullRequest(unjudged, 1, 3)
	assert.Equal(t, reconcilePending, decision, "a PR nothing has judged waits; on a repo with no required checks arming auto-merge would merge it on the spot")
	assert.Empty(t, trigger)
}

// exhaustedTask is a work-stage task nulltickets parked at the end of time
// after its last attempt; live is one still waiting for its next attempt.
func exhaustedTask(id string, issue int) map[string]any {
	return map[string]any{"id": id, "pipeline_id": testPipeline, "stage": issueLoopWorkStage, "task_version": 1,
		"next_eligible_at_ms": int64(9223372036854775807),
		"metadata":            map[string]any{repoKey: testRepo, "issue_number": issue}}
}

func TestIssueReconcileWithdrawsAnExhaustedTaskAndHandsTheIssueToAPerson(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	live := map[string]any{"id": "live", "stage": issueLoopWorkStage, "next_eligible_at_ms": time.Now().Add(time.Minute).UnixMilli(),
		"metadata": map[string]any{"issue_number": 8}}
	queue := &fakeQueue{inDev: []map[string]any{exhaustedTask("dead", 7), live}}
	comments, labels, _ := withIssueLoopSeams(t, queue, nil, nil)

	_, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline})
	require.NoError(t, err)

	assert.Equal(t, []string{"dead"}, queue.withdrawn, "only the task with no attempts left is withdrawn")
	require.Len(t, *comments, 1)
	assert.Contains(t, (*comments)[0], issueLoopRetryLabel, "the comment must say how to run it again")
	assert.Equal(t, []string{issueLoopOperatorTag}, *labels)
}

func TestIssueReconcileDryRunLeavesAnExhaustedTaskAlone(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{inDev: []map[string]any{exhaustedTask("dead", 7)}}
	comments, _, _ := withIssueLoopSeams(t, queue, nil, nil)

	_, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline, "dry-run": true})
	require.NoError(t, err)
	assert.Empty(t, queue.withdrawn)
	assert.Empty(t, *comments)
}

// withRetryIssues serves one issue under the retry label and records the label
// removals.
func withRetryIssues(t *testing.T, issues []gh.Issue) *[]string {
	t.Helper()

	removed := &[]string{}
	orig, origRemove := listOpenIssues, removeIssueLabel
	listOpenIssues = func(_, label string, _ int) ([]gh.Issue, error) {
		if label == issueLoopRetryLabel {
			return issues, nil
		}

		return nil, nil
	}
	removeIssueLabel = func(_ string, _ int, label string) error { *removed = append(*removed, label); return nil }
	t.Cleanup(func() { listOpenIssues, removeIssueLabel = orig, origRemove })

	return removed
}

func TestIssuePromoteRetryQueuesTheNextGenerationWhenTheLastIsDead(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{stages: map[string]string{"task-#7": "not_doing"}}
	withIssueLoopSeams(t, queue, nil, nil)
	removed := withRetryIssues(t, []gh.Issue{{Number: 7, Title: "again", URL: "u"}})

	_, err := runHandler(t, "issue.promote", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline})
	require.NoError(t, err)

	require.Len(t, queue.created, 2)
	assert.Equal(t, "github:issue:"+testRepo+"#7", queue.created[0].IdempotencyKey)
	assert.Equal(t, "github:issue:"+testRepo+"#7:g2", queue.created[1].IdempotencyKey)
	assert.Equal(t, []string{issueLoopRetryLabel}, *removed, "the retry label is consumed")
}

func TestIssuePromoteRetryReadsAConflictAsAnEditedIssueAndMovesOn(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{conflicts: map[string]bool{"github:issue:" + testRepo + "#7": true}}
	withIssueLoopSeams(t, queue, nil, nil)
	removed := withRetryIssues(t, []gh.Issue{{Number: 7, Title: "edited", URL: "u"}})

	_, err := runHandler(t, "issue.promote", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline})
	require.NoError(t, err)

	require.Len(t, queue.created, 2)
	assert.Equal(t, "github:issue:"+testRepo+"#7:g2", queue.created[1].IdempotencyKey)
	assert.Equal(t, []string{issueLoopRetryLabel}, *removed)
}

func TestIssuePromoteRetryGivesUpAfterTheGenerationCap(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{createCode: http.StatusConflict}
	withIssueLoopSeams(t, queue, nil, nil)
	removed := withRetryIssues(t, []gh.Issue{{Number: 7, Title: "stuck", URL: "u"}})

	out, err := runHandler(t, "issue.promote", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline})
	require.NoError(t, err)
	assert.Len(t, queue.created, promoteMaxGenerations)
	assert.Empty(t, *removed, "an issue that could not be queued keeps its label")
	assert.Contains(t, out, "needs to look")
}

func TestIssuePromoteRetryDryRunCreatesNothing(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{}
	withIssueLoopSeams(t, queue, nil, nil)
	withRetryIssues(t, []gh.Issue{{Number: 7, Title: "x", URL: "u"}})

	_, err := runHandler(t, "issue.promote", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline, "dry-run": true})
	require.NoError(t, err)
	assert.Empty(t, queue.created)
}

func TestIssuePromoteRetrySkipsAParkedGenerationReconcileHasNotWithdrawnYet(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{parked: map[string]bool{"task-#7": true}}
	withIssueLoopSeams(t, queue, nil, nil)
	removed := withRetryIssues(t, []gh.Issue{{Number: 7, Title: "parked", URL: "u"}})

	_, err := runHandler(t, "issue.promote", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline})
	require.NoError(t, err)

	require.Len(t, queue.created, 2, "the parked generation is dead, so the next one is queued")
	assert.Equal(t, []string{issueLoopRetryLabel}, *removed)
}

func droppedTask(id string, issue int, age time.Duration) map[string]any {
	return map[string]any{"id": id, "stage": issueLoopWithdrawnStage, "updated_at_ms": time.Now().Add(-age).UnixMilli(),
		"metadata": map[string]any{repoKey: testRepo, "issue_number": issue}}
}

func orphanPR(draft bool, conclusion string) *gh.PullRequest {
	return &gh.PullRequest{Number: 9, URL: "https://x/pr/9", State: "OPEN", IsDraft: draft, Body: issueLoopPRTag + " for #1.",
		Checks: []gh.CheckOutcome{{Name: "ci", Conclusion: conclusion}}}
}

func TestIssueReconcileMergesAGreenDeliveredPRWhoseTaskWasDropped(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{notDoing: []map[string]any{droppedTask("t", 1, time.Hour)}}
	_, _, armed := withIssueLoopSeams(t, queue, nil, map[string]*gh.PullRequest{"t": orphanPR(false, "SUCCESS")})

	_, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline})
	require.NoError(t, err)
	assert.Equal(t, []int{9}, *armed)
}

func TestIssueReconcileOnlyMarksAnOrphanedDraftReadyThenWaitsForItsChecks(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	queue := &fakeQueue{notDoing: []map[string]any{droppedTask("t", 1, time.Hour)}}
	_, _, armed := withIssueLoopSeams(t, queue, nil, map[string]*gh.PullRequest{"t": orphanPR(true, "SUCCESS")})
	ready := withReadySeam(t)

	_, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline})
	require.NoError(t, err)
	assert.Equal(t, []int{9}, *ready)
	assert.Empty(t, *armed, "leaving draft can start new checks; the next tick judges them")
}

func TestIssueReconcileLeavesOrphanedPRsThatAreNotSafeToMerge(t *testing.T) { //nolint:paralleltest // runHandler swaps os.Stdout
	human := orphanPR(false, "SUCCESS")
	human.Body = "a person's PR"

	cases := map[string]struct {
		pr  *gh.PullRequest
		age time.Duration
	}{
		"red":       {orphanPR(false, "FAILURE"), time.Hour},
		"pending":   {orphanPR(false, "PENDING"), time.Hour},
		"not ours":  {human, time.Hour},
		"too old":   {orphanPR(false, "SUCCESS"), 48 * time.Hour},
		"no checks": {&gh.PullRequest{Number: 9, State: "OPEN", Body: issueLoopPRTag}, time.Hour},
	}

	for name, c := range cases {
		queue := &fakeQueue{notDoing: []map[string]any{droppedTask("t", 1, c.age)}}
		_, _, armed := withIssueLoopSeams(t, queue, nil, map[string]*gh.PullRequest{"t": c.pr})

		_, err := runHandler(t, "issue.reconcile", map[string]any{repoKey: testRepoFlag, pipelineFlag: testPipeline})
		require.NoError(t, err, name)
		assert.Empty(t, *armed, name)
	}
}
