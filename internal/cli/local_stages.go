// Package cli — local_stages.go provides the shared plumbing used by the
// per-stage `lw local *` handlers (local_doctor.go, local_fmt.go,
// local_lint.go, local_testcmd.go, local_build.go, local_act.go,
// local_test_harness.go, local_tauri.go).
//
// Every stage handler does the same three things:
//
//  1. Detect the repo (so behavior dispatches per repo).
//  2. Shell out to the canonical command for that repo, streaming stdio.
//  3. Return an error with a remediation hint when the command fails.
//
// `local_gate.go` reuses the same per-stage helpers via the buildStage*
// constructors so the composite report records identical results to what
// running each stage in isolation would.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/local/gate"
	"github.com/lightwave-media/lightwave-cli/internal/repo"
)

// detectRepo wraps repo.Detect with a friendlier error message. Stage
// handlers refuse to run outside a recognised git repo because they need
// a known root to dispatch from.
func detectRepo() (repo.Info, error) {
	info, err := repo.Detect("")
	if err != nil {
		return repo.Info{}, fmt.Errorf("repo detect: %w (run from inside a git repo)", err)
	}

	return info, nil
}

// runStreaming runs name+args in dir, piping stdio. Returns the underlying
// exec error so callers can wrap with remediation hints.
func runStreaming(ctx context.Context, dir, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		c.Dir = dir
	}

	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	return c.Run()
}

// commandExists returns true when name resolves on PATH.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// sysDevScript returns the absolute path to a lightwave-sys/dev/<script>.sh,
// or "" if the script isn't accessible. Used by handlers that wrap
// dev/ci.sh, dev/test-harness.sh, dev/run-tauri-dev.sh.
func sysDevScript(info repo.Info, script string) string {
	if info.ID == repo.LightwaveSys {
		return filepath.Join(info.Root, "dev", script)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	candidate := filepath.Join(home, "dev", "lightwave-sys", "dev", script)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}

	return ""
}

// skipResult builds a uniform StageResult for stages that are not
// applicable to the current repo (e.g. tauri on lightwave-cli).
func skipResult(reason string) gate.StageResult {
	return gate.StageResult{Status: gate.StatusSkip, Notes: reason}
}

// remediation wraps an error message with a single-line "→ fix" hint
// so the operator never has to guess what to do next.
func remediation(err error, cmd string) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%w\n  → fix: %s", err, cmd)
}

// stageNotImplemented is returned by per-repo dispatchers when an `lw local
// <stage>` was invoked in a repo where the persona has no defined behavior.
// Not an error — handlers exit 0 with a one-line skip notice.
func stageNotImplemented(stage string, info repo.Info) {
	fmt.Printf("%s %s not implemented for repo %s — skipping\n",
		color.YellowString("•"), stage, info.ID)
}
