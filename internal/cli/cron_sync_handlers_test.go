package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/testutil"
)

// secretMap writes the consumer records `lw cron sync` checks entitlements
// against, under the test HOME cronWorkspace set (~/.lightwave/specs/security).
func secretMap(t *testing.T, records map[string][]string) {
	t.Helper()

	dir := filepath.Join(os.Getenv("HOME"), ".lightwave", "specs", "security")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "daemon_secrets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret_rotation.yaml"), []byte("records: []\n"), 0o644))

	for id, names := range records {
		var b strings.Builder

		b.WriteString("id: " + id + "\ndaemon_id: " + id + "\nsecret_loadings:\n")

		for _, n := range names {
			b.WriteString("  - ssm_path: /lightwave/prod/" + n + "\n    target_env_var: " + n + "\n")
		}

		require.NoError(t, os.WriteFile(filepath.Join(dir, "daemon_secrets", id+".yaml"), []byte(b.String()), 0o644))
	}
}

const sessionJob = `id: triage
name: Backlog triage
schedule: "0 */4 * * *"
job_class: maintenance
persona: v_scrum-manager
daemon_secrets_ref: cron-triage
secret_names: [NULLTICKETS_API_TOKEN]
dispatch:
  kind: agent_session
  target: v_scrum-manager
  prompt_template: Triage the needs-triage issues.
  args:
    repos: [lightwave-cli]
`

const legacyJob = `id: legacy
schedule: "*/30 * * * *"
handler:
  transport: cli
  command: lw scrum sync --json
persona: v_scrum-manager
`

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestCronSyncRendersAnEntitledJobAndRefusesTheRestWithReasons(t *testing.T) {
	jobsDir, agentsDir := cronWorkspace(t)
	secretMap(t, map[string][]string{"cron-triage": {"NULLTICKETS_API_TOKEN"}})
	writeJob(t, jobsDir, "triage.yaml", sessionJob)
	writeJob(t, jobsDir, "legacy.yaml", legacyJob)

	out, err := testutil.RunHandler(t, "cron.sync", nil, map[string]any{jsonFlag: true, dryRunFlag: true})
	require.NoError(t, err)

	var result struct {
		Rendered []struct {
			ID          string   `json:"id"`
			Label       string   `json:"label"`
			SecretNames []string `json:"secret_names"`
		} `json:"rendered"`
		Refused []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		} `json:"refused"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result), out)

	require.Len(t, result.Rendered, 1)
	assert.Equal(t, "com.lightwave.cron.triage", result.Rendered[0].Label)
	assert.Equal(t, []string{"NULLTICKETS_API_TOKEN"}, result.Rendered[0].SecretNames, "names, never values")

	require.Len(t, result.Refused, 1)
	assert.Equal(t, "legacy", result.Refused[0].ID)
	assert.Contains(t, result.Refused[0].Reason, "no daemon_secrets_ref")

	// sync renders; it never writes or loads a launchd job.
	entries, _ := os.ReadDir(agentsDir)
	assert.Empty(t, entries)
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestCronSyncWithNoSecretMapRefusesEveryJob(t *testing.T) {
	jobsDir, _ := cronWorkspace(t)
	writeJob(t, jobsDir, "triage.yaml", sessionJob)

	out, err := testutil.RunHandler(t, "cron.sync", nil, map[string]any{jsonFlag: true})
	require.NoError(t, err)
	assert.Contains(t, out, "secret map unreadable")
	assert.Contains(t, out, `"rendered": []`)
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestCronSyncPrintsTheNixModuleItDoesNotWrite(t *testing.T) {
	jobsDir, _ := cronWorkspace(t)
	secretMap(t, map[string][]string{"cron-triage": {"NULLTICKETS_API_TOKEN"}})
	writeJob(t, jobsDir, "triage.yaml", sessionJob)

	out, err := testutil.RunHandler(t, "cron.sync", nil, map[string]any{})
	require.NoError(t, err)
	assert.Contains(t, out, `launchd.user.agents."com.lightwave.cron.triage".serviceConfig`)
	assert.Contains(t, out, `"--only" "NULLTICKETS_API_TOKEN"`)
}

const shellJob = `id: knowledge
schedule: "@hourly"
persona: v_scrum-manager
daemon_secrets_ref: cron-knowledge
dispatch:
  kind: shell_command
  target: %s
`

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestCronRunShellCommandRunsArgvAndRefusesAShell(t *testing.T) {
	jobsDir, _ := cronWorkspace(t)
	secretMap(t, map[string][]string{"cron-knowledge": nil})

	writeJob(t, jobsDir, "knowledge.yaml", strings.Replace(shellJob, "%s", "lw knowledge sync --json", 1))
	out, err := testutil.RunHandler(t, "cron.run", []string{"knowledge"}, map[string]any{dryRunFlag: true})
	require.NoError(t, err)
	assert.Contains(t, out, `would run ["lw" "knowledge" "sync" "--json"]`)

	writeJob(t, jobsDir, "knowledge.yaml", strings.Replace(shellJob, "%s", `"lw knowledge sync | tee out"`, 1))
	_, err = testutil.RunHandler(t, "cron.run", []string{"knowledge"}, map[string]any{dryRunFlag: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a shell")
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestCronRunRefusesAJobSyncWouldRefuse(t *testing.T) {
	jobsDir, _ := cronWorkspace(t)
	secretMap(t, nil)
	writeJob(t, jobsDir, "legacy.yaml", legacyJob)

	_, err := testutil.RunHandler(t, "cron.run", []string{"legacy"}, map[string]any{dryRunFlag: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no daemon_secrets_ref")
}

// fakeRoutineQueue is nulltickets as cron run sees it: one routine pipeline,
// and task creation keyed by Idempotency-Key, replaying the same body.
type fakeRoutineQueue struct {
	bodies map[string]string
	keys   []string
	mu     sync.Mutex
}

func (q *fakeRoutineQueue) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /pipelines", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"pipe-routine","name":"routine-v_scrum-manager"},{"id":"pipe-fix","name":"fix-lightwave-cli"}]`))
	})
	mux.HandleFunc("POST /tasks", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Header.Get("Idempotency-Key")

		q.mu.Lock()
		defer q.mu.Unlock()

		q.keys = append(q.keys, key)
		if prev, ok := q.bodies[key]; ok && prev != string(body) {
			w.WriteHeader(http.StatusConflict)
			return
		}

		q.bodies[key] = string(body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"task-1"}`))
	})

	return mux
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestCronRunAgentSessionQueuesOneIdempotentTaskPerWindow(t *testing.T) {
	jobsDir, _ := cronWorkspace(t)
	secretMap(t, map[string][]string{"cron-triage": {"NULLTICKETS_API_TOKEN"}})
	writeJob(t, jobsDir, "triage.yaml", sessionJob)

	queue := &fakeRoutineQueue{bodies: map[string]string{}}
	server := httptest.NewServer(queue.handler())
	t.Cleanup(server.Close)
	t.Setenv("NULLTICKETS_URL", server.URL)
	t.Setenv("NULLTICKETS_API_TOKEN", "test-bearer")

	first, err := testutil.RunHandler(t, "cron.run", []string{"triage"}, map[string]any{jsonFlag: true})
	require.NoError(t, err)
	_, err = testutil.RunHandler(t, "cron.run", []string{"triage"}, map[string]any{jsonFlag: true})
	require.NoError(t, err)

	require.Len(t, queue.keys, 2)
	assert.Equal(t, queue.keys[0], queue.keys[1], "two fires of one window are one key")
	assert.Regexp(t, `^routine:triage:\d{4}-\d{2}-\d{2}T\d{2}:00Z$`, queue.keys[0])
	assert.Len(t, queue.bodies, 1, "and one body: a repeated fire is a replay, not a second task")
	assert.Contains(t, first, `"action":"queued"`)

	var task map[string]any
	require.NoError(t, json.Unmarshal([]byte(queue.bodies[queue.keys[0]]), &task))
	assert.Equal(t, "pipe-routine", task["pipeline_id"])
	assert.Contains(t, task["description"], "Triage the needs-triage issues.")
}
