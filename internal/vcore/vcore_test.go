package vcore

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pinHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestSaveLoadClearState(t *testing.T) {
	pinHome(t)

	if s, err := LoadState(); err != nil || s != nil {
		t.Fatalf("LoadState on fresh home: state=%+v err=%v, want (nil,nil)", s, err)
	}

	want := &State{
		PID:       12345,
		Binary:    "/usr/local/bin/vcore",
		StartedAt: time.Now().UTC().Truncate(time.Second),
		LogPath:   "/tmp/v_core.log",
	}
	if err := SaveState(want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState()
	if err != nil || got == nil {
		t.Fatalf("LoadState: state=%+v err=%v", got, err)
	}
	if got.PID != want.PID || got.Binary != want.Binary || got.LogPath != want.LogPath {
		t.Errorf("round-trip mismatch: got=%+v want=%+v", got, want)
	}

	if err := ClearState(); err != nil {
		t.Fatalf("ClearState: %v", err)
	}
	if s, err := LoadState(); err != nil || s != nil {
		t.Errorf("after ClearState: state=%+v err=%v, want (nil,nil)", s, err)
	}

	// Idempotent.
	if err := ClearState(); err != nil {
		t.Errorf("ClearState (idempotent): %v", err)
	}
}

func TestResolveBinary_Override(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "vcore")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LW_VCORE_BINARY", binary)
	t.Setenv("PATH", "/nonexistent")

	got, err := ResolveBinary()
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if got != binary {
		t.Errorf("got %q, want %q", got, binary)
	}
}

func TestResolveBinary_OverrideMissing(t *testing.T) {
	t.Setenv("LW_VCORE_BINARY", "/definitely/does/not/exist")
	t.Setenv("PATH", "/nonexistent")

	_, err := ResolveBinary()
	if err == nil || !strings.Contains(err.Error(), "$LW_VCORE_BINARY") {
		t.Errorf("err = %v, expected $LW_VCORE_BINARY mention", err)
	}
}

func TestResolveBinary_NotFound(t *testing.T) {
	t.Setenv("LW_VCORE_BINARY", "")
	t.Setenv("PATH", "/nonexistent")
	t.Setenv("HOME", "/nonexistent")

	_, err := ResolveBinary()
	if err == nil {
		t.Fatal("expected not-found error")
	}
	for _, want := range []string{"vcore binary not found", "$PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, expected %q in message", err, want)
		}
	}
}

func TestStartStopStatus_HappyPath(t *testing.T) {
	pinHome(t)

	// Use /bin/sh as a stand-in daemon so the lifecycle path is real.
	t.Setenv("LW_VCORE_BINARY", "/bin/sh")

	s, err := Start([]string{"-c", "sleep 30"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.PID == 0 {
		t.Fatalf("expected PID > 0, got 0")
	}
	t.Cleanup(func() {
		_ = exec.Command("kill", "-9", itoa(s.PID)).Run()
		_ = ClearState()
	})

	// Status reports running.
	live, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if live == nil || live.PID != s.PID {
		t.Errorf("Status = %+v, want PID %d", live, s.PID)
	}

	// Stop kills + clears state.
	if err := Stop(true); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s, err := LoadState(); err != nil || s != nil {
		t.Errorf("after Stop: state=%+v err=%v, want (nil,nil)", s, err)
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	pinHome(t)
	t.Setenv("LW_VCORE_BINARY", "/bin/sh")

	s1, err := Start([]string{"-c", "sleep 30"})
	if err != nil {
		t.Fatalf("Start 1: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("kill", "-9", itoa(s1.PID)).Run()
		_ = ClearState()
	})

	_, err = Start([]string{"-c", "sleep 30"})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("expected ErrAlreadyRunning, got %v", err)
	}
}

func TestStart_StaleStateOverwritten(t *testing.T) {
	pinHome(t)

	// Write a stale state file pointing at a PID that is almost certainly
	// not alive on this machine.
	stale := &State{
		PID:       99999999,
		Binary:    "/bin/sh",
		StartedAt: time.Now().UTC().Add(-1 * time.Hour),
		LogPath:   "/tmp/old.log",
	}
	if err := SaveState(stale); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LW_VCORE_BINARY", "/bin/sh")
	s, err := Start([]string{"-c", "sleep 30"})
	if err != nil {
		t.Fatalf("Start should overwrite stale state: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("kill", "-9", itoa(s.PID)).Run()
		_ = ClearState()
	})
	if s.PID == stale.PID {
		t.Errorf("Start did not replace stale PID")
	}
}

func TestStop_NoStateIsNoOp(t *testing.T) {
	pinHome(t)
	if err := Stop(false); err != nil {
		t.Errorf("Stop on missing state should be no-op: %v", err)
	}
}

func TestStatus_StaleStateCleared(t *testing.T) {
	pinHome(t)
	stale := &State{
		PID:       99999999,
		Binary:    "/bin/sh",
		StartedAt: time.Now().UTC(),
		LogPath:   "/tmp/x.log",
	}
	if err := SaveState(stale); err != nil {
		t.Fatal(err)
	}
	live, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if live != nil {
		t.Errorf("Status = %+v, want nil (stale)", live)
	}
	if s, _ := LoadState(); s != nil {
		t.Errorf("stale state should be cleared, still %+v", s)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := []byte{}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
