// Package agent provides worker lifecycle management for lw agents.
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State represents the current lifecycle state of an agent.
//
// Agents are persistent: they survive work completion and can be reused.
// The five states are:
//
//   - Working: Tmux session active, doing assigned work
//   - Idle: Work completed, session killed, worktree preserved for reuse
//   - Done: Agent called completion, transient before cleanup
//   - Stuck: Agent signaled it needs assistance
//   - Zombie: Tmux session exists but worktree is missing or cleanup failed
type State string

const (
	StateWorking State = "working"
	StateIdle    State = "idle"
	StateDone    State = "done"
	StateStuck   State = "stuck"
	StateZombie  State = "zombie"
)

// Role identifies the functional area an agent operates in.
type Role string

const (
	RoleBackend  Role = "backend"
	RoleFrontend Role = "frontend"
	RoleInfra    Role = "infra"
	RoleVCore    Role = "vcore"
)

// Agent represents a worker agent with filesystem-backed state.
type Agent struct {
	Name        string    `json:"name"`
	Role        Role      `json:"role"`
	State       State     `json:"state"`
	Repo        string    `json:"repo"`
	RepoPath    string    `json:"repo_path"`
	WorkDir     string    `json:"work_dir"`
	Branch      string    `json:"branch"`
	TaskID      string    `json:"task_id,omitempty"`
	Model       string    `json:"model,omitempty"`
	Prompt      string    `json:"prompt,omitempty"`
	TmuxSession string    `json:"tmux_session"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// BaseDir returns the root directory for all agent state.
func BaseDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".lw", "agents")
}

// ReposDir returns the directory for bare repo clones.
func ReposDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".lw", "repos")
}

// Dir returns this agent's state directory.
func (a *Agent) Dir() string {
	return filepath.Join(BaseDir(), a.Name)
}

// StateFile returns the path to this agent's persisted state.
func (a *Agent) StateFile() string {
	return filepath.Join(a.Dir(), "state.json")
}

// Save persists the agent state as JSON to disk.
func (a *Agent) Save() error {
	if err := os.MkdirAll(a.Dir(), 0755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}
	a.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent: %w", err)
	}
	if err := os.WriteFile(a.StateFile(), data, 0644); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

// Load reads an agent's state from disk by name.
func Load(name string) (*Agent, error) {
	path := filepath.Join(BaseDir(), name, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read agent %s: %w", name, err)
	}
	var a Agent
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("unmarshal agent %s: %w", name, err)
	}
	return &a, nil
}

// ListAll scans BaseDir and returns all agents with valid state files.
func ListAll() ([]*Agent, error) {
	base := BaseDir()
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agents dir: %w", err)
	}
	var agents []*Agent
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		a, err := Load(e.Name())
		if err != nil {
			continue
		}
		agents = append(agents, a)
	}
	return agents, nil
}

// SetState updates the agent's state and persists to disk.
func (a *Agent) SetState(state State) error {
	a.State = state
	return a.Save()
}
