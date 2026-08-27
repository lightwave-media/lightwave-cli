package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lightwave-media/lightwave-cli/internal/db"
)

const (
	defaultListLimit   = 50
	maxInstanceNameLen = 64
	maxDispatchBody    = 1 << 20
	dispatchTimeout    = 60 * time.Second
	dbCallTimeout      = 3 * time.Second
	nullhubProbeWait   = 3 * time.Second
)

type toolDef struct {
	InputSchema map[string]any `json:"inputSchema"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolCallResult struct {
	Content []map[string]string `json:"content"`
	IsError bool                `json:"isError,omitempty"`
}

func toolResult(isError bool, text string) toolCallResult {
	return toolCallResult{
		Content: []map[string]string{{"type": "text", "text": text}},
		IsError: isError,
	}
}

func objectSchema(props map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	return schema
}

func toolsFor(tier Tier) []toolDef {
	read := []toolDef{
		{Name: "queue_read", Description: "Read agile queue/board state (tasks).", InputSchema: objectSchema(map[string]any{
			"status": map[string]any{"type": "string", "description": "Optional status filter"},
			"limit":  map[string]any{"type": "string", "description": "Max rows (default 50)"},
		}, nil)},
		{Name: "epic_read", Description: "Read an epic by id, or list epics if id is omitted.", InputSchema: objectSchema(map[string]any{
			"id": map[string]any{"type": "string"},
		}, nil)},
		{Name: "story_read", Description: "Read a story by id, or list stories if id is omitted.", InputSchema: objectSchema(map[string]any{
			"id": map[string]any{"type": "string"},
		}, nil)},
		{Name: "task_read", Description: "Read a task by id, or list tasks if id is omitted.", InputSchema: objectSchema(map[string]any{
			"id":     map[string]any{"type": "string"},
			"status": map[string]any{"type": "string"},
		}, nil)},
		{Name: "context_get", Description: "Show Lightwave runtime context (instance bindings + composer status).", InputSchema: objectSchema(map[string]any{}, nil)},
		{Name: "context_refresh", Description: "Refresh Lightwave runtime context (same surface as lw context refresh).", InputSchema: objectSchema(map[string]any{}, nil)},
	}
	if !tier.allowsWrite() {
		return read
	}

	write := []toolDef{
		{Name: "epic_write", Description: "Create an epic via the lw db validation path.", InputSchema: objectSchema(map[string]any{
			"name":     map[string]any{"type": "string"},
			"status":   map[string]any{"type": "string"},
			"priority": map[string]any{"type": "string"},
		}, []string{"name"})},
		{Name: "story_write", Description: "Create a user story via the lw db validation path.", InputSchema: objectSchema(map[string]any{
			"name":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"priority":    map[string]any{"type": "string"},
			"epic_id":     map[string]any{"type": "string"},
		}, []string{"name"})},
		{Name: "task_write", Description: "Create or update a task via the lw db validation path.", InputSchema: objectSchema(map[string]any{
			"id":          map[string]any{"type": "string", "description": "If set, update this task"},
			"title":       map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"status":      map[string]any{"type": "string"},
			"priority":    map[string]any{"type": "string"},
			"epic_id":     map[string]any{"type": "string"},
		}, nil)},
	}

	out := make([]toolDef, 0, len(read)+len(write)+1)
	out = append(out, read...)
	out = append(out, write...)

	if tier.allowsDispatch() {
		out = append(out, toolDef{
			Name:        "dispatch_agent",
			Description: "Dispatch a message to a managed nullclaw instance through nullhub.",
			InputSchema: objectSchema(map[string]any{
				"name":    map[string]any{"type": "string", "description": "Nullclaw instance name"},
				"message": map[string]any{"type": "string"},
				"agent":   map[string]any{"type": "string"},
			}, []string{"name", "message"}),
		})
	}

	return out
}

func (s Server) callTool(ctx context.Context, tier Tier, raw json.RawMessage) toolCallResult {
	var params callParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return toolResult(true, "invalid tools/call params")
	}

	if !toolAllowed(tier, params.Name) {
		return toolResult(true, fmt.Sprintf("tool %q not advertised for this persona tier", params.Name))
	}

	args := parseToolArgs(params.Arguments)

	switch params.Name {
	case "queue_read", "task_read":
		return s.taskRead(ctx, args)
	case "epic_read":
		return s.epicRead(ctx, args)
	case "story_read":
		return s.storyRead(ctx, args)
	case "epic_write":
		return s.epicWrite(ctx, args)
	case "story_write":
		return s.storyWrite(ctx, args)
	case "task_write":
		return s.taskWrite(ctx, args)
	case "context_get", "context_refresh":
		return s.contextGet()
	case "dispatch_agent":
		return s.dispatchAgent(ctx, args)
	default:
		return toolResult(true, "unknown tool")
	}
}

func toolAllowed(tier Tier, name string) bool {
	for _, t := range toolsFor(tier) {
		if t.Name == name {
			return true
		}
	}

	return false
}

func parseToolArgs(raw json.RawMessage) map[string]string {
	args := map[string]string{}
	if len(raw) == 0 {
		return args
	}

	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return args
	}

	for k, v := range generic {
		args[k] = stringifyArg(v)
	}

	return args
}

func stringifyArg(v any) string {
	if v == nil {
		return ""
	}

	if s, ok := v.(string); ok {
		return s
	}

	return fmt.Sprint(v)
}

func (s Server) withDB(ctx context.Context, fn func(*pgxpool.Pool) (any, error)) toolCallResult {
	ctx, cancel := context.WithTimeout(ctx, dbCallTimeout)
	defer cancel()

	pool, err := s.Connect(ctx)
	if err != nil {
		return toolResult(true, encodePlaneDown(planeDown("postgres", err.Error())))
	}

	if pool != nil {
		defer pool.Close()
	}

	out, err := fn(pool)
	if err != nil {
		if errors.Is(err, db.ErrDBUnavailable) {
			return toolResult(true, encodePlaneDown(planeDown("postgres", err.Error())))
		}

		return toolResult(true, err.Error())
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return toolResult(true, err.Error())
	}

	return toolResult(false, string(b))
}

func (s Server) taskRead(ctx context.Context, args map[string]string) toolCallResult {
	return s.withDB(ctx, func(pool *pgxpool.Pool) (any, error) {
		if id := args["id"]; id != "" {
			return db.GetTask(ctx, pool, id)
		}

		return db.ListTasks(ctx, pool, db.TaskListOptions{
			Status: args["status"],
			Limit:  listLimit(args["limit"]),
		})
	})
}

func (s Server) epicRead(ctx context.Context, args map[string]string) toolCallResult {
	return s.withDB(ctx, func(pool *pgxpool.Pool) (any, error) {
		if id := args["id"]; id != "" {
			return db.GetEpic(ctx, pool, id)
		}

		return db.ListEpics(ctx, pool, db.EpicListOptions{Limit: defaultListLimit})
	})
}

func (s Server) storyRead(ctx context.Context, args map[string]string) toolCallResult {
	return s.withDB(ctx, func(pool *pgxpool.Pool) (any, error) {
		if id := args["id"]; id != "" {
			return db.GetStory(ctx, pool, id)
		}

		return db.ListStories(ctx, pool, db.StoryListOptions{Limit: defaultListLimit})
	})
}

func (s Server) epicWrite(ctx context.Context, args map[string]string) toolCallResult {
	return s.withDB(ctx, func(pool *pgxpool.Pool) (any, error) {
		status := args["status"]
		if status == "" {
			status = "draft"
		}

		return db.CreateEpic(ctx, pool, db.EpicCreateOptions{
			Name:     args["name"],
			Status:   status,
			Priority: args["priority"],
		})
	})
}

func (s Server) storyWrite(ctx context.Context, args map[string]string) toolCallResult {
	return s.withDB(ctx, func(pool *pgxpool.Pool) (any, error) {
		return db.CreateStory(ctx, pool, db.StoryCreateOptions{
			Name:        args["name"],
			Description: args["description"],
			Priority:    args["priority"],
			EpicID:      args["epic_id"],
		})
	})
}

func (s Server) taskWrite(ctx context.Context, args map[string]string) toolCallResult {
	return s.withDB(ctx, func(pool *pgxpool.Pool) (any, error) {
		if id := args["id"]; id != "" {
			return db.UpdateTask(ctx, pool, id, taskUpdateFromArgs(args))
		}

		if args["title"] == "" {
			return nil, errors.New("title is required to create a task")
		}

		return db.CreateTask(ctx, pool, db.TaskCreateOptions{
			Title:       args["title"],
			Description: args["description"],
			Priority:    args["priority"],
			EpicID:      args["epic_id"],
		})
	})
}

func taskUpdateFromArgs(args map[string]string) db.TaskUpdateOptions {
	opts := db.TaskUpdateOptions{}
	if v := args["status"]; v != "" {
		opts.Status = &v
	}

	if v := args["priority"]; v != "" {
		opts.Priority = &v
	}

	if v := args["title"]; v != "" {
		opts.Title = &v
	}

	if v := args["description"]; v != "" {
		opts.Description = &v
	}

	return opts
}

func (s Server) contextGet() toolCallResult {
	path := filepath.Join(s.HomeDir, ".lightwave", "config", "lightwave-ai", "instances.yaml")
	body, err := os.ReadFile(path)
	payload := map[string]any{
		"composer":  "lw context init/refresh/show are not yet wired (6-layer composer gap)",
		"instances": string(body),
		"path":      path,
	}

	if err != nil {
		payload["instances_error"] = err.Error()
		payload["instances"] = ""
	}

	b, marshalErr := json.MarshalIndent(payload, "", "  ")
	if marshalErr != nil {
		return toolResult(true, marshalErr.Error())
	}

	return toolResult(false, string(b))
}

func (s Server) dispatchAgent(ctx context.Context, args map[string]string) toolCallResult {
	name := strings.TrimSpace(args["name"])
	message := args["message"]

	if name == "" || message == "" {
		return toolResult(true, "name and message are required")
	}

	if err := validInstanceName(name); err != nil {
		return toolResult(true, err.Error())
	}

	probeCtx, probeCancel := context.WithTimeout(ctx, nullhubProbeWait)
	defer probeCancel()

	if err := probeNullhub(probeCtx, s.Client, s.Base); err != nil {
		return toolResult(true, encodePlaneDown(planeDown("nullhub", err.Error())))
	}

	return s.invokeNullclaw(ctx, name, message, args["agent"])
}

func (s Server) invokeNullclaw(ctx context.Context, name, message, agent string) toolCallResult {
	invokeCtx, invokeCancel := context.WithTimeout(ctx, dispatchTimeout)
	defer invokeCancel()

	body, err := json.Marshal(map[string]string{
		"message": message,
		"agent":   agent,
	})
	if err != nil {
		return toolResult(true, err.Error())
	}

	url := strings.TrimRight(s.Base, "/") + "/api/instances/nullclaw/" + name + "/agent"

	req, err := http.NewRequestWithContext(invokeCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return toolResult(true, err.Error())
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.Client.Do(req)
	if err != nil {
		return toolResult(true, encodePlaneDown(planeDown("nullhub", err.Error())))
	}

	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxDispatchBody))
	if resp.StatusCode >= http.StatusInternalServerError {
		return toolResult(true, encodePlaneDown(planeDown("nullhub", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, respBody))))
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return toolResult(true, strings.TrimSpace(string(respBody)))
	}

	return toolResult(false, string(respBody))
}

func validInstanceName(name string) error {
	if len(name) == 0 || len(name) > maxInstanceNameLen {
		return errors.New("invalid instance name")
	}

	for _, c := range name {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '-' {
			continue
		}

		return errors.New("invalid instance name")
	}

	return nil
}

func listLimit(raw string) int {
	if raw == "" {
		return defaultListLimit
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultListLimit
	}

	return n
}
