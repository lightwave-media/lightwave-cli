//nolint:testpackage // exercises unexported git discovery helpers
package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitMapHandlerEmptyRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// No workspace.yaml — uses default joelschaeffer root which won't exist
	report, err := buildGitAuditReport(context.Background(), &localSetupProfile{
		ID:             "test",
		WorkspaceRoots: []string{filepath.Join(home, "empty")},
		StrictMarkers:  []string{"mise.toml"},
		RequiredHooks:  map[string][]string{"strict": {"pre-commit"}, "advisory": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.TotalRepos != 0 {
		t.Fatalf("expected 0 repos, got %d", report.Summary.TotalRepos)
	}
}

//nolint:paralleltest // git init in temp dir
func TestInspectGitCheckoutStrictTier(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
	_ = os.WriteFile(filepath.Join(dir, "mise.toml"), []byte("[tasks.ci]\nrun = 'true'\n"), gitFilePerm)
	_ = os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# agents\n"), gitFilePerm)

	profile := localSetupProfile{
		StrictMarkers: []string{"mise.toml", "AGENTS.md"},
		RequiredHooks: map[string][]string{
			"strict":   {"pre-commit"},
			"advisory": {},
		},
	}
	node, err := inspectGitCheckout(context.Background(), dir, &profile)
	if err != nil {
		t.Fatal(err)
	}
	if node.RepoInfraTier != gitTierStrict {
		t.Fatalf("expected strict tier, got %s", node.RepoInfraTier)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
