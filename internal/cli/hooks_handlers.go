package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fatih/color"
)

// Schema-driven hooks handlers. Manages pre-commit / pre-push gates across
// the LightWave repo set without a hardcoded list: doctor + sync discover
// repos by walking the search roots below for any directory that is BOTH a
// git repo (has a .git dir) AND has a .pre-commit-config.yaml at its top.
// Add a repo by giving it a config; nothing else needs to change.

func init() {
	RegisterHandler("hooks.install", hooksInstallHandler)
	RegisterHandler("hooks.doctor", hooksDoctorHandler)
	RegisterHandler("hooks.sync", hooksSyncHandler)
}

// hooksSearchRoots are the directories scanned for LightWave repos. Each
// root is checked itself + its immediate children (depth 1). Roots that
// don't exist on this machine are silently skipped — single-developer
// laptops won't have all three.
func hooksSearchRoots() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, "dev", "lightwave-media"),
		filepath.Join(home, "dev", "lightwave-sys"),
		filepath.Join(home, ".brain"),
	}
}

// repoStatus is the per-repo result reported by `lw hooks doctor`.
type repoStatus struct {
	Path        string `json:"path"`
	PreCommit   bool   `json:"pre_commit_installed"`
	PrePush     bool   `json:"pre_push_installed"`
	ConfigFound bool   `json:"config_found"`
}

func (s repoStatus) ok() bool { return s.ConfigFound && s.PreCommit && s.PrePush }

func hooksInstallHandler(_ context.Context, _ []string, flags map[string]any) error {
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
	return installHooks(repo)
}

func hooksDoctorHandler(_ context.Context, _ []string, flags map[string]any) error {
	repos := discoverRepos()
	statuses := make([]repoStatus, 0, len(repos))
	for _, r := range repos {
		statuses = append(statuses, repoStatusFor(r))
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Path < statuses[j].Path })

	if asJSON(flags) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(statuses)
	}
	if len(statuses) == 0 {
		fmt.Println(color.YellowString("no LightWave repos with .pre-commit-config.yaml found"))
		return nil
	}
	bad := 0
	for _, s := range statuses {
		mark := color.GreenString("✓")
		if !s.ok() {
			mark = color.RedString("✗")
			bad++
		}
		fmt.Printf("  %s %s  (pre-commit=%v pre-push=%v)\n",
			mark, s.Path, s.PreCommit, s.PrePush)
	}
	fmt.Printf("\n%d repo(s) checked, %d need attention\n", len(statuses), bad)
	if bad > 0 {
		return fmt.Errorf("%d repo(s) missing hooks — run `lw hooks sync`", bad)
	}
	return nil
}

func hooksSyncHandler(_ context.Context, _ []string, flags map[string]any) error {
	repos := discoverRepos()
	if len(repos) == 0 {
		fmt.Println(color.YellowString("no LightWave repos with .pre-commit-config.yaml found"))
		return nil
	}
	dry := flagBool(flags, "dry-run")
	for _, r := range repos {
		if dry {
			fmt.Printf("would install hooks at %s\n", r)
			continue
		}
		fmt.Printf("→ %s\n", r)
		if err := installHooks(r); err != nil {
			fmt.Printf("  %s %v\n", color.RedString("✗"), err)
			continue
		}
		fmt.Printf("  %s installed\n", color.GreenString("✓"))
	}
	return nil
}

// discoverRepos walks each search root + its immediate children and returns
// every directory that is a git repo AND has a .pre-commit-config.yaml.
// Depth-1 walk keeps the cost ~O(top-level dirs) on a typical laptop.
func discoverRepos() []string {
	seen := map[string]bool{}
	var out []string

	check := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		if isGitRepo(p) && hasPreCommitConfig(p) {
			out = append(out, p)
		}
	}

	for _, root := range hooksSearchRoots() {
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

func repoStatusFor(repo string) repoStatus {
	return repoStatus{
		Path:        repo,
		ConfigFound: hasPreCommitConfig(repo),
		PreCommit:   hookInstalled(repo, "pre-commit"),
		PrePush:     hookInstalled(repo, "pre-push"),
	}
}

// hookInstalled treats a hook as installed iff the file exists, is regular
// (not a directory or symlink-to-nowhere), and contains the pre-commit
// framework marker. Bare files left behind by `git init` don't count.
func hookInstalled(repo, name string) bool {
	path := filepath.Join(repo, ".git", "hooks", name)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return containsPreCommitMarker(body)
}

// containsPreCommitMarker looks for a sentinel that pre-commit writes into
// the generated hook script. Either marker is sufficient — matching both
// would falsely reject older pre-commit versions.
func containsPreCommitMarker(body []byte) bool {
	s := string(body)
	return strings.Contains(s, "pre-commit") || strings.Contains(s, "PRE_COMMIT")
}

// installHooks runs `pre-commit install` and `pre-commit install -t pre-push`
// in repo. Streams output so the user sees pre-commit's own progress.
func installHooks(repo string) error {
	for _, args := range [][]string{
		{"install"},
		{"install", "-t", "pre-push"},
	} {
		c := exec.Command("pre-commit", args...)
		c.Dir = repo
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("pre-commit %v: %w", args, err)
		}
	}
	return nil
}
