// Package vcore is the daemon-lifecycle wrapper around the vcore
// binary that lives in lightwave-sys. v_core itself is implemented in
// Rust over there; this package just supervises a single instance
// from the lw side (start / stop / status / logs).
//
// State lives at ~/.lightwave/v_core/. Singleton — one daemon at a
// time. Reusing the pidAlive pattern from internal/agent so v_core's
// supervisor isn't a new implementation of process tracking.
package vcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// State is the on-disk record of the running daemon. JSON at
// ~/.lightwave/v_core/state.json. PID==0 ⇒ "not running" sentinel.
type State struct {
	PID       int       `json:"pid"`
	Binary    string    `json:"binary"`
	StartedAt time.Time `json:"started_at"`
	LogPath   string    `json:"log_path"`
}

// Dir returns ~/.lightwave/v_core/, creating it if missing (0o700).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(home, ".lightwave", "v_core")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", d, err)
	}
	return d, nil
}

// StatePath returns the absolute path to the singleton state file.
func StatePath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "state.json"), nil
}

// LogPath returns the absolute path to the daemon's log file.
func LogPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "v_core.log"), nil
}

// LoadState reads the singleton record. Returns (nil, nil) when no
// state file exists — the daemon has never been started.
func LoadState() (*State, error) {
	path, err := StatePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &s, nil
}

// SaveState writes the state record atomically.
func SaveState(s *State) error {
	path, err := StatePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ClearState removes the state file. Idempotent.
func ClearState() error {
	path, err := StatePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ErrAlreadyRunning is returned by Start when a daemon is already alive.
var ErrAlreadyRunning = errors.New("v_core is already running")

// ResolveBinary picks the daemon binary path. Precedence:
//  1. $LW_VCORE_BINARY (explicit override)
//  2. `vcore` on PATH
//  3. ~/dev/lightwave-sys/target/release/vcore  (dev build, fallback)
//
// Returns an error mentioning every probe location when none is found
// so callers can surface a useful remediation message.
func ResolveBinary() (string, error) {
	if p := os.Getenv("LW_VCORE_BINARY"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("$LW_VCORE_BINARY=%s does not exist", p)
	}
	if p, err := exec.LookPath("vcore"); err == nil {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		fallback := filepath.Join(home, "dev", "lightwave-sys", "target", "release", "vcore")
		if _, err := os.Stat(fallback); err == nil {
			return fallback, nil
		}
	}
	return "", fmt.Errorf("vcore binary not found (tried $LW_VCORE_BINARY, $PATH, ~/dev/lightwave-sys/target/release/vcore). " +
		"Build it from lightwave-sys (`cargo build --release -p vcore`) or set $LW_VCORE_BINARY")
}

// Start launches the daemon detached, captures stdout+stderr to the log
// file, and persists the state record. Errors with ErrAlreadyRunning if
// a live PID is already on file.
func Start(args []string) (*State, error) {
	if existing, err := LoadState(); err != nil {
		return nil, err
	} else if existing != nil && existing.PID > 0 {
		alive, _ := pidAlive(existing.PID)
		if alive {
			return existing, ErrAlreadyRunning
		}
		// Stale state — fall through and overwrite.
	}

	binary, err := ResolveBinary()
	if err != nil {
		return nil, err
	}
	logPath, err := LogPath()
	if err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}

	cmd := exec.Command(binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// New session so the daemon survives `lw` exiting.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start %s: %w", binary, err)
	}
	_ = logFile.Close()

	s := &State{
		PID:       cmd.Process.Pid,
		Binary:    binary,
		StartedAt: time.Now().UTC(),
		LogPath:   logPath,
	}
	if err := SaveState(s); err != nil {
		// Don't kill the daemon — surface the save error.
		return s, fmt.Errorf("save state: %w", err)
	}

	// Release Go's hold on the child; the daemon is independent now.
	_ = cmd.Process.Release()
	return s, nil
}

// Status checks the live state of the daemon. Returns (nil, nil) when
// nothing is running, or (state, nil) with a PID-liveness-accurate
// state. Stale entries (PID dead) are auto-cleared.
func Status() (*State, error) {
	s, err := LoadState()
	if err != nil {
		return nil, err
	}
	if s == nil || s.PID <= 0 {
		return nil, nil
	}
	alive, err := pidAlive(s.PID)
	if err != nil {
		return nil, err
	}
	if !alive {
		_ = ClearState()
		return nil, nil
	}
	return s, nil
}

// Stop sends SIGTERM (or SIGKILL when force) and clears the state.
// Idempotent — no-op if no state on file or PID already dead.
func Stop(force bool) error {
	s, err := LoadState()
	if err != nil {
		return err
	}
	if s == nil || s.PID <= 0 {
		return nil
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if p, err := os.FindProcess(s.PID); err == nil {
		_ = p.Signal(sig)
	}
	if !force {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			alive, _ := pidAlive(s.PID)
			if !alive {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return ClearState()
}

// pidAlive returns true when a process exists at pid. Mirrors the
// implementation in internal/agent/status.go — kept here as a local
// helper to avoid an import cycle.
func pidAlive(pid int) (bool, error) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
		return false, nil
	}
	if errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	return false, err
}
