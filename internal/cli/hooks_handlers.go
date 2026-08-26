package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
)

// Schema-driven hooks handlers. Manages pre-commit / pre-push gates across
// the LightWave repo set without a hardcoded list: doctor + sync discover
// repos by walking the search roots below for commissioned git repos. A
// missing .pre-commit-config.yaml is evidence, not a reason to hide the repo:
// tracked raw hooks and completely missing hook setup must both be visible.

func init() {
	RegisterHandler("hooks.install", hooksInstallHandler)
	RegisterHandler("hooks.doctor", hooksDoctorHandler)
	RegisterHandler("hooks.sync", hooksSyncHandler)
}

// hooksSearchRoots are the directories scanned for LightWave repos. Each
// root is checked itself + its immediate children (depth 1). Roots that
// don't exist on this machine are silently skipped.
//
// The first root is the workspace root (paths.lightwave_root, default ~/dev,
// overridable via LW_LIGHTWAVE_ROOT) — the flat layout every other `lw`
// command resolves against. It used to be ~/dev/lightwave-media, the umbrella
// workspace that was dismantled when the repos went flat. That directory no
// longer exists, so doctor scanned nothing that mattered and reported a clean
// fleet while every repo under ~/dev was invisible to it (lightwave-cli#307).
func hooksSearchRoots(workspaceRoot string) []string {
	roots := []string{workspaceRoot}

	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".brain"))
	}

	return roots
}

// hooksWorkspaceRoot resolves paths.lightwave_root (default ~/dev, overridable
// via LW_LIGHTWAVE_ROOT) — the same root every other `lw` command uses.
func hooksWorkspaceRoot() string {
	if cfg := config.Get(); cfg != nil && cfg.Paths.LightwaveRoot != "" {
		return cfg.Paths.LightwaveRoot
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, "dev")
}

// repoStatus is the per-repo result reported by `lw hooks doctor`.
type repoStatus struct {
	Path        string `json:"path"`
	HooksPath   string `json:"hooks_path"`
	PreCommit   bool   `json:"pre_commit_installed"`
	PrePush     bool   `json:"pre_push_installed"`
	ConfigFound bool   `json:"config_found"`
}

func (s repoStatus) ok() bool { return s.PreCommit && s.PrePush }

func hooksInstallHandler(ctx context.Context, _ []string, flags map[string]any) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	repo, err := nearestRepoRoot(cwd)
	if err != nil {
		return err
	}

	if !hasPreCommitConfig(repo) {
		return fmt.Errorf("no .pre-commit-config.yaml at %s", repo)
	}

	if flagBool(flags, "dry-run") {
		fmt.Printf("would install pre-commit + pre-push hooks at %s\n", repo)
		return nil
	}

	return installHooks(ctx, repo)
}

func hooksDoctorHandler(ctx context.Context, _ []string, flags map[string]any) error {
	root := hooksWorkspaceRoot()
	repos := discoverRepos(root)

	statuses := make([]repoStatus, 0, len(repos))
	for _, r := range repos {
		statuses = append(statuses, repoStatusFor(ctx, r))
	}

	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Path < statuses[j].Path })

	bad := 0

	for _, status := range statuses {
		if !status.ok() {
			bad++
		}
	}

	if asJSON(flags) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(statuses); err != nil {
			return err
		}

		return hooksDoctorResultError(len(statuses), bad)
	}

	if len(statuses) == 0 {
		// Name the roots that were scanned. A bare "found nothing" reads as a
		// clean fleet and is unfalsifiable — the operator cannot tell a healthy
		// estate from a doctor pointed at the wrong directory, which is exactly
		// how #307 hid every repo under ~/dev behind a green result.
		fmt.Println(color.RedString("no commissioned LightWave git repos found"))
		fmt.Println("scanned (each root + its immediate children):")

		for _, scanned := range hooksSearchRoots(root) {
			marker := color.RedString("missing")
			if info, err := os.Stat(scanned); err == nil && info.IsDir() {
				marker = "present"
			}

			fmt.Printf("  - %s (%s)\n", scanned, marker)
		}

		return hooksDoctorResultError(0, 0)
	}

	for _, s := range statuses {
		mark := color.GreenString("✓")
		if !s.ok() {
			mark = color.RedString("✗")
		}

		fmt.Printf("  %s %s  (hooksPath=%s pre-commit=%v pre-push=%v config=%v)\n",
			mark, s.Path, s.HooksPath, s.PreCommit, s.PrePush, s.ConfigFound)
	}

	fmt.Printf("\n%d repo(s) checked, %d need attention\n", len(statuses), bad)

	return hooksDoctorResultError(len(statuses), bad)
}

func hooksDoctorResultError(checked, bad int) error {
	switch {
	case checked == 0:
		return errors.New(
			"hook census found no commissioned repos; verify paths.lightwave_root — " +
				"do not treat an empty audit as healthy",
		)
	case bad > 0:
		return fmt.Errorf(
			"%d repo(s) missing hooks; install the tracked hooks or run `lw hooks sync`; "+
				"do not bypass or disable the gate",
			bad,
		)
	default:
		return nil
	}
}

func hooksSyncHandler(ctx context.Context, _ []string, flags map[string]any) error {
	repos := discoverRepos(hooksWorkspaceRoot())
	if len(repos) == 0 {
		fmt.Println(color.YellowString("no LightWave repos with .pre-commit-config.yaml found"))
		return hooksDoctorResultError(0, 0)
	}

	dry := flagBool(flags, "dry-run")
	failed := 0

	for _, r := range repos {
		status := repoStatusFor(ctx, r)
		if status.ok() {
			fmt.Printf("  %s %s hooks already healthy\n", color.GreenString("✓"), r)

			continue
		}

		if !status.ConfigFound {
			fmt.Printf("  %s %s has no .pre-commit-config.yaml; install its tracked raw hooks\n",
				color.RedString("✗"), r)

			failed++

			continue
		}

		if dry {
			fmt.Printf("would install hooks at %s\n", r)
			continue
		}

		fmt.Printf("→ %s\n", r)

		if err := installHooks(ctx, r); err != nil {
			fmt.Printf("  %s %v\n", color.RedString("✗"), err)

			failed++

			continue
		}

		fmt.Printf("  %s installed\n", color.GreenString("✓"))
	}

	if failed > 0 {
		return fmt.Errorf(
			"%d repo(s) could not be synchronized; install their tracked hooks and rerun; "+
				"do not bypass the doctor",
			failed,
		)
	}

	return nil
}

// discoverRepos walks each search root + its immediate children and returns
// every commissioned Lightwave git repo. mise.toml is the fleet marker; the
// pre-commit config remains a compatibility marker for older commissioned repos.
// Depth-1 walk keeps the cost ~O(top-level dirs) on a typical laptop.
func discoverRepos(workspaceRoot string) []string {
	seen := map[string]bool{}

	var out []string

	check := func(p string) {
		if seen[p] {
			return
		}

		seen[p] = true
		if isGitRepo(p) && isCommissionedRepo(p) {
			out = append(out, p)
		}
	}

	for _, root := range hooksSearchRoots(workspaceRoot) {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}

		check(root)

		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}

			check(filepath.Join(root, e.Name()))
		}
	}

	sort.Strings(out)

	return out
}

func isGitRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && info.IsDir()
}

func hasPreCommitConfig(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".pre-commit-config.yaml"))
	return err == nil
}

func isCommissionedRepo(dir string) bool {
	if hasPreCommitConfig(dir) {
		return true
	}

	_, err := os.Stat(filepath.Join(dir, "mise.toml"))

	return err == nil
}

// nearestRepoRoot walks up from start until it finds a .git dir. Mirrors
// `git rev-parse --show-toplevel` without the shell-out.
func nearestRepoRoot(start string) (string, error) {
	dir := start
	for {
		if isGitRepo(dir) {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside a git repo: %s", start)
		}

		dir = parent
	}
}

func repoStatusFor(ctx context.Context, repo string) repoStatus {
	hooksPath, _ := gitOutput(ctx, repo, "config", "core.hooksPath")
	installed := listInstalledHooks(repo, hooksPath)

	return repoStatus{
		Path:        repo,
		HooksPath:   resolveHooksDir(repo, hooksPath),
		ConfigFound: hasPreCommitConfig(repo),
		PreCommit:   containsString(installed, "pre-commit"),
		PrePush:     containsString(installed, "pre-push"),
	}
}

// installHooks runs `pre-commit install` and `pre-commit install -t pre-push`
// in repo. Streams output so the user sees pre-commit's own progress.
func installHooks(ctx context.Context, repo string) error {
	for _, args := range [][]string{
		{"install"},
		{"install", "-t", "pre-push"},
	} {
		c := exec.CommandContext(ctx, "pre-commit", args...)
		c.Dir = repo
		c.Stdout = os.Stdout

		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("pre-commit %v: %w", args, err)
		}
	}

	return nil
}
