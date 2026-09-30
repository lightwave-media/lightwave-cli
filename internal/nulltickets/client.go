// Package nulltickets is the lw side of the nulltickets HTTP API for the issue
// loop: create a task, list a stage, claim one task by id, move it. It speaks
// the same vocabulary as the two adapters that already write to the queue —
// the lw-webhook receiver and `lw knowledge promote` — and locates the server
// the way they do (NULLTICKETS_URL, NULLTICKETS_API_TOKEN by name).
package nulltickets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultURL = "http://127.0.0.1:7700"

	requestTimeout = 10 * time.Second
	bodyCap        = 1 << 20
)

// ErrNotClaimable is a targeted claim that nulltickets answered 204 to: the
// task is leased, gone, in a stage the role does not own, or blocked.
var ErrNotClaimable = errors.New("task is not claimable right now")

type Client struct {
	http  *http.Client
	Base  string
	Token string
}

func New(base, token string) *Client {
	if base == "" {
		base = DefaultURL
	}

	return &Client{http: &http.Client{Timeout: requestTimeout}, Base: strings.TrimRight(base, "/"), Token: token}
}

type TaskRequest struct {
	Metadata    map[string]any `json:"metadata"`
	PipelineID  string         `json:"pipeline_id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Priority    int            `json:"priority,omitempty"`
}

// Task is the subset of a nulltickets task the loop reads.
type Task struct {
	Metadata    map[string]any `json:"metadata"`
	ID          string         `json:"id"`
	PipelineID  string         `json:"pipeline_id"`
	Stage       string         `json:"stage"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	TaskVersion int            `json:"task_version"`
}

// Claim is the lease a targeted claim returns.
type Claim struct {
	RunID      string `json:"-"`
	LeaseID    string `json:"lease_id"`
	LeaseToken string `json:"lease_token"`
	Task       Task   `json:"task"`
}

// CreateTask posts one task; idempotencyKey makes a repeat a replay, never a
// second task.
func (c *Client) CreateTask(ctx context.Context, idempotencyKey string, request TaskRequest) (string, error) {
	var created struct {
		ID string `json:"id"`
	}

	status, err := c.do(ctx, http.MethodPost, "/tasks", request, map[string]string{"Idempotency-Key": idempotencyKey}, c.Token, &created)
	if err != nil {
		return "", err
	}

	if status < 200 || status >= 300 {
		return "", fmt.Errorf("nulltickets POST /tasks returned HTTP %d", status)
	}

	if created.ID == "" {
		return "", errors.New("nulltickets did not return a task id")
	}

	return created.ID, nil
}

// StageRole is the agent_role a pipeline assigns to one of its stages — the
// role a claim must present to lease a task sitting in that stage.
func (c *Client) StageRole(ctx context.Context, pipelineID, stage string) (string, error) {
	var pipeline struct {
		Definition struct {
			States map[string]struct {
				AgentRole string `json:"agent_role"`
			} `json:"states"`
		} `json:"definition"`
	}

	status, err := c.do(ctx, http.MethodGet, "/pipelines/"+pipelineID, nil, nil, c.Token, &pipeline)
	if err != nil {
		return "", err
	}

	if status != http.StatusOK {
		return "", fmt.Errorf("nulltickets GET /pipelines/%s returned HTTP %d", pipelineID, status)
	}

	state, ok := pipeline.Definition.States[stage]
	if !ok {
		return "", fmt.Errorf("pipeline %s has no stage %q", pipelineID, stage)
	}

	if state.AgentRole == "" {
		return "", fmt.Errorf("pipeline %s stage %q has no agent_role, so nothing can claim it", pipelineID, stage)
	}

	return state.AgentRole, nil
}

// ListTasks lists every task of pipeline in stage, following the cursor.
func (c *Client) ListTasks(ctx context.Context, pipelineID, stage string) ([]Task, error) {
	var all []Task

	cursor := ""

	for {
		// Encoded: a cursor is often base64, and a raw + / = corrupts it after
		// the first page.
		query := url.Values{"pipeline_id": {pipelineID}, "stage": {stage}, "limit": {"100"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}

		path := "/tasks?" + query.Encode()

		var page struct {
			NextCursor string `json:"next_cursor"`
			Tasks      []Task `json:"tasks"`
		}

		status, err := c.do(ctx, http.MethodGet, path, nil, nil, c.Token, &page)
		if err != nil {
			return nil, err
		}

		if status != http.StatusOK {
			return nil, fmt.Errorf("nulltickets GET /tasks returned HTTP %d", status)
		}

		all = append(all, page.Tasks...)
		if page.NextCursor == "" || len(page.Tasks) == 0 {
			return all, nil
		}

		cursor = page.NextCursor
	}
}

// ClaimTask leases exactly taskID as agentID acting in role.
func (c *Client) ClaimTask(ctx context.Context, agentID, role, taskID string, leaseTTL time.Duration) (*Claim, error) {
	body := map[string]any{"agent_id": agentID, "agent_role": role, "task_id": taskID, "lease_ttl_ms": leaseTTL.Milliseconds()}

	var raw struct {
		LeaseID    string `json:"lease_id"`
		LeaseToken string `json:"lease_token"`
		Run        struct {
			ID string `json:"id"`
		} `json:"run"`
		Task Task `json:"task"`
	}

	status, err := c.do(ctx, http.MethodPost, "/leases/claim", body, nil, c.Token, &raw)
	if err != nil {
		return nil, err
	}

	switch status {
	case http.StatusOK:
	case http.StatusNoContent:
		return nil, ErrNotClaimable
	default:
		return nil, fmt.Errorf("nulltickets POST /leases/claim returned HTTP %d", status)
	}

	if raw.Task.ID != taskID {
		// The server predates targeted claims and handed out another task. Its
		// lease will lapse; never act on it.
		return nil, fmt.Errorf("nulltickets claimed %s instead of %s: it does not support task_id claims", raw.Task.ID, taskID)
	}

	return &Claim{Task: raw.Task, RunID: raw.Run.ID, LeaseID: raw.LeaseID, LeaseToken: raw.LeaseToken}, nil
}

// Transition fires trigger on the claimed run, with the lease token as bearer.
func (c *Client) Transition(ctx context.Context, claim *Claim, trigger, instructions string) error {
	body := map[string]any{"trigger": trigger, "expected_stage": claim.Task.Stage}
	if instructions != "" {
		body["instructions"] = instructions
	}

	status, err := c.do(ctx, http.MethodPost, "/runs/"+claim.RunID+"/transition", body, nil, claim.LeaseToken, nil)
	if err != nil {
		return err
	}

	if status < 200 || status >= 300 {
		return fmt.Errorf("nulltickets transition %q on run %s returned HTTP %d", trigger, claim.RunID, status)
	}

	return nil
}

// FailRun records why the claimed run could not finish and releases it to the
// task's retry policy.
func (c *Client) FailRun(ctx context.Context, claim *Claim, reason string) error {
	status, err := c.do(ctx, http.MethodPost, "/runs/"+claim.RunID+"/fail", map[string]any{"error": reason}, nil, claim.LeaseToken, nil)
	if err != nil {
		return err
	}

	if status < 200 || status >= 300 {
		return fmt.Errorf("nulltickets fail on run %s returned HTTP %d", claim.RunID, status)
	}

	return nil
}

func (c *Client) do(ctx context.Context, method, path string, payload any, headers map[string]string, bearer string, into any) (int, error) {
	var body io.Reader

	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}

		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return 0, err
	}

	req.Header.Set("Content-Type", "application/json")

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	response, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("nulltickets unreachable: %w", err)
	}

	raw, err := io.ReadAll(io.LimitReader(response.Body, bodyCap))
	_ = response.Body.Close()

	if err != nil {
		return response.StatusCode, err
	}

	if into != nil && len(raw) > 0 && response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := json.Unmarshal(raw, into); err != nil {
			return response.StatusCode, fmt.Errorf("nulltickets %s %s: unreadable reply: %w", method, path, err)
		}
	}

	return response.StatusCode, nil
}
