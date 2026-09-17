package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
)

// Same package the GoReleaser build and `mise run lw:sync` inject ldflags
// into (internal/version). One path, kept as a constant so a rename shows up
// as a compile error here instead of a silently unstamped binary.
const versionPkg = "github.com/lightwave-media/lightwave-cli/internal/version"

var selfCmd = &cobra.Command{
	Use:   "self",
	Short: "Manage the lw binary (dev fast path)",
	Long: `Rebuild lw from origin/main, via an isolated worktree.

Use after merging CLI features or pulling main — the homebrew-tap release
train that this line used to reference no longer exists (deleted 2026-09-16);
this is the only distribution path on a dev machine now.

Builds from origin/main, never from whatever the canonical ~/dev/lightwave-cli
checkout happens to have checked out. That checkout's branch changes under
concurrent sessions constantly (§16/§17 of the operator's engineering
doctrine: never read a shared checkout's working tree as a committed fact) —
building it directly would silently install whichever feature branch, however
incomplete, another session had open at that moment. This is also what makes
the rebuild safe to run unattended from a maintenance cron, not only by hand.

Examples:
  lw self sync
  lw self sync --home`,
}

var selfSyncHome bool

var selfSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Rebuild lw from origin/main into ~/.local/bin",
	RunE:  selfSyncRun,
}

func init() {
	selfSyncCmd.Flags().BoolVar(&selfSyncHome, "home", false, "Also run lw home sync (policy stamp→print) after rebuild")
	selfCmd.AddCommand(selfSyncCmd)
}

func selfSyncRun(_ *cobra.Command, _ []string) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("self sync: config not loaded")
	}

	cliRoot := filepath.Join(cfg.Paths.LightwaveRoot, "lightwave-cli")
	if _, err := os.Stat(filepath.Join(cliRoot, "go.mod")); err != nil {
		return fmt.Errorf("self sync: lightwave-cli not found at %s", cliRoot)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	buildDir, sha, describe, err := checkoutOriginMainWorktree(cliRoot)
	if err != nil {
		return fmt.Errorf("self sync: %w", err)
	}
	defer removeWorktreeQuiet(cliRoot, buildDir)

	out := filepath.Join(home, ".local/bin", "lw")
	if err := os.MkdirAll(filepath.Dir(out), codegenDirPerm); err != nil {
		return err
	}

	// Same ldflags path `mise run lw:sync` injects (mise.toml, this repo's
	// workspace tasks), and for the same reason: without them the binary
	// reports "dev / none / unknown" and nothing — including a freshness
	// check reading `lw version` — can tell which commit built it. This was
	// the one path that never carried them; `lw self sync` has produced an
	// unstamped binary since the command existed.
	ldflags := "-s -w -X " + versionPkg + ".Version=" + describe +
		" -X " + versionPkg + ".Commit=" + sha +
		" -X " + versionPkg + ".Date=" + time.Now().UTC().Format("2006-01-02T15:04:05Z")

	build := exec.CommandContext(context.Background(), "go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, "./cmd/lw")
	build.Dir = buildDir
	build.Env = filterOutGitEnv(os.Environ())
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr

	if err := build.Run(); err != nil {
		return fmt.Errorf("self sync: go build: %w", err)
	}

	fmt.Printf("self sync: installed %s (origin/main @ %s)\n", out, sha)

	if selfSyncHome {
		sync := exec.CommandContext(context.Background(), out, "home", "sync")
		sync.Stdout = os.Stdout
		sync.Stderr = os.Stderr

		if err := sync.Run(); err != nil {
			return fmt.Errorf("self sync: home sync: %w", err)
		}
	}

	return nil
}

// checkoutOriginMainWorktree fetches origin/main and materialises it into an
// isolated, detached worktree — separate from cliRoot's own working tree,
// whose branch and cleanliness are not this command's to assume. Returns the
// worktree path, the short SHA it pinned, and a `git describe` string (the
// same VERSION shape `mise run lw:sync` and GoReleaser both stamp) for the
// caller to build from and report.
func checkoutOriginMainWorktree(cliRoot string) (path, sha, describe string, err error) {
	fail := func(err error) (string, string, string, error) { return "", "", "", err }

	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(context.Background(), "git", args...)
		cmd.Dir = cliRoot
		// self sync can itself run from inside a hook, which may have
		// GIT_DIR/GIT_WORK_TREE exported — dropping them keeps `-C`/cwd the
		// only thing that decides which repo git.actually targets.
		cmd.Env = filterOutGitEnv(os.Environ())

		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), runErr, out)
		}

		return strings.TrimSpace(string(out)), nil
	}

	if _, err = run("fetch", "origin", "main", "--quiet"); err != nil {
		return fail(err)
	}

	if sha, err = run("rev-parse", "--short", "origin/main"); err != nil {
		return fail(err)
	}

	// mkdtemp semantics, not PID alone: two calls in the SAME process (this
	// happened in the test suite the moment two cases ran in parallel) raced
	// on an identical path when it was keyed only by os.Getpid(). A worktree
	// add accepts an existing EMPTY directory as its target, so the directory
	// MkdirTemp creates is used as-is rather than removed and recreated.
	if path, err = os.MkdirTemp("", "lw-self-sync-*"); err != nil {
		return fail(fmt.Errorf("create isolated build dir: %w", err))
	}

	if _, err = run("worktree", "add", "--detach", "--quiet", path, "origin/main"); err != nil {
		return fail(err)
	}

	// Explicit origin/main, not bare HEAD: `git describe` defaults to
	// whatever cliRoot itself currently has checked out, which is exactly the
	// shared-working-tree assumption this whole helper exists to avoid — a
	// real bug caught only by actually running this end to end, where the
	// reported Version and Commit visibly disagreed. --always falls back to
	// the bare SHA when no tag is reachable, so this never fails a sync over
	// a missing tag the way a plain `git describe` would.
	if describe, err = run("describe", "--tags", "--always", "origin/main"); err != nil {
		return fail(err)
	}

	// `go` on this machine is a mise shim, and mise refuses to load a
	// mise.toml (which pins the Go toolchain version) from a directory it has
	// never seen — this is a brand-new temp path on every sync. Without this,
	// the build below fails with "config files ... are not trusted" on every
	// single run, not just the first. Trusting a config is not a security
	// downgrade: it is the same one-time grant a human runs by hand the first
	// time they open a fresh worktree, done here non-interactively because
	// nothing is present to answer a prompt.
	trust := exec.CommandContext(context.Background(), "mise", "trust", "--quiet", path)

	trust.Env = filterOutGitEnv(os.Environ())
	if out, trustErr := trust.CombinedOutput(); trustErr != nil {
		return fail(fmt.Errorf("mise trust %s: %w\n%s", path, trustErr, out))
	}

	return path, sha, describe, nil
}

// removeWorktreeQuiet tears down a worktree this command created and never
// handed to anything else to edit — always via `git worktree remove`, never a
// raw directory delete, or git's own administrative metadata under
// cliRoot/.git/worktrees/ is left dangling and can wedge a later `worktree
// add` at the same path. `--force` is safe here specifically because it is
// OUR OWN just-created, untouched, detached worktree — the housekeeping this
// command owns end to end, not another session's checkout (contrast the
// standing rule against `-D`/force on a branch someone else may hold work in).
func removeWorktreeQuiet(cliRoot, path string) {
	env := filterOutGitEnv(os.Environ())

	cmd := exec.CommandContext(context.Background(), "git", "worktree", "remove", "--force", path)
	cmd.Dir = cliRoot
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		// Best-effort: prune the administrative entry so a failed remove
		// (e.g. the dir already gone) doesn't wedge the next sync.
		prune := exec.CommandContext(context.Background(), "git", "worktree", "prune")
		prune.Dir = cliRoot
		prune.Env = env
		_ = prune.Run()
		_ = os.RemoveAll(path)
	}
}

// filterOutGitEnv drops every GIT_-prefixed variable from an environment
// slice, so a git subprocess resolves the repo from its own -C/cwd instead of
// an inherited GIT_DIR — which a call site running inside a hook may well
// have exported, pointed at a different repo entirely. Overriding a key with
// an empty value is not equivalent: git treats an empty GIT_WORK_TREE as
// still present and refuses to run at all without a matching GIT_DIR. The
// keys have to be absent, not blank.
func filterOutGitEnv(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}

		clean = append(clean, kv)
	}

	return clean
}
