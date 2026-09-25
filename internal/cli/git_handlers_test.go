//nolint:testpackage // exercises unexported git discovery helpers
package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/testutil/gitfixture"
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

func hasViolationCode(vs []gitViolation, code string) bool {
	for _, v := range vs {
		if v.Code == code {
			return true
		}
	}

	return false
}

func TestCompareRequiredChecksDetectsDrift(t *testing.T) {
	t.Parallel()

	result := compareRequiredChecks(
		"lightwave-example",
		[]string{"ci / CI (mise)", "review"},
		[]string{"ci / CI (mise)", "legacy"},
	)

	if result.Status != "drift" {
		t.Fatalf("expected drift, got %+v", result)
	}
	if !containsString(result.Missing, "review") {
		t.Fatalf("expected missing review check, got %+v", result)
	}
	if !containsString(result.Unexpected, "legacy") {
		t.Fatalf("expected unexpected legacy check, got %+v", result)
	}
	if !strings.Contains(result.Detail, "do not remove") {
		t.Fatalf("drift guidance must reject circumvention, got %q", result.Detail)
	}
}

func TestCompareRequiredChecksRejectsEmptyAgreementAsHealthy(t *testing.T) {
	t.Parallel()

	result := compareRequiredChecks("lightwave-platform", nil, nil)

	if result.Status != "gap" {
		t.Fatalf("empty required checks must be an assurance gap, got %+v", result)
	}
	if !strings.Contains(result.Detail, "report and block nothing") {
		t.Fatalf("gap guidance must explain the merge-boundary risk, got %q", result.Detail)
	}
}

func TestMachineReadableAuditErrorsRemainBlocking(t *testing.T) {
	t.Parallel()

	if err := gitAuditResultError(1); err == nil || !strings.Contains(err.Error(), "do not bypass") {
		t.Fatalf("git audit error must be blocking and actionable, got %v", err)
	}
	if err := gitDoctorResultError("/repo", 1); err == nil || !strings.Contains(err.Error(), "blanket bypass") {
		t.Fatalf("git doctor error must be blocking and actionable, got %v", err)
	}
}

func TestAssuranceFlagShapes(t *testing.T) {
	t.Parallel()

	if !isBooleanFlag("remote") {
		t.Fatal("--remote must be boolean or the live audit silently stays disabled")
	}
	if !isStringArrayFlag("affected-path") {
		t.Fatal("--affected-path must preserve repeated failure evidence")
	}
}

//nolint:paralleltest // shared fixture mutation via os.Chtimes
func TestWorktreePolicyViolations(t *testing.T) {
	tmp := t.TempDir()
	mainCheckout := filepath.Join(tmp, "repo")
	// worktree_home_policy v2.0.0: an absolute canonical_root with {repo}
	// underneath. The v1.x repo-relative ".worktrees" is now a legacy root,
	// so using it here would test the superseded layout.
	profile := &localSetupProfile{WorktreeRoot: filepath.Join(tmp, "wt-home")}
	canonicalRepoRoot := filepath.Join("wt-home", "repo")

	mkWorktreeDir := func(rel string, withMarker bool) string {
		t.Helper()

		dir := filepath.Join(tmp, rel)
		if err := os.MkdirAll(dir, gitDirPerm); err != nil {
			t.Fatal(err)
		}

		if withMarker {
			if err := os.WriteFile(filepath.Join(dir, worktreeMarkerFile), []byte("session_id: test\n"), gitFilePerm); err != nil {
				t.Fatal(err)
			}
		}

		return dir
	}

	tests := []struct {
		name        string
		checkout    string
		wantCodes   []string
		absentCodes []string
	}{
		{
			name:        "registered tree in the canonical root is clean",
			checkout:    mkWorktreeDir(filepath.Join(canonicalRepoRoot, "good-slug"), true),
			absentCodes: []string{"legacy_worktree_root", "undeclared_worktree_root", "naming_violation", "missing_marker", "expired_worktree"},
		},
		{
			// The v2.0.0 inversion, half one. .claude/worktrees left
			// forbidden_roots and became supplemental: a tree there carrying
			// the project_workspace record is CONFORMING, not a violation.
			name:        "registered tree in a supplemental vendor root is conforming",
			checkout:    mkWorktreeDir("repo/.claude/worktrees/sess-abc123", true),
			absentCodes: []string{"legacy_worktree_root", "undeclared_worktree_root", "missing_marker"},
		},
		{
			// The v2.0.0 inversion, half two. The record, not the path,
			// decides. Same vendor root, no record, so it is nonconforming —
			// and never invisible.
			name:        "unregistered tree in a supplemental root is nonconforming",
			checkout:    mkWorktreeDir("repo/.claude/worktrees/sess-unmarked", false),
			wantCodes:   []string{"missing_marker"},
			absentCodes: []string{"undeclared_worktree_root"},
		},
		{
			// The old canonical root. Existing trees register in place and
			// drain; the finding must not imply relocating them.
			name:      "the v1.x repo root takes no new allocation",
			checkout:  mkWorktreeDir("repo/.worktrees/some-slug", true),
			wantCodes: []string{"legacy_worktree_root"},
		},
		{
			name:      "a root no policy names is undeclared",
			checkout:  mkWorktreeDir("elsewhere/some-worktree", true),
			wantCodes: []string{"undeclared_worktree_root"},
		},
		{
			// v2.0.0 naming is {slug}. A date prefix is allowed but no longer
			// required, so only a non-slug shape may fail here.
			name:      "non-slug name in the canonical root",
			checkout:  mkWorktreeDir(filepath.Join(canonicalRepoRoot, "Bad_Slug"), true),
			wantCodes: []string{"naming_violation"},
		},
		{
			name:        "the v1.x date-prefixed name is still a valid slug",
			checkout:    mkWorktreeDir(filepath.Join(canonicalRepoRoot, "2026-08-10-good-slug"), true),
			absentCodes: []string{"naming_violation"},
		},
		{
			name:      "missing claim marker",
			checkout:  mkWorktreeDir(filepath.Join(canonicalRepoRoot, "unmarked"), false),
			wantCodes: []string{"missing_marker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := worktreePolicyViolations(tt.checkout, mainCheckout, "feature/1-x", profile)

			for _, code := range tt.wantCodes {
				if !hasViolationCode(got, code) {
					t.Errorf("expected violation %q, got %+v", code, got)
				}
			}

			for _, code := range tt.absentCodes {
				if hasViolationCode(got, code) {
					t.Errorf("unexpected violation %q in %+v", code, got)
				}
			}
		})
	}
}

//nolint:paralleltest // mutates fixture mtime
func TestWorktreePolicyViolationsExpiry(t *testing.T) {
	tmp := t.TempDir()
	mainCheckout := filepath.Join(tmp, "repo")
	profile := &localSetupProfile{WorktreeRoot: ".worktrees"}

	dir := filepath.Join(mainCheckout, ".worktrees", "2026-08-01-ancient")
	if err := os.MkdirAll(dir, gitDirPerm); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, worktreeMarkerFile), []byte("session_id: test\n"), gitFilePerm); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-worktreeMaxAge - time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}

	got := worktreePolicyViolations(dir, mainCheckout, "feature/1-x", profile)
	if !hasViolationCode(got, "expired_worktree") {
		t.Errorf("expected expired_worktree, got %+v", got)
	}
}

func TestRepoLevelWorktreeViolations(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	mainCheckout := filepath.Join(tmp, "repo")
	profile := &localSetupProfile{WorktreeRoot: ".worktrees"}

	registeredPath := filepath.Join(mainCheckout, ".worktrees", "2026-08-10-registered")
	ghostPath := filepath.Join(mainCheckout, ".worktrees", "2026-07-29-ghost")

	for _, d := range []string{registeredPath, ghostPath} {
		if err := os.MkdirAll(d, gitDirPerm); err != nil {
			t.Fatal(err)
		}
	}

	registered := map[string]bool{mainCheckout: true, registeredPath: true}
	prunable := []string{filepath.Join(mainCheckout, ".worktrees", "2026-06-01-vanished")}

	got := repoLevelWorktreeViolations(mainCheckout, registered, prunable, profile)

	if !hasViolationCode(got, "prunable_registry_entry") {
		t.Errorf("expected prunable_registry_entry, got %+v", got)
	}

	if !hasViolationCode(got, "orphan_gitdir") {
		t.Errorf("expected orphan_gitdir for %s, got %+v", ghostPath, got)
	}

	for _, v := range got {
		if v.Code == "orphan_gitdir" && strings.Contains(v.Message, registeredPath) {
			t.Errorf("registered worktree flagged as orphan: %+v", v)
		}
	}
}

//nolint:paralleltest // git init in temp dir
func TestBuildGitAuditReportSeesRepoRelativeWorktrees(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")

	if err := os.MkdirAll(repo, gitDirPerm); err != nil {
		t.Fatal(err)
	}

	runGit(t, repo, "init")
	runGit(t, repo, "commit", "--allow-empty", "-m", "init")
	runGit(t, repo, "worktree", "add", filepath.Join(".worktrees", "2026-08-10-linked"), "-b", "feature/1-linked")

	// A directory under the canonical root that git does not recognize —
	// the cloud-session ghost class (unresolvable gitdir).
	if err := os.MkdirAll(filepath.Join(repo, ".worktrees", "2026-07-29-ghost"), gitDirPerm); err != nil {
		t.Fatal(err)
	}

	profile := localSetupProfile{
		WorkspaceRoots: []string{tmp},
		WorktreeRoot:   ".worktrees",
		StrictMarkers:  []string{"mise.toml"},
		RequiredHooks:  map[string][]string{"strict": {}, "advisory": {}},
	}

	report, err := buildGitAuditReport(context.Background(), &profile)
	if err != nil {
		t.Fatal(err)
	}

	if report.Summary.TotalRepos != 2 {
		t.Fatalf("expected main + 1 linked worktree, got %d nodes", report.Summary.TotalRepos)
	}

	var mainNode, linked *gitTopologyNode

	for i := range report.Repos {
		if report.Repos[i].IsWorktree {
			linked = &report.Repos[i]
		} else {
			mainNode = &report.Repos[i]
		}
	}

	if mainNode == nil || linked == nil {
		t.Fatalf("expected one main and one linked node, got %+v", report.Repos)
	}

	if !hasViolationCode(mainNode.Violations, "orphan_gitdir") {
		t.Errorf("main node missing orphan_gitdir for ghost dir: %+v", mainNode.Violations)
	}

	if !hasViolationCode(linked.Violations, "missing_marker") {
		t.Errorf("linked worktree missing missing_marker: %+v", linked.Violations)
	}

	if hasViolationCode(linked.Violations, "naming_violation") {
		t.Errorf("conforming name flagged: %+v", linked.Violations)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...)
	c.Dir = dir
	c.Env = gitfixture.Env()
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
