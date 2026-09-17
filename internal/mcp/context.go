//nolint:goconst,gocritic // JSON field keys match the stamp; Server is passed by value throughout mcp.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	observationBytes  = 1 << 20
	contextGitTimeout = 2 * time.Second
	observationBuffer = 4096
)

func contextSchema() map[string]any {
	props := map[string]any{}
	for _, name := range []string{"cwd", "session_id", "task_id", "harness"} {
		props[name] = map[string]any{"type": "string"}
	}

	return objectSchema(props, nil)
}

// workspaceContext reads the same runtime sources used by Lightwave's hooks
// and CLI. It never creates a second registry or infers a task from a branch.
func (s Server) workspaceContext(ctx context.Context, args map[string]string) toolCallResult {
	cwd := args["cwd"]
	cwdSource := "caller"

	if cwd == "" {
		cwd, _ = os.Getwd()
		cwdSource = "server_fallback"
	}

	if !filepath.IsAbs(cwd) {
		return toolResult(true, "cwd must be an absolute path")
	}

	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return toolResult(true, "cwd must be an existing directory")
	}

	root := filepath.Join(s.HomeDir, ".lightwave")
	contract, contractSource := activeContract(cwd, root)

	job := map[string]string{
		"task_id": args["task_id"], "session_id": args["session_id"],
		"harness": args["harness"], "persona": s.Persona,
		"contract": contract, "contract_source": contractSource,
	}
	for key, env := range map[string]string{"task_id": "LW_TASK_ID", "session_id": "LW_SESSION_ID", "harness": "LIGHTWAVE_HARNESS"} {
		if job[key] == "" {
			job[key] = os.Getenv(env)
		}
	}

	if job["session_id"] == "" {
		job["session_id"] = os.Getenv("CODEX_THREAD_ID")
	}

	if body, err := os.ReadFile(contract); err == nil {
		var fields struct {
			Task  string `yaml:"task"`
			Claim string `yaml:"claim"`
		}
		if yaml.Unmarshal(body, &fields) == nil {
			job["contract_task"] = fields.Task
			job["objective"] = fields.Claim
		}
	}

	sessions, sessionStatus := sessionObservations(filepath.Join(root, "observability", "sessions.jsonl"))
	agents, agentStatus := agentObservations(filepath.Join(root, "agents"))
	legacy := s.contextGet()

	var payload map[string]any
	if err := json.Unmarshal([]byte(legacy.Content[0]["text"]), &payload); err != nil {
		return toolResult(true, err.Error())
	}

	payload["schema_version"] = "1.0.0"
	payload["workspace"] = map[string]string{
		"cwd": cwd, "cwd_source": cwdSource,
		"repo_root": gitContext(ctx, cwd, "rev-parse", "--show-toplevel"),
		"branch":    gitContext(ctx, cwd, "branch", "--show-current"),
	}
	payload["job"] = job
	payload["runtime"] = map[string]string{
		"home": root, "core_root": s.CoreRoot, "nullhub": s.Base,
		"instances_path": filepath.Join(root, "config", "lightwave-ai", "instances.yaml"),
		"datastore_path": filepath.Join(root, "config", "datastore.yaml"),
	}
	payload["agents"] = agents
	fleet, fleetStatus := s.fleetObservations(ctx)
	payload["fleet"] = fleet
	payload["sessions"] = sessions
	payload["sources"] = map[string]string{"agents": agentStatus, "sessions": sessionStatus, "fleet": fleetStatus}
	payload["guidance"] = "Use task_read for the assigned task_id. Empty job fields mean unassigned or unavailable; do not invent an assignment. Session records show observations, not a live heartbeat. Process existence does not prove agent progress. Dispatch remains restricted by persona."

	return jsonResult(payload)
}

func (s Server) fleetObservations(ctx context.Context) ([]map[string]any, string) {
	rows := []map[string]any{}

	probe, cancel := context.WithTimeout(ctx, defaultHTTPTimeout)
	defer cancel()

	base := s.Base
	if base == "" {
		base = NullhubBase()
	}

	req, err := http.NewRequestWithContext(probe, http.MethodGet, strings.TrimRight(base, "/")+"/api/instances", nil)
	if err != nil {
		return rows, "unavailable: " + err.Error()
	}

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return rows, "unavailable: " + err.Error()
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return rows, "unavailable: " + resp.Status
	}

	var body struct {
		Instances map[string]map[string]map[string]any `json:"instances"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, observationBytes)).Decode(&body); err != nil {
		return rows, "unavailable: " + err.Error()
	}

	if body.Instances == nil {
		return rows, "unavailable: missing instances"
	}

	for module, instances := range body.Instances {
		for name, instance := range instances {
			row := map[string]any{"module": module, "name": name}

			for _, field := range []string{"status", "pid", "uptime_seconds", "launch_mode"} {
				if instance[field] != nil {
					row[field] = instance[field]
				}
			}

			rows = append(rows, row)
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		return stringifyArg(rows[i]["module"])+stringifyArg(rows[i]["name"]) < stringifyArg(rows[j]["module"])+stringifyArg(rows[j]["name"])
	})

	return rows, "ok"
}

func gitContext(ctx context.Context, cwd string, args ...string) string {
	probe, cancel := context.WithTimeout(ctx, contextGitTimeout)
	defer cancel()

	cmd := exec.CommandContext(probe, "git", append([]string{"-C", cwd}, args...)...)
	// A parent hook may carry git location variables for a different checkout.
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			cmd.Env = append(cmd.Env, value)
		}
	}

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

func activeContract(cwd, root string) (string, string) {
	if path := os.Getenv("LW_VALIDITY_CONTRACT"); path != "" {
		return path, "env"
	}

	marker := filepath.Join(cwd, ".lightwave", "contract")
	if body, err := os.ReadFile(marker); err == nil {
		path := strings.TrimSpace(string(body))
		if path != "" {
			if !filepath.IsAbs(path) {
				path = filepath.Join(cwd, path)
			}

			return path, "cwd"
		}
	}

	path := filepath.Join(root, "config", "contracts", "active")
	if _, err := os.Stat(path); err == nil {
		return path, "global"
	}

	return "", "none"
}

// Read a bounded tail of the existing lifecycle channel. Start without end
// remains unconfirmed: hooks can miss exits and Stop can mean a turn boundary.
func sessionObservations(path string) ([]map[string]any, string) {
	rows := []map[string]any{}

	f, err := os.Open(path)
	if err != nil {
		return rows, "unavailable: " + err.Error()
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return rows, "unavailable: " + err.Error()
	}

	status := "ok"

	if info.Size() > observationBytes {
		if _, err = f.Seek(-observationBytes, io.SeekEnd); err != nil {
			return rows, err.Error()
		}

		status = "tail_only"
	}

	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, observationBuffer), observationBytes)

	if status == "tail_only" {
		scan.Scan()
	}

	latest := map[string]map[string]any{}

	for scan.Scan() {
		var row map[string]any
		if json.Unmarshal(scan.Bytes(), &row) != nil {
			continue
		}

		id, _ := row["session_id"].(string)
		if id == "" {
			continue
		}

		view := map[string]any{"session_id": id, "ts": row["ts"], "action_type": row["action_type"], "state": "unconfirmed"}
		if row["action_type"] == "session_end" {
			view["state"] = "end_observed"
		}

		for _, key := range []string{"agent_id", "harness", "cwd", "task_id"} {
			if row[key] != nil {
				view[key] = row[key]
			}
		}

		if identity, ok := row["agent_identity"].(map[string]any); ok {
			view["persona"] = identity["persona_slug"]
		}

		latest[id] = view
	}

	if err := scan.Err(); err != nil {
		status = "partial: " + err.Error()
	}

	for _, row := range latest {
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return stringifyArg(rows[i]["ts"]) > stringifyArg(rows[j]["ts"]) })

	if len(rows) > defaultListLimit {
		rows = rows[:defaultListLimit]
		status = "latest_50"
	}

	return rows, status
}

func agentObservations(dir string) ([]map[string]any, string) {
	rows := []map[string]any{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return rows, "unavailable: " + err.Error()
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var row map[string]any
		if json.Unmarshal(body, &row) != nil {
			continue
		}

		view := map[string]any{}

		for _, key := range []string{"id", "task_id", "persona", "repo", "worktree", "branch", "shell", "pid", "status", "started_at"} {
			if row[key] != nil {
				view[key] = row[key]
			}
		}

		pid, _ := row["pid"].(float64)
		view["process_alive"] = pid > 0 && pidAlive(int(pid))
		rows = append(rows, view)
	}

	sort.Slice(rows, func(i, j int) bool { return stringifyArg(rows[i]["started_at"]) > stringifyArg(rows[j]["started_at"]) })

	if len(rows) > defaultListLimit {
		return rows[:defaultListLimit], "latest_50"
	}

	return rows, "ok"
}
