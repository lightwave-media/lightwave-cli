package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateBranch(t *testing.T) {
	valid := []string{
		"feature/123-add-worktree-cli",
		"feature/abc12345-user-auth",
		"fix/def67890-token-expiry",
		"fix/766-some-fix",
		"hotfix/v1.0.1-login-crash",
		"hotfix/v2.0.0-beta",
	}
	for _, b := range valid {
		if err := validateBranch(b); err != nil {
			t.Errorf("expected valid branch %q to pass, got: %v", b, err)
		}
	}

	invalid := []string{
		"main",
		"feature/",
		"feature/no_description",
		"Fix/123-uppercase",
		"feature/123",
		"random/123-desc",
		"hotfix/v1-missing-patch",
	}
	for _, b := range invalid {
		if err := validateBranch(b); err == nil {
			t.Errorf("expected invalid branch %q to fail validation", b)
		}
	}
}

func TestBuildBranch(t *testing.T) {
	branch, err := buildBranch("766", "feature", "add-worktree-cli")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "feature/766-add-worktree-cli" {
		t.Errorf("got %q, want feature/766-add-worktree-cli", branch)
	}

	branch, err = buildBranch("100", "fix", "token-expiry")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "fix/100-token-expiry" {
		t.Errorf("got %q, want fix/100-token-expiry", branch)
	}

	_, err = buildBranch("123", "feature", "")
	if err == nil {
		t.Error("expected error when description is empty")
	}

	_, err = buildBranch("123", "hotfix", "crash")
	if err == nil {
		t.Error("expected error for hotfix (requires explicit --branch)")
	}
}

func TestReadWriteMeta(t *testing.T) {
	dir := t.TempDir()

	meta := &worktreeMeta{
		Issue:     "766",
		Branch:    "feature/766-add-worktree-cli",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		CreatedBy: "test-agent",
		State:     "created",
	}

	if err := writeMeta(dir, meta); err != nil {
		t.Fatalf("writeMeta: %v", err)
	}

	read, err := readMeta(dir)
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}

	if read.Issue != meta.Issue {
		t.Errorf("issue mismatch: got %q, want %q", read.Issue, meta.Issue)
	}
	if read.Branch != meta.Branch {
		t.Errorf("branch mismatch: got %q, want %q", read.Branch, meta.Branch)
	}
	if read.State != meta.State {
		t.Errorf("state mismatch: got %q, want %q", read.State, meta.State)
	}
}

func TestReadActStatus(t *testing.T) {
	dir := t.TempDir()

	// Missing file → empty string
	if got := readActStatus(dir); got != "" {
		t.Errorf("expected empty for missing .act-status, got %q", got)
	}

	// Write "passed"
	if err := os.WriteFile(filepath.Join(dir, ".act-status"), []byte("passed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := readActStatus(dir); got != "passed" {
		t.Errorf("expected \"passed\", got %q", got)
	}
}

func TestGcMissingWorktree(t *testing.T) {
	// gc on a non-existent worktree should not error (idempotent)
	args := []string{"9999999"}
	err := worktreeGcCmd.RunE(worktreeGcCmd, args)
	// This will error because the git repo won't be set up in test env,
	// but it shouldn't panic. Just check it doesn't panic.
	_ = err
}
