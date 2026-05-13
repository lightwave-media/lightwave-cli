package task

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pinLightwaveRoots redirects HOME so memory.Put writes under t.TempDir()
// (memory.Root resolves HOME at call time) and returns artefacts/archive/
// worktrees roots rooted under the same tmp dir.
func pinLightwaveRoots(t *testing.T) (artefacts, archive, worktrees string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, ".lightwave")
	return filepath.Join(base, "artefacts"), filepath.Join(base, "archive"), filepath.Join(base, "worktrees")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fixedNow returns a stable timestamp so archive month dirs + memory keys
// don't drift across the calendar boundary.
func fixedNow() time.Time {
	return time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
}

func TestDone_HappyPath(t *testing.T) {
	artefacts, archive, worktrees := pinLightwaveRoots(t)

	taskID := "T-9999"
	writeFile(t, filepath.Join(artefacts, taskID, "01-research.md"), "# research\n")
	writeFile(t, filepath.Join(artefacts, taskID, "subdir", "02-spec.md"), "# spec\n")

	var stdout bytes.Buffer
	res, err := Done(DoneOptions{
		TaskID:        taskID,
		ArtefactsRoot: artefacts,
		ArchiveRoot:   archive,
		WorktreesRoot: worktrees,
		SkipGitHub:    true, // unit test must never shell out to gh
		Now:           fixedNow,
		Stdout:        &stdout,
	})
	if err != nil {
		t.Fatalf("Done: %v", err)
	}

	// Archive lands at the expected path with the YYYY-MM month dir.
	wantArchive := filepath.Join(archive, "2026-05", taskID+".tar.gz")
	if res.ArchivePath != wantArchive {
		t.Errorf("ArchivePath = %s, want %s", res.ArchivePath, wantArchive)
	}
	if _, err := os.Stat(wantArchive); err != nil {
		t.Fatalf("archive not written: %v", err)
	}

	// Artefacts dir is gone.
	if _, err := os.Stat(filepath.Join(artefacts, taskID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("artefacts dir still exists: %v", err)
	}

	// Archive contents include both files under T-9999/.
	names := readTarGzNames(t, wantArchive)
	wantNames := map[string]bool{
		taskID + "/":                  true,
		taskID + "/01-research.md":    true,
		taskID + "/subdir":            false, // dir entry shape allowed either way
		taskID + "/subdir/":           false,
		taskID + "/subdir/02-spec.md": true,
	}
	for n, required := range wantNames {
		if required && !names[n] {
			t.Errorf("archive missing entry %q (have %v)", n, names)
		}
	}

	// Memory log written under ~/.lightwave/memory/sessions/.
	if res.MemoryLogPath == "" {
		t.Fatal("MemoryLogPath empty")
	}
	if !strings.Contains(res.MemoryLogPath, filepath.Join(".lightwave", "memory", "sessions")) {
		t.Errorf("MemoryLogPath = %s, want under ~/.lightwave/memory/sessions/", res.MemoryLogPath)
	}
	body, err := os.ReadFile(res.MemoryLogPath)
	if err != nil {
		t.Fatalf("read memory log: %v", err)
	}
	if !bytes.Contains(body, []byte("task: T-9999")) {
		t.Errorf("memory log missing task id: %s", body)
	}

	// gh was skipped — IssueClosed must stay false, IssueSkipped true.
	if res.IssueClosed {
		t.Error("IssueClosed should be false when SkipGitHub=true")
	}
	if !res.IssueSkipped {
		t.Error("IssueSkipped should be true when SkipGitHub=true")
	}

	// stdout summary mentions the archive + memory paths.
	out := stdout.String()
	if !strings.Contains(out, "archived ") {
		t.Errorf("stdout missing 'archived ': %s", out)
	}
	if !strings.Contains(out, "logged ") {
		t.Errorf("stdout missing 'logged ': %s", out)
	}
}

func TestDone_DryRun_NoSideEffects(t *testing.T) {
	artefacts, archive, worktrees := pinLightwaveRoots(t)

	taskID := "T-0042"
	srcFile := filepath.Join(artefacts, taskID, "plan.md")
	writeFile(t, srcFile, "# plan\n")

	var stdout bytes.Buffer
	res, err := Done(DoneOptions{
		TaskID:        taskID,
		ArtefactsRoot: artefacts,
		ArchiveRoot:   archive,
		WorktreesRoot: worktrees,
		DryRun:        true,
		SkipGitHub:    true,
		IssueNumber:   42,
		Repo:          "lightwave-media/lightwave-cli",
		Now:           fixedNow,
		Stdout:        &stdout,
	})
	if err != nil {
		t.Fatalf("Done dry-run: %v", err)
	}
	if !res.DryRun {
		t.Error("Result.DryRun should be true")
	}

	// Artefacts dir still present.
	if _, err := os.Stat(srcFile); err != nil {
		t.Errorf("dry-run removed artefacts: %v", err)
	}
	// No archive on disk.
	wantArchive := filepath.Join(archive, "2026-05", taskID+".tar.gz")
	if _, err := os.Stat(wantArchive); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry-run wrote archive: %v", err)
	}
	// No memory log on disk.
	if res.MemoryLogPath != "" {
		t.Errorf("dry-run set MemoryLogPath = %s", res.MemoryLogPath)
	}
	// stdout banner present.
	if !strings.Contains(stdout.String(), "DRY RUN") {
		t.Errorf("dry-run missing banner: %s", stdout.String())
	}
}

func TestDone_RejectsInvalidTaskID(t *testing.T) {
	artefacts, archive, worktrees := pinLightwaveRoots(t)
	for _, bad := range []string{"", "T-1", "T-99999", "../etc/passwd", "T-0001/../escape", "t-0001"} {
		_, err := Done(DoneOptions{
			TaskID:        bad,
			ArtefactsRoot: artefacts,
			ArchiveRoot:   archive,
			WorktreesRoot: worktrees,
			SkipGitHub:    true,
			Now:           fixedNow,
			Stdout:        io.Discard,
		})
		if err == nil {
			t.Errorf("expected rejection for task id %q", bad)
		}
	}
}

func TestDone_MissingArtefactsDir(t *testing.T) {
	artefacts, archive, worktrees := pinLightwaveRoots(t)
	_, err := Done(DoneOptions{
		TaskID:        "T-0001",
		ArtefactsRoot: artefacts,
		ArchiveRoot:   archive,
		WorktreesRoot: worktrees,
		SkipGitHub:    true,
		Now:           fixedNow,
		Stdout:        io.Discard,
	})
	if err == nil {
		t.Fatal("expected error when artefacts dir absent")
	}
	if !strings.Contains(err.Error(), "artefacts dir not found") {
		t.Errorf("error message = %v, want 'artefacts dir not found'", err)
	}
}

func TestDone_RemovesWorktreeWhenPresent(t *testing.T) {
	artefacts, archive, worktrees := pinLightwaveRoots(t)

	taskID := "T-0500"
	writeFile(t, filepath.Join(artefacts, taskID, "report.md"), "ok\n")
	writeFile(t, filepath.Join(worktrees, taskID, "checked-out-file.go"), "package x\n")

	_, err := Done(DoneOptions{
		TaskID:        taskID,
		ArtefactsRoot: artefacts,
		ArchiveRoot:   archive,
		WorktreesRoot: worktrees,
		SkipGitHub:    true,
		Now:           fixedNow,
		Stdout:        io.Discard,
	})
	if err != nil {
		t.Fatalf("Done: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktrees, taskID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree dir still exists: %v", err)
	}
}

// readTarGzNames returns the set of entry names in a gzipped tarball.
func readTarGzNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		names[hdr.Name] = true
	}
	return names
}
