package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Status is the lifecycle state of a spawned agent session.
type Status string

const (
	StatusRunning Status = "running"
	StatusExited  Status = "exited"
	StatusError   Status = "error" // spawn failed before pid was obtained
)

// Agent is the persisted record of a sealed sub-session spawned by
// `lw agent spawn`. State lives at ~/.lightwave/agents/<id>.json — the
// JSON-on-disk encoding is the contract v_core polls.
//
// PID-only liveness tracking: when the inner session ends (success or
// crash), `lw agent status` discovers the dead pid and updates Status to
// "exited". ExitCode is best-effort (unavailable for orphaned children
// reparented to launchd after `lw` exits). Use the log file for outcome.
type Agent struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	Persona     string    `json:"persona"`
	Repo        string    `json:"repo"`         // absolute path to the repo the worktree was added to
	Worktree    string    `json:"worktree"`     // absolute path to the worktree
	Branch      string    `json:"branch"`       // branch the worktree is on
	Shell       string    `json:"shell"`        // binary invoked: claude / pi / …
	ShellArgs   []string  `json:"shell_args"`   // argv passed to Shell
	PID         int       `json:"pid"`          // 0 when StatusError
	ContextPath string    `json:"context_path"` // bundle file fed to the session
	LogPath     string    `json:"log_path"`     // captured stdout+stderr
	StartedAt   time.Time `json:"started_at"`
	ExitedAt    time.Time `json:"exited_at,omitzero"`
	ExitCode    *int      `json:"exit_code,omitempty"`
	Status      Status    `json:"status"`
	Error       string    `json:"error,omitempty"` // populated when Status == StatusError
}

// ShortID returns the first 8 chars of the agent UUID, used in branch
// names, worktree paths, and table rendering.
func (a *Agent) ShortID() string {
	if len(a.ID) >= 8 {
		return a.ID[:8]
	}
	return a.ID
}

// NewID returns a fresh agent UUID.
func NewID() string {
	return uuid.NewString()
}

// StateDir returns the on-disk root for persisted agent records, creating
// it if missing (0o700 — the log files may capture sensitive bundle text).
func StateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lightwave", "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return dir, nil
}

// StatePath returns the JSON-on-disk path for an agent.
func StatePath(id string) (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
}

// Save writes the agent record atomically (write-to-tmp + rename so a
// concurrent `lw agent list` never sees a partial JSON blob).
func (a *Agent) Save() error {
	path, err := StatePath(a.ID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads an agent record by ID. The lookup accepts the full UUID or
// a unique short-ID prefix (matches ShortID() length).
func Load(idOrPrefix string) (*Agent, error) {
	dir, err := StateDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var matches []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp.json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if id == idOrPrefix || strings.HasPrefix(id, idOrPrefix) {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("agent %q not found in %s", idOrPrefix, dir)
	case 1:
		return readAgentFile(filepath.Join(dir, matches[0]+".json"))
	default:
		sort.Strings(matches)
		return nil, fmt.Errorf("agent prefix %q is ambiguous: %v", idOrPrefix, matches)
	}
}

// List returns every persisted agent record, newest first.
func List() ([]*Agent, error) {
	dir, err := StateDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*Agent
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp.json") {
			continue
		}
		a, err := readAgentFile(filepath.Join(dir, name))
		if err != nil {
			// Skip unreadable entries rather than failing the whole list — a
			// corrupted file shouldn't hide healthy siblings from v_core.
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}

func readAgentFile(path string) (*Agent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a Agent
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &a, nil
}
