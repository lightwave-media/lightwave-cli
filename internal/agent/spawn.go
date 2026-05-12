package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// SpawnOptions is the input to Spawn(). All fields are required.
type SpawnOptions struct {
	ID          string   // pre-generated agent UUID (NewID())
	TaskID      string   // for state + branch
	Persona     string   // persona name (passed to LoadPersonaPrompt under the hood)
	Repo        string   // absolute path to the repo the worktree will live in
	Shell       string   // binary to invoke (claude, pi, …)
	ShellArgs   []string // additional argv between the binary and the prompt
	Prompt      string   // prompt body to pass on stdin (system prompt + context bundle, joined)
	PromptStdin bool     // when true, Prompt is piped via stdin instead of argv
}

// Spawn creates a worktree, persists a Prompt-as-context file, and starts
// the agent shell as a background process. Returns the persisted Agent
// record on success.
//
// Background semantics: cmd.Start() then return — the spawned process is
// reparented to launchd / init when `lw` exits. Status lookup later uses
// PID liveness (StatusOf in status.go). Stdout + stderr are appended to
// <StateDir>/<id>.log; nothing is streamed back to the caller.
//
// On any failure before the child reaches Start(), the worktree is rolled
// back so v_core never sees an inconsistent state.
func Spawn(opts SpawnOptions) (*Agent, error) {
	if opts.ID == "" {
		return nil, fmt.Errorf("agent id is required (use agent.NewID())")
	}
	if opts.Shell == "" {
		opts.Shell = "claude"
	}

	worktree, branch, err := CreateWorktree(WorktreeOptions{
		Repo:    opts.Repo,
		TaskID:  opts.TaskID,
		Persona: opts.Persona,
		AgentID: opts.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	stateDir, err := StateDir()
	if err != nil {
		_ = RemoveWorktree(opts.Repo, worktree, branch)
		return nil, err
	}
	contextPath := filepath.Join(stateDir, opts.ID+"-context.md")
	if err := os.WriteFile(contextPath, []byte(opts.Prompt), 0o600); err != nil {
		_ = RemoveWorktree(opts.Repo, worktree, branch)
		return nil, fmt.Errorf("write context file: %w", err)
	}

	logPath := filepath.Join(stateDir, opts.ID+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		_ = RemoveWorktree(opts.Repo, worktree, branch)
		return nil, fmt.Errorf("open log file: %w", err)
	}

	argv := append([]string{}, opts.ShellArgs...)
	if !opts.PromptStdin {
		argv = append(argv, opts.Prompt)
	}

	cmd := exec.Command(opts.Shell, argv...)
	cmd.Dir = worktree
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Detach from `lw`'s process group so the child survives `lw` exiting
	// (and isn't killed when `lw` receives Ctrl-C).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if opts.PromptStdin {
		cmd.Stdin = strings.NewReader(opts.Prompt)
	}

	a := &Agent{
		ID:          opts.ID,
		TaskID:      opts.TaskID,
		Persona:     opts.Persona,
		Repo:        opts.Repo,
		Worktree:    worktree,
		Branch:      branch,
		Shell:       opts.Shell,
		ShellArgs:   opts.ShellArgs,
		ContextPath: contextPath,
		LogPath:     logPath,
		StartedAt:   time.Now().UTC(),
	}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		_ = RemoveWorktree(opts.Repo, worktree, branch)
		a.Status = StatusError
		a.Error = err.Error()
		_ = a.Save() // record failure even on rollback so v_core can see it
		return a, fmt.Errorf("start %s: %w", opts.Shell, err)
	}

	// Close our handle once Start() has dup'd it for the child.
	_ = logFile.Close()

	a.PID = cmd.Process.Pid
	a.Status = StatusRunning
	if err := a.Save(); err != nil {
		// Don't kill the child — it's already running. Surface the save
		// failure; v_core can poll with `lw agent list` to recover.
		return a, fmt.Errorf("save agent state: %w", err)
	}

	// Reap the child in a goroutine so it doesn't become a zombie if
	// `lw` lives long enough to see it exit (common in tests; rare in
	// production where lw exits immediately and the child is reparented
	// to launchd). The goroutine flips Status to StatusExited and writes
	// the exit code when Wait returns. When lw exits before the child,
	// the goroutine dies with it and pidAlive becomes the source of
	// truth on the next `lw agent status` call.
	go func(cmd *exec.Cmd, idCopy string) {
		_ = cmd.Wait()
		latest, err := Load(idCopy)
		if err != nil {
			return
		}
		if latest.Status != StatusRunning {
			return
		}
		latest.Status = StatusExited
		latest.ExitedAt = time.Now().UTC()
		if cmd.ProcessState != nil {
			code := cmd.ProcessState.ExitCode()
			latest.ExitCode = &code
		}
		_ = latest.Save()
	}(cmd, a.ID)

	return a, nil
}
