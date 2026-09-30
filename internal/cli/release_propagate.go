//nolint:govet,wsl_v5 // propagate report structs prioritize readable JSON field order
package cli

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/release"
)

const propagationFailed = "failed"

func init() {
	RegisterHandler("release.propagate", releasePropagateHandler)
}

type propagateWorktreeResult struct {
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	SessionID string `json:"session_id,omitempty"`
	Rebased   bool   `json:"rebased"`
	CIOK      bool   `json:"ci_ok"`
	Blocked   bool   `json:"blocked"`
	Detail    string `json:"detail"`
}

type propagateReport struct {
	Status              string                    `json:"status"`
	Repo                string                    `json:"repo"`
	MainSHA             string                    `json:"main_sha"`
	ScanTimeISO         string                    `json:"scan_time_iso"`
	Worktrees           []propagateWorktreeResult `json:"worktrees"`
	InterruptedSessions []string                  `json:"interrupted_sessions,omitempty"`
}

func releasePropagateHandler(ctx context.Context, _ []string, flags map[string]any) error {
	repoSlug := flagString(flags, "repo")
	mainSHA := flagString(flags, "main-sha")
	reportPath := flagString(flags, "report-path")
	apply := flagBool(flags, "yes")
	dryRun := flagBool(flags, "dry-run")

	if repoSlug == "" {
		return errors.New("usage: lw release propagate --repo <slug> [--main-sha SHA] [--yes|--dry-run]")
	}
	if mainSHA != "" || reportPath != "" {
		decoded, err := hex.DecodeString(mainSHA)
		if err != nil || len(decoded) != 20 {
			return errors.New("--main-sha must be a full commit SHA when supplied or when --report-path is requested")
		}
	}

	enabled, err := release.IsEnabled("autonomous_release_session_propagate")
	if err != nil {
		return err
	}

	if !enabled && apply {
		fmt.Printf("%s autonomous_release_session_propagate is off\n", color.YellowString("●"))
		return nil
	}

	profile := loadWorkspaceProfile()

	report, err := buildGitAuditReport(ctx, &profile)
	if err != nil {
		return err
	}

	target := shortRepo(resolveRepo(repoSlug))
	results := make([]propagateWorktreeResult, 0, len(report.Repos))

	var interrupted []string

	for i := range report.Repos {
		node := report.Repos[i]
		if !node.IsWorktree {
			continue
		}

		if node.Branch == "main" || node.Branch == "" {
			continue
		}

		remote, err := gitIn(ctx, node.Path, "remote", "get-url", "origin")
		if err != nil || !propagationRepoMatches(remote, resolveRepo(repoSlug)) {
			continue
		}

		res := propagateWorktree(ctx, node.Path, node.Branch, mainSHA, apply && !dryRun)
		results = append(results, res)
		if res.Blocked && res.SessionID != "" {
			interrupted = append(interrupted, res.SessionID)
			if apply && !dryRun {
				emitPropagateInterrupt(res.SessionID, repoSlug, mainSHA, res.Detail)
			}
		}

		verb := "would propagate"
		if apply && !dryRun {
			verb = "propagated"
		}
		fmt.Printf("  %s %s (%s) — %s\n", markPropagate(res), node.Path, node.Branch, res.Detail)
		_ = verb
	}

	out := propagateReport{
		Status:              "completed",
		Repo:                target,
		MainSHA:             mainSHA,
		ScanTimeISO:         time.Now().UTC().Format(time.RFC3339),
		Worktrees:           results,
		InterruptedSessions: interrupted,
	}
	for _, result := range results {
		if result.Blocked {
			out.Status = propagationFailed
		}
	}

	if apply && !dryRun {
		if err := writePropagateReport(&out, reportPath); err != nil {
			return err
		}
	}
	if apply && !dryRun && out.Status == propagationFailed {
		return errors.New("release propagation blocked; see the invocation report")
	}

	if len(interrupted) > 0 {
		fmt.Printf("%s %d session(s) blocked until CI green after rebase\n",
			color.RedString("✗"), len(interrupted))
	}

	return nil
}

func propagationRepoMatches(remote, repo string) bool {
	for _, prefix := range []string{"https://github.com/", "git@github.com:", "ssh://git@github.com/"} {
		if strings.HasPrefix(remote, prefix) {
			return strings.TrimSuffix(strings.TrimPrefix(remote, prefix), ".git") == repo
		}
	}
	return false
}

// worktreeIsDirty reports whether the checkout has staged, unstaged, or
// untracked changes. Untracked counts: uncommitted work is uncommitted work
// regardless of whether git is tracking it yet, and it is the untracked case
// (a whole new package sitting in a worktree) that is easiest to destroy.
func worktreeIsDirty(ctx context.Context, dir string) (bool, error) {
	out, err := gitIn(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(out) != "", nil
}

func propagateWorktree(ctx context.Context, dir, branch, mainSHA string, apply bool) propagateWorktreeResult {
	res := propagateWorktreeResult{
		Path:      dir,
		Branch:    branch,
		SessionID: detectSessionID(dir),
		Detail:    "already current",
	}

	// A dirty worktree is skipped, never rebased. git would refuse anyway, but
	// the refusal surfaced here as "rebase conflict — resolve manually", sending
	// whoever read it hunting for conflicts that do not exist. The lightwave-git
	// doctrine is also explicit: never switch or rebase a dirty tree. Checked
	// before the dry-run exit so a preview reports the skip it would take —
	// there is a worktree in this repo carrying ~3.2k uncommitted lines today.
	if dirty, err := worktreeIsDirty(ctx, dir); err != nil {
		res.Blocked = true
		res.Detail = "could not read worktree status: " + err.Error()

		return res
	} else if dirty {
		res.Blocked = true
		res.Detail = "skipped — uncommitted changes; commit or stash, then re-run"

		return res
	}

	if !apply {
		res.Detail = "dry-run — would fetch, rebase, ci"
		return res
	}

	if err := gitInErr(ctx, dir, "fetch", "origin", "--prune"); err != nil {
		res.Blocked = true
		res.Detail = "fetch failed: " + err.Error()
		return res
	}

	candidate := mainSHA
	if candidate == "" {
		candidate = "origin/main"
	}
	target, err := gitIn(ctx, dir, "rev-parse", "--verify", "--end-of-options", candidate+"^{commit}")
	if err != nil {
		res.Blocked = true
		res.Detail = "candidate commit unavailable: " + err.Error()
		return res
	}
	if err := gitInErr(ctx, dir, "merge-base", "--is-ancestor", target, "origin/main"); err != nil {
		res.Blocked = true
		res.Detail = "candidate is not on origin/main"
		return res
	}

	base, err := gitIn(ctx, dir, "merge-base", "HEAD", "origin/main")
	if err != nil {
		res.Blocked = true
		res.Detail = "merge-base failed: " + err.Error()
		return res
	}

	if base != target {
		if gitInErr(ctx, dir, "merge-base", "--is-ancestor", target, base) == nil {
			res.Blocked = true
			res.Detail = "worktree already includes main newer than the requested candidate"
			return res
		}
		if err := gitInErr(ctx, dir, "rebase", target); err != nil {
			res.Blocked = true
			res.Detail = "rebase conflict — resolve manually: " + err.Error()
			_, _ = gitIn(ctx, dir, "rebase", "--abort")
			return res
		}
		res.Rebased = true
	}
	base, err = gitIn(ctx, dir, "merge-base", "HEAD", "origin/main")
	if err != nil || base != target {
		res.Blocked = true
		res.Detail = "worktree base does not match the requested candidate"
		return res
	}
	testedHead, err := gitIn(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		res.Blocked = true
		res.Detail = "could not resolve the CI source revision"
		return res
	}

	ciCmd := "mise run ci"
	if _, err := os.Stat(filepath.Join(dir, "mise.toml")); err != nil {
		ciCmd = "./dev/ci.sh all"
	}

	cmd := exec.CommandContext(ctx, "bash", "-lc", ciCmd)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		res.CIOK = false
		res.Blocked = true
		res.Detail = fmt.Sprintf("CI failed after propagate: %v", err)
		if len(out) > 0 {
			res.Detail += " — see " + dir
		}
		return res
	}

	afterHead, headReadErr := gitIn(ctx, dir, "rev-parse", "HEAD")
	dirty, dirtyErr := worktreeIsDirty(ctx, dir)
	if headReadErr != nil || dirtyErr != nil || dirty || afterHead != testedHead {
		res.Blocked = true
		res.Detail = "source changed during CI; propagation proof invalid"
		return res
	}

	res.CIOK = true
	res.Detail = "rebased + CI green"
	return res
}

func gitInErr(ctx context.Context, dir string, args ...string) error {
	_, err := gitIn(ctx, dir, args...)
	return err
}

func gitIn(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(string(b)), err)
	}
	return strings.TrimSpace(string(b)), nil
}

func detectSessionID(worktreePath string) string {
	tasksRoot := filepath.Join(os.Getenv("HOME"), ".lightwave", "tasks")
	entries, err := os.ReadDir(tasksRoot)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		link := filepath.Join(tasksRoot, e.Name(), "worktree")
		target, err := os.Readlink(link)
		if err != nil {
			continue
		}
		if strings.Contains(worktreePath, filepath.Clean(target)) ||
			strings.Contains(filepath.Clean(target), worktreePath) {
			return e.Name()
		}
	}
	return ""
}

func emitPropagateInterrupt(sessionID, repo, mainSHA, detail string) {
	path := filepath.Join(os.Getenv("HOME"), ".lightwave", "observability", "interrupts.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), gitDirPerm); err != nil {
		return
	}

	rec := map[string]string{
		"ts":       time.Now().UTC().Format(time.RFC3339),
		"to_agent": sessionID,
		"kind":     "release_propagate_block",
		"repo":     repo,
		"main_sha": mainSHA,
		"detail":   detail,
	}

	b, err := json.Marshal(rec)
	if err != nil {
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, gitFilePerm)
	if err != nil {
		return
	}

	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func writePropagateReport(rep *propagateReport, requestedPath string) error {
	dir := filepath.Join(os.Getenv("HOME"), ".lightwave", "reports")
	return writePropagateReportInDir(rep, requestedPath, dir)
}

func writePropagateReportInDir(rep *propagateReport, requestedPath, dir string) error {
	if err := os.MkdirAll(dir, gitDirPerm); err != nil {
		return err
	}

	stamp := time.Now().UTC().Format("20060102T150405Z")
	path := filepath.Join(dir, "release-propagate-"+stamp+".json")
	latest := filepath.Join(dir, "release-propagate.latest.json")
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if requestedPath != "" {
		if err := writeRequestedPropagateReport(requestedPath, b); err != nil {
			return fmt.Errorf("write requested propagation report: %w", err)
		}
	}

	if err := os.WriteFile(path, b, gitFilePerm); err != nil {
		return err
	}

	return os.WriteFile(latest, b, gitFilePerm)
}

// A runbook may reserve an empty file before invocation. Never accept a stale
// report or follow a symlink into another artifact.
func writeRequestedPropagateReport(path string, data []byte) error {
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	flags := os.O_WRONLY
	if errors.Is(err, os.ErrNotExist) {
		flags |= os.O_CREATE | os.O_EXCL
	} else if !info.Mode().IsRegular() || info.Size() != 0 {
		return errors.New("report path must be absent or a reserved empty regular file")
	}
	f, err := os.OpenFile(path, flags, gitFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if info != nil && (!os.SameFile(info, opened) || opened.Size() != 0) {
		return errors.New("report path changed after reservation")
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func markPropagate(r propagateWorktreeResult) string {
	if r.Blocked {
		return color.RedString("✗")
	}
	if r.Rebased || r.CIOK {
		return color.GreenString("✓")
	}
	return color.CyanString("●")
}
