package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/tmux"
)

// Manager handles spawning, listing, and killing agents.
type Manager struct {
	tmux *tmux.Tmux
}

// NewManager creates a new agent manager.
func NewManager() *Manager {
	return &Manager{
		tmux: tmux.New(),
	}
}

// SpawnOptions configures a new agent spawn.
type SpawnOptions struct {
	Role   Role
	Repo   string
	TaskID string
	Model  string
	Prompt string
}

// Spawn creates a new agent: generates a name, ensures a bare repo clone,
// creates a worktree, starts a tmux session with claude, and saves state.
func (m *Manager) Spawn(opts SpawnOptions) (*Agent, error) {
	name, err := GenerateName(opts.Role)
	if err != nil {
		return nil, fmt.Errorf("generate name: %w", err)
	}

	// Ensure bare repo at ~/.lw/repos/
	repoPath := filepath.Join(ReposDir(), filepath.Base(opts.Repo)+".git")
	if err := ensureBareRepo(opts.Repo, repoPath); err != nil {
		return nil, fmt.Errorf("ensure bare repo: %w", err)
	}

	// Create worktree
	branch := BranchName(name)
	workDir := filepath.Join(BaseDir(), name, "worktree")
	if err := createWorktree(repoPath, workDir, branch); err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	// Build claude command and start tmux session
	claudeCmd := buildClaudeCommand(opts)
	sessionName := TmuxSessionName(name)
	if err := m.tmux.NewSession(sessionName, workDir, claudeCmd); err != nil {
		return nil, fmt.Errorf("start tmux session: %w", err)
	}

	a := &Agent{
		Name:        name,
		Role:        opts.Role,
		State:       StateWorking,
		Repo:        opts.Repo,
		RepoPath:    repoPath,
		WorkDir:     workDir,
		Branch:      branch,
		TaskID:      opts.TaskID,
		Model:       opts.Model,
		Prompt:      opts.Prompt,
		TmuxSession: sessionName,
		CreatedAt:   time.Now(),
	}
	if err := a.Save(); err != nil {
		_ = m.tmux.KillSession(sessionName)
		return nil, fmt.Errorf("save agent: %w", err)
	}

	return a, nil
}

// List returns all agents from disk.
func (m *Manager) List() ([]*Agent, error) {
	return ListAll()
}

// Kill terminates an agent. If force is true, the worktree and agent
// directory are removed. Otherwise the agent is marked done.
func (m *Manager) Kill(name string, force bool) error {
	a, err := Load(name)
	if err != nil {
		return fmt.Errorf("load agent: %w", err)
	}

	_ = m.tmux.KillSession(a.TmuxSession)

	if force {
		if a.WorkDir != "" {
			removeWorktree(a.RepoPath, a.WorkDir)
		}
		return os.RemoveAll(a.Dir())
	}

	return a.SetState(StateDone)
}

// buildClaudeCommand assembles the claude CLI invocation.
func buildClaudeCommand(opts SpawnOptions) string {
	cmd := "claude --dangerously-skip-permissions"
	if opts.Prompt != "" {
		cmd += fmt.Sprintf(" -p '%s'", opts.Prompt)
	}
	return cmd
}

// ensureBareRepo clones a bare repo if it doesn't exist, or fetches if it does.
func ensureBareRepo(remote, localPath string) error {
	if _, err := os.Stat(localPath); err == nil {
		cmd := exec.Command("git", "-C", localPath, "fetch", "--all")
		return cmd.Run()
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}
	cmd := exec.Command("git", "clone", "--bare", remote, localPath)
	return cmd.Run()
}

// createWorktree adds a git worktree on a new branch.
func createWorktree(repoPath, workDir, branch string) error {
	if err := os.MkdirAll(filepath.Dir(workDir), 0755); err != nil {
		return err
	}
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", "-b", branch, workDir)
	return cmd.Run()
}

// removeWorktree force-removes a git worktree.
func removeWorktree(repoPath, workDir string) {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", workDir)
	_ = cmd.Run()
}
