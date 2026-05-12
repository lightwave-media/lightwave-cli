package agent

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// RefreshStatus inspects the on-disk PID and updates the agent record's
// Status if the process has exited since the last poll. The record is
// re-saved when status changes.
//
// PID-only tracking: orphaned children reparented to launchd lose their
// exit code (waitpid only works for direct children of the running
// process). Status flips to StatusExited with ExitCode=nil — the log file
// at LogPath is the source of truth for outcome.
func RefreshStatus(a *Agent) error {
	if a.Status != StatusRunning {
		return nil
	}
	if a.PID <= 0 {
		a.Status = StatusError
		if a.Error == "" {
			a.Error = "agent has no pid; cannot poll status"
		}
		return a.Save()
	}

	alive, err := pidAlive(a.PID)
	if err != nil {
		return err
	}
	if alive {
		return nil
	}

	a.Status = StatusExited
	a.ExitedAt = time.Now().UTC()
	return a.Save()
}

// pidAlive returns true when a process with the given pid exists. Uses
// signal-0 ("does this pid exist") rather than parsing /proc, which is
// not available on darwin.
//
// Treats os.ErrProcessDone the same as ESRCH — it surfaces when the Go
// runtime has already observed the child's exit (e.g. via cmd.Wait() in
// the in-process Spawn goroutine). In real usage `lw` exits immediately
// after Spawn so the child is reparented to launchd, but tests share the
// process and need this branch.
func pidAlive(pid int) (bool, error) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	// "no such process" — common case when the agent has exited.
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if errors.Is(err, os.ErrProcessDone) {
		return false, nil
	}
	// EPERM means the process exists but is owned by another user. Treat
	// as alive so v_core doesn't prematurely mark exit; the real exit
	// signal will come on a subsequent poll once the PID is recycled or
	// the process actually disappears.
	if errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	return false, err
}

// Stop sends SIGTERM (or SIGKILL when force is true) to the agent process
// and removes the worktree. Idempotent: if the agent has already exited,
// only the worktree cleanup runs.
func Stop(a *Agent, force bool) error {
	if a.Status == StatusRunning && a.PID > 0 {
		sig := syscall.SIGTERM
		if force {
			sig = syscall.SIGKILL
		}
		if p, err := os.FindProcess(a.PID); err == nil {
			_ = p.Signal(sig)
		}
		// Give the process a moment to react when not forcing.
		if !force {
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				alive, _ := pidAlive(a.PID)
				if !alive {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}

	a.Status = StatusExited
	if a.ExitedAt.IsZero() {
		a.ExitedAt = time.Now().UTC()
	}
	if err := a.Save(); err != nil {
		return err
	}

	if a.Worktree != "" && a.Repo != "" {
		if err := RemoveWorktree(a.Repo, a.Worktree, a.Branch); err != nil {
			return err
		}
	}
	return nil
}
