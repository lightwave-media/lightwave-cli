// Package task is the SOP-mirror manual-fallback surface for v_core's
// resident-orchestrator pipeline.
//
// `Done` performs the four cleanup steps documented in
// docs/software/ddds/DDD-mvp-vertical-slice.md §5.5 (task_done.sop.yaml):
//
//  1. Archive ~/.lightwave/artefacts/<task-id>/ → ~/.lightwave/archive/YYYY-MM/<task-id>.tar.gz
//  2. Remove the artefacts dir + the worktree at ~/.lightwave/worktrees/<task-id>
//  3. Close the linked GitHub issue (`gh issue close --reason completed`)
//  4. Log a memory entry under the `sessions` namespace
//
// These steps fire automatically inside v_core when the merged-PR webhook
// arrives. `lw task done` is the manual fallback for when v_core is
// offline or a SOP step has hung — DDD §8 row "Joel-merge webhook missed".
package task

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/memory"
)

// taskIDRe matches T-NNNN — the only ID format the artefact dir layout
// guarantees. Reject anything else early so we never feed a traversal
// fragment into tar / rm / shell.
var taskIDRe = regexp.MustCompile(`^T-\d{4}$`)

// DoneOptions configures a single `lw task done` invocation. Paths are
// roots, not per-task paths; per-task paths derive from TaskID. The Now
// func and Stdout writer are seams for tests.
type DoneOptions struct {
	TaskID string

	// Roots — default to ~/.lightwave/{artefacts,archive,worktrees} when
	// empty. Tests inject t.TempDir() subdirs.
	ArtefactsRoot string
	ArchiveRoot   string
	WorktreesRoot string

	// GitHub issue close inputs. When IssueNumber == 0 the gh-close step
	// is skipped with a logged warning — v_core's automatic path knows
	// the number from the webhook; the manual fallback may not.
	IssueNumber int
	Repo        string

	// Flags from the CLI surface.
	DryRun bool

	// SkipGitHub disables shelling out to `gh` even when IssueNumber is
	// non-zero. Tests set this; production callers leave it false.
	SkipGitHub bool

	// Now defaults to time.Now when nil. Tests pin it so archive paths
	// and the memory-log key are deterministic.
	Now func() time.Time

	// Stdout defaults to os.Stdout when nil. Tests capture into a buffer.
	Stdout io.Writer
}

// Result describes what `Done` did (or would have done, in dry-run).
type Result struct {
	TaskID        string
	ArchivePath   string // path to the tar.gz that was (or would be) created
	ArtefactsDir  string // the dir that was (or would be) removed
	WorktreeDir   string // the worktree dir that was (or would be) removed
	WorktreeExist bool   // true iff the worktree dir was present
	IssueClosed   bool   // true iff `gh issue close` ran successfully
	IssueSkipped  bool   // true iff the gh step was skipped (no number)
	MemoryLogPath string // path written under ~/.lightwave/memory/sessions/
	DryRun        bool
}

// Done runs the four cleanup steps. Returns an error if any *required*
// step fails: artefact dir missing, archive write fails, or memory write
// fails. The worktree-remove and gh-close steps are best-effort — they
// log a warning on failure but do not fail the call (the SOP intent is
// "clean up what's there"; a missing worktree or unreachable GitHub
// shouldn't strand the operator).
func Done(opts DoneOptions) (Result, error) {
	if !taskIDRe.MatchString(opts.TaskID) {
		return Result{}, fmt.Errorf("invalid task id %q (expected T-NNNN)", opts.TaskID)
	}
	out := opts.Stdout
	if out == nil {
		out = os.Stdout
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	artefactsRoot, archiveRoot, worktreesRoot, err := resolveRoots(opts)
	if err != nil {
		return Result{}, err
	}

	artefactsDir := filepath.Join(artefactsRoot, opts.TaskID)
	worktreeDir := filepath.Join(worktreesRoot, opts.TaskID)

	info, err := os.Stat(artefactsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, fmt.Errorf("artefacts dir not found: %s", artefactsDir)
		}
		return Result{}, fmt.Errorf("stat artefacts dir: %w", err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("artefacts path is not a directory: %s", artefactsDir)
	}

	ts := now().UTC()
	monthDir := filepath.Join(archiveRoot, ts.Format("2006-01"))
	archivePath := filepath.Join(monthDir, opts.TaskID+".tar.gz")

	_, worktreeStatErr := os.Stat(worktreeDir)
	worktreeExist := worktreeStatErr == nil

	res := Result{
		TaskID:        opts.TaskID,
		ArchivePath:   archivePath,
		ArtefactsDir:  artefactsDir,
		WorktreeDir:   worktreeDir,
		WorktreeExist: worktreeExist,
		IssueSkipped:  opts.IssueNumber == 0 || opts.SkipGitHub,
		DryRun:        opts.DryRun,
	}

	if opts.DryRun {
		fmt.Fprintf(out, "DRY RUN — no changes will be made\n")
		fmt.Fprintf(out, "  archive:  %s → %s\n", artefactsDir, archivePath)
		if worktreeExist {
			fmt.Fprintf(out, "  worktree: remove %s\n", worktreeDir)
		} else {
			fmt.Fprintf(out, "  worktree: (none at %s)\n", worktreeDir)
		}
		if res.IssueSkipped {
			fmt.Fprintf(out, "  gh:       (skipped — no --issue)\n")
		} else {
			fmt.Fprintf(out, "  gh:       close #%d in %s\n", opts.IssueNumber, opts.Repo)
		}
		fmt.Fprintf(out, "  memory:   sessions/%s-done-%s\n", opts.TaskID, ts.Format("2006-01-02"))
		return res, nil
	}

	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		return res, fmt.Errorf("mkdir archive dir: %w", err)
	}
	if err := tarGzDir(artefactsDir, opts.TaskID, archivePath); err != nil {
		return res, fmt.Errorf("archive: %w", err)
	}
	fmt.Fprintf(out, "archived %s\n", archivePath)

	if err := os.RemoveAll(artefactsDir); err != nil {
		return res, fmt.Errorf("remove artefacts dir: %w", err)
	}

	if worktreeExist {
		// Try `git worktree remove` first — it cleans the registry entry
		// in the parent repo. Falls back to RemoveAll when the registry
		// is gone (parent repo deleted, manual worktree, etc.).
		if err := exec.Command("git", "worktree", "remove", worktreeDir).Run(); err != nil {
			if rmErr := os.RemoveAll(worktreeDir); rmErr != nil {
				fmt.Fprintf(out, "warning: could not remove worktree %s: %v (git: %v)\n", worktreeDir, rmErr, err)
				res.WorktreeExist = true // tell the caller cleanup wasn't complete
			} else {
				fmt.Fprintf(out, "removed worktree (force) %s\n", worktreeDir)
			}
		} else {
			fmt.Fprintf(out, "removed worktree %s\n", worktreeDir)
		}
	}

	if !res.IssueSkipped {
		if err := closeGitHubIssue(opts.IssueNumber, opts.Repo); err != nil {
			fmt.Fprintf(out, "warning: gh issue close failed: %v\n", err)
		} else {
			res.IssueClosed = true
			fmt.Fprintf(out, "closed GitHub issue #%d in %s\n", opts.IssueNumber, opts.Repo)
		}
	}

	memKey := fmt.Sprintf("%s-done-%s", opts.TaskID, ts.Format("2006-01-02"))
	memPayload := fmt.Sprintf("task: %s\ndone_at: %s\nrepo: %s\nissue: %d\narchive: %s\n",
		opts.TaskID, ts.Format(time.RFC3339), opts.Repo, opts.IssueNumber, archivePath)
	memPath, err := memory.Put("sessions", memKey, []byte(memPayload))
	if err != nil {
		return res, fmt.Errorf("memory log: %w", err)
	}
	res.MemoryLogPath = memPath
	fmt.Fprintf(out, "logged %s\n", memPath)

	return res, nil
}

// resolveRoots fills in defaults for any unset root path. Defaults rebase
// on ~/.lightwave, matching the layout used by v_core and the v_core SOPs.
func resolveRoots(opts DoneOptions) (artefacts, archive, worktrees string, err error) {
	artefacts = opts.ArtefactsRoot
	archive = opts.ArchiveRoot
	worktrees = opts.WorktreesRoot
	if artefacts != "" && archive != "" && worktrees != "" {
		return
	}
	home, herr := os.UserHomeDir()
	if herr != nil {
		err = herr
		return
	}
	base := filepath.Join(home, ".lightwave")
	if artefacts == "" {
		artefacts = filepath.Join(base, "artefacts")
	}
	if archive == "" {
		archive = filepath.Join(base, "archive")
	}
	if worktrees == "" {
		worktrees = filepath.Join(base, "worktrees")
	}
	return
}

// tarGzDir writes srcDir as a gzipped tar at dst, storing entries under
// `nameInArchive/` (so untar produces `<task-id>/...`, matching the v_core
// SOP's `-C ~/.lightwave/artefacts T-NNNN/` shape).
func tarGzDir(srcDir, nameInArchive, dst string) error {
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	walkErr := filepath.Walk(srcDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return relErr
		}
		archiveName := nameInArchive
		if rel != "." {
			archiveName = filepath.ToSlash(filepath.Join(nameInArchive, rel))
		}

		hdr, hdrErr := tar.FileInfoHeader(info, "")
		if hdrErr != nil {
			return hdrErr
		}
		hdr.Name = archiveName
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		src, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(tw, src)
		_ = src.Close()
		return copyErr
	})
	if walkErr != nil {
		_ = tw.Close()
		_ = gz.Close()
		_ = f.Close()
		f = nil
		_ = os.Remove(tmp)
		return walkErr
	}

	if err := tw.Close(); err != nil {
		_ = gz.Close()
		_ = f.Close()
		f = nil
		_ = os.Remove(tmp)
		return err
	}
	if err := gz.Close(); err != nil {
		_ = f.Close()
		f = nil
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		f = nil
		_ = os.Remove(tmp)
		return err
	}
	f = nil
	return os.Rename(tmp, dst)
}

func closeGitHubIssue(number int, repo string) error {
	if number <= 0 {
		return errors.New("issue number must be positive")
	}
	args := []string{"issue", "close", fmt.Sprintf("%d", number), "--reason", "completed"}
	if strings.TrimSpace(repo) != "" {
		args = append(args, "--repo", repo)
	}
	cmd := exec.Command("gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
