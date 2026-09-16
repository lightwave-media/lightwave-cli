// Package worktreelock answers one question with a fact instead of a guess:
// is an agent session working in this worktree right now?
//
// Before this, nothing on the machine recorded that. pre-tool-use-checkout-lock
// writes a lock for each CANONICAL checkout and deliberately exempts worktrees
// — its own comment calls them "the sanctioned escape hatch" — so the only
// liveness signal covered the one place agents are told never to work, and the
// place all work actually happens had none.
//
// Anything sweeping worktrees therefore had to INFER abandonment from
// idleness. Inference about a live agent is wrong eventually, and when it is
// wrong it deletes work that exists nowhere else. Measured on this host in
// September: 25 raw `git worktree remove --force` and 4 `rm -rf` under
// .worktrees, against 3 uses of the sanctioned verbs.
//
// The lock file format is the hook's, deliberately, so both halves read and
// write one shape: {session_id, pid, ts, branch}. Liveness is a process check
// first (is pid alive?) and a TTL second, because a pid that exited is a fact
// while an old timestamp is only a hint.
package worktreelock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/config"
)

// TTL matches the checkout-lock hook's self-clearing window. A lock older than
// this is treated as abandoned even if something still holds the pid, because
// a session that has not touched a worktree in half an hour is not editing it.
const TTL = 30 * time.Minute

const (
	// lockDirMode / lockFileMode: the directory is shared with the hook's own
	// locks, so it stays group-readable; the lock itself carries no secret.
	lockDirMode  os.FileMode = 0o755
	lockFileMode os.FileMode = 0o644

	// pathHashLen is enough of the path digest to make a collision between two
	// same-named worktrees implausible while keeping the filename readable.
	pathHashLen = 12
)

// Lock is one session's claim on a worktree. Field names match the JSON the
// checkout-lock hook writes; Path is additive and absent on hook-written locks.
type Lock struct {
	SessionID string `json:"session_id"`
	TS        string `json:"ts"`
	Branch    string `json:"branch,omitempty"`
	Path      string `json:"path,omitempty"`
	PID       int    `json:"pid"`
}

func lockDir() string {
	return filepath.Join(config.PrintRoot(), "observability", "checkout-locks")
}

// LockPath is where a worktree's lock lives.
//
// Keyed by a hash of the absolute path, not by basename: two repos routinely
// carry a worktree with the same branch-derived name, and a collision here
// would let one session's lock vouch for another session's worktree. The
// basename is kept as a readable prefix so the directory can be eyeballed.
func LockPath(worktreePath string) string {
	abs, err := filepath.Abs(worktreePath)
	if err != nil {
		abs = worktreePath
	}

	sum := sha256.Sum256([]byte(strings.TrimRight(abs, "/")))
	name := fmt.Sprintf("wt-%s-%s.lock", filepath.Base(abs), hex.EncodeToString(sum[:])[:pathHashLen])

	return filepath.Join(lockDir(), name)
}

// Claim records that this process is working in worktreePath.
func Claim(worktreePath, sessionID, branch string) error {
	if err := os.MkdirAll(lockDir(), lockDirMode); err != nil {
		return err
	}

	abs, err := filepath.Abs(worktreePath)
	if err != nil {
		abs = worktreePath
	}

	body, err := json.Marshal(Lock{
		SessionID: sessionID,
		PID:       os.Getpid(),
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
		Branch:    branch,
		Path:      abs,
	})
	if err != nil {
		return err
	}

	return os.WriteFile(LockPath(worktreePath), body, lockFileMode)
}

// Release drops this worktree's claim. Absent lock is not an error.
func Release(worktreePath string) error {
	err := os.Remove(LockPath(worktreePath))
	if os.IsNotExist(err) {
		return nil
	}

	return err
}

// Holder returns the live lock on a worktree, or nil when nothing holds it.
//
// Two ways to be dead, and both matter. A pid that no longer exists is
// conclusive — the session crashed or exited without releasing. A lock whose
// timestamp is past TTL is stale even with a live pid, because pids are
// recycled and a long-lived shell is not the same as active work.
func Holder(worktreePath string) *Lock {
	body, err := os.ReadFile(LockPath(worktreePath))
	if err != nil {
		return nil
	}

	var lock Lock
	if err := json.Unmarshal(body, &lock); err != nil {
		return nil // an unreadable lock vouches for nothing
	}

	if ts, err := time.Parse(time.RFC3339Nano, lock.TS); err == nil {
		if time.Since(ts) > TTL {
			return nil
		}
	}

	if lock.PID > 0 && !processAlive(lock.PID) {
		return nil
	}

	return &lock
}

// Age is how long ago the holder last touched the worktree.
func (l *Lock) Age() time.Duration {
	ts, err := time.Parse(time.RFC3339Nano, l.TS)
	if err != nil {
		return 0
	}

	return time.Since(ts)
}

// processAlive reports whether a pid currently exists.
//
// Signal 0 performs the permission and existence checks without delivering
// anything. EPERM means the process EXISTS and belongs to someone else, which
// is still alive — treating it as dead is the error that would make this
// return "abandoned" for a worktree another user is actively using.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}

	return errors.Is(err, syscall.EPERM)
}
