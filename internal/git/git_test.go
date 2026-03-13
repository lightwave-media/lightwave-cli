package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = dir
	_ = cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	_ = cmd.Run()

	testFile := filepath.Join(dir, "README.md")
	if err := os.WriteFile(testFile, []byte("# Test\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = dir
	_ = cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = dir
	_ = cmd.Run()

	return dir
}

func TestNewGit(t *testing.T) {
	g := NewGit("/tmp/test")
	if g.WorkDir() != "/tmp/test" {
		t.Errorf("WorkDir() = %q, want /tmp/test", g.WorkDir())
	}

	gd := NewGitWithDir("/tmp/bare.git", "/tmp/work")
	if gd.WorkDir() != "/tmp/work" {
		t.Errorf("WorkDir() = %q, want /tmp/work", gd.WorkDir())
	}
}

func TestIsRepo_NonRepo(t *testing.T) {
	dir := t.TempDir()
	g := NewGit(dir)

	if g.IsRepo() {
		t.Fatal("expected IsRepo to be false for empty dir")
	}

	// Verify GitError is returned from operations on non-repo
	_, err := g.CurrentBranch()
	gitErr, ok := err.(*GitError)
	if !ok {
		t.Errorf("expected GitError, got %T: %v", err, err)
		return
	}
	if gitErr.Stderr == "" {
		t.Errorf("expected GitError with Stderr, got empty stderr")
	}
}

// realPath resolves symlinks to handle macOS /var -> /private/var.
func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

func TestWorktreeList_Parsing(t *testing.T) {
	dir := initTestRepo(t)
	dirReal := realPath(t, dir)
	g := NewGit(dir)

	// A regular repo lists itself as the main worktree
	worktrees, err := g.WorktreeList()
	if err != nil {
		t.Fatalf("WorktreeList: %v", err)
	}

	if len(worktrees) != 1 {
		t.Fatalf("expected 1 worktree (main), got %d", len(worktrees))
	}

	wt := worktrees[0]
	if wt.Path != dirReal {
		t.Errorf("worktree path = %q, want %q", wt.Path, dirReal)
	}
	if wt.Commit == "" {
		t.Error("expected non-empty commit hash")
	}
	if len(wt.Commit) != 40 {
		t.Errorf("commit hash length = %d, want 40", len(wt.Commit))
	}

	// Verify branch is main or master
	if wt.Branch != "main" && wt.Branch != "master" {
		t.Errorf("worktree branch = %q, want main or master", wt.Branch)
	}

	// Add a worktree and verify listing
	wtDir := filepath.Join(t.TempDir(), "feature-wt")
	wtDirReal := realPath(t, filepath.Dir(wtDir))
	wtDirExpected := filepath.Join(wtDirReal, "feature-wt")
	if err := g.WorktreeAdd(wtDir, "feature-branch"); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	worktrees, err = g.WorktreeList()
	if err != nil {
		t.Fatalf("WorktreeList after add: %v", err)
	}

	if len(worktrees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(worktrees))
	}

	// Find the new worktree
	var found bool
	for _, w := range worktrees {
		if w.Branch == "feature-branch" {
			found = true
			if w.Path != wtDirExpected {
				t.Errorf("feature worktree path = %q, want %q", w.Path, wtDirExpected)
			}
			break
		}
	}
	if !found {
		t.Error("feature-branch worktree not found in list")
	}
}

func TestIsRepo_Valid(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	if !g.IsRepo() {
		t.Fatal("expected IsRepo to be true for initialized repo")
	}
}

func TestCurrentBranch(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	branch, err := g.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}

	if branch != "main" && branch != "master" {
		t.Errorf("branch = %q, want main or master", branch)
	}
}

func TestStatus(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	status, err := g.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Clean {
		t.Error("expected clean status")
	}

	// Add an untracked file
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	status, err = g.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Clean {
		t.Error("expected dirty status")
	}
	if len(status.Untracked) != 1 {
		t.Errorf("untracked = %d, want 1", len(status.Untracked))
	}
}

func TestRev(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	hash, err := g.Rev("HEAD")
	if err != nil {
		t.Fatalf("Rev: %v", err)
	}

	if len(hash) != 40 {
		t.Errorf("hash length = %d, want 40", len(hash))
	}
}

func TestAddAndCommit(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := g.Add("new.txt"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := g.Commit("add new file"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	status, err := g.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Clean {
		t.Error("expected clean after commit")
	}
}

func TestCheckoutNewBranch(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	if err := g.CheckoutNewBranch("feature", "HEAD"); err != nil {
		t.Fatalf("CheckoutNewBranch: %v", err)
	}

	branch, _ := g.CurrentBranch()
	if branch != "feature" {
		t.Errorf("branch = %q, want feature", branch)
	}
}

func TestBranchExists(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	mainBranch, _ := g.CurrentBranch()
	exists, err := g.BranchExists(mainBranch)
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if !exists {
		t.Error("expected current branch to exist")
	}

	exists, err = g.BranchExists("nonexistent-branch-xyz")
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if exists {
		t.Error("expected nonexistent branch to not exist")
	}
}

func TestConfigGet(t *testing.T) {
	dir := initTestRepo(t)
	g := NewGit(dir)

	email, err := g.ConfigGet("user.email")
	if err != nil {
		t.Fatalf("ConfigGet: %v", err)
	}
	if email != "test@test.com" {
		t.Errorf("user.email = %q, want test@test.com", email)
	}

	// Nonexistent key returns empty string, not error
	val, err := g.ConfigGet("nonexistent.key")
	if err != nil {
		t.Fatalf("ConfigGet nonexistent: %v", err)
	}
	if val != "" {
		t.Errorf("nonexistent key = %q, want empty", val)
	}
}

func TestUncommittedWorkStatus_Clean(t *testing.T) {
	s := &UncommittedWorkStatus{}
	if !s.Clean() {
		t.Error("expected clean status")
	}
	if s.String() != "clean" {
		t.Errorf("String() = %q, want clean", s.String())
	}
}

func TestUncommittedWorkStatus_Dirty(t *testing.T) {
	s := &UncommittedWorkStatus{
		HasUncommittedChanges: true,
		StashCount:            2,
		UnpushedCommits:       1,
		ModifiedFiles:         []string{"a.go", "b.go"},
		UntrackedFiles:        []string{"c.go"},
	}
	if s.Clean() {
		t.Error("expected dirty status")
	}
	str := s.String()
	if !strings.Contains(str, "3 uncommitted change(s)") {
		t.Errorf("String() = %q, missing uncommitted changes", str)
	}
	if !strings.Contains(str, "2 stash(es)") {
		t.Errorf("String() = %q, missing stashes", str)
	}
	if !strings.Contains(str, "1 unpushed commit(s)") {
		t.Errorf("String() = %q, missing unpushed commits", str)
	}
}
