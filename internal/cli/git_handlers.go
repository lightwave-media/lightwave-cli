package cli

// git_handlers.go — lw git audit | map | doctor | worktree list
//
// Fleet-level git topology discovery and safe remediation. Single-repo
// dirty-tree checks remain under `lw check git`.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fatih/color"
	"gopkg.in/yaml.v3"
)

const (
	gitTierStrict       = "strict"
	gitTierAdvisory     = "advisory"
	gitSeverityWarn     = "warn"
	gitSeverityError    = "error"
	gitStatusFieldWidth = 2
	gitDirPerm          = 0o755
	gitFilePerm         = 0o644
)

func init() {
	RegisterHandler("git.audit", gitAuditHandler)
	RegisterHandler("git.map", gitMapHandler)
	RegisterHandler("git.doctor", gitDoctorHandler)
	RegisterHandler("git.worktree", gitWorktreeHandler)
}

type gitViolation struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type gitDirtyCounts struct {
	Staged    int `json:"staged"`
	Unstaged  int `json:"unstaged"`
	Untracked int `json:"untracked"`
}

type gitRemoteRef struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Fetch bool   `json:"fetch"`
	Push  bool   `json:"push"`
}

type gitReachability struct {
	LastFetchISO     string `json:"last_fetch_iso,omitempty"`
	CredentialHelper string `json:"credential_helper,omitempty"`
	SubmoduleCount   int    `json:"submodule_count"`
}

type gitTopologyNode struct {
	HooksPath       string          `json:"hooks_path,omitempty"`
	GitDir          string          `json:"git_dir"`
	CommonDir       string          `json:"common_dir"`
	RepoName        string          `json:"repo_name,omitempty"`
	Branch          string          `json:"branch"`
	Upstream        string          `json:"upstream,omitempty"`
	RepoInfraTier   string          `json:"repo_infra_tier"`
	Path            string          `json:"path"`
	Reachability    gitReachability `json:"reachability,omitempty"`
	HooksInstalled  []string        `json:"hooks_installed,omitempty"`
	Remotes         []gitRemoteRef  `json:"remotes,omitempty"`
	LinkedWorktrees []string        `json:"linked_worktrees,omitempty"`
	Violations      []gitViolation  `json:"violations"`
	Dirty           gitDirtyCounts  `json:"dirty,omitempty"`
	Behind          int             `json:"behind,omitempty"`
	Ahead           int             `json:"ahead,omitempty"`
	IsWorktree      bool            `json:"is_worktree"`
}

type gitAuditSummary struct {
	TotalRepos     int `json:"total_repos"`
	TotalWorktrees int `json:"total_worktrees"`
	StrictRepos    int `json:"strict_repos"`
	AdvisoryRepos  int `json:"advisory_repos"`
	Errors         int `json:"errors"`
	Warnings       int `json:"warnings"`
	Info           int `json:"info"`
}

type gitRecommendedAction struct {
	ID            string `json:"id"`
	Description   string `json:"description"`
	DryRunCommand string `json:"dry_run_command"`
	ApplyCommand  string `json:"apply_command,omitempty"`
	SafeToAutoFix bool   `json:"safe_to_auto_fix"`
}

type gitAuditReport struct {
	ScanTimeISO        string                 `json:"scan_time_iso"`
	MachineID          string                 `json:"machine_id"`
	ProfileID          string                 `json:"profile_id,omitempty"`
	FixMode            string                 `json:"fix_mode,omitempty"`
	ScannedRoots       []string               `json:"scanned_roots"`
	Repos              []gitTopologyNode      `json:"repos"`
	RecommendedActions []gitRecommendedAction `json:"recommended_actions"`
	Summary            gitAuditSummary        `json:"summary"`
}

type localSetupProfile struct {
	RequiredHooks       map[string][]string `yaml:"required_hooks"`
	ID                  string              `yaml:"id"`
	WorktreeRoot        string              `yaml:"worktree_root"`
	WorkspaceRoots      []string            `yaml:"workspace_roots"`
	CursorWorktreeRoots []string            `yaml:"cursor_worktree_roots"`
	StrictMarkers       []string            `yaml:"strict_markers"`
}

func gitAuditHandler(ctx context.Context, _ []string, flags map[string]any) error {
	profile := loadWorkspaceProfile()

	report, err := buildGitAuditReport(ctx, &profile)
	if err != nil {
		return err
	}

	fix := flagBool(flags, "fix")
	apply := flagBool(flags, "apply")
	dryRun := !apply || flagBool(flags, "dry-run")

	if fix {
		if dryRun {
			report.FixMode = "dry-run"
			report.RecommendedActions = append(report.RecommendedActions,
				gitRecommendedAction{
					ID:            "prune-stale-worktrees",
					Description:   "Would run git worktree prune and remove orphan dirs under worktree_root",
					SafeToAutoFix: true,
					DryRunCommand: "lw git audit --fix --dry-run",
					ApplyCommand:  "lw git audit --fix --apply",
				},
			)
		} else {
			report.FixMode = "apply"
			if err := applyGitFixes(ctx, report, &profile); err != nil {
				return err
			}
		}
	}

	if err := writeAuditReportFile(report); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write report: %v\n", err)
	}

	if asJSON(flags) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(report)
	}

	printGitAuditTTY(report)

	if report.Summary.Errors > 0 {
		return fmt.Errorf("%d error-level git violation(s) — see report above", report.Summary.Errors)
	}

	return nil
}

func gitMapHandler(ctx context.Context, _ []string, flags map[string]any) error {
	profile := loadWorkspaceProfile()
	repoFilter := flagString(flags, "repo")

	report, err := buildGitAuditReport(ctx, &profile)
	if err != nil {
		return err
	}

	nodes := report.Repos
	if repoFilter != "" {
		nodes = filterNodesByRepo(nodes, repoFilter)
	}

	if asJSON(flags) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(nodes)
	}

	for i := range nodes {
		n := nodes[i]

		wt := ""
		if n.IsWorktree {
			wt = " [worktree]"
		}

		fmt.Printf("%s%s  branch=%s tier=%s violations=%d\n",
			n.Path, wt, n.Branch, n.RepoInfraTier, len(n.Violations))
	}

	return nil
}

func gitDoctorHandler(ctx context.Context, _ []string, flags map[string]any) error {
	repo := flagString(flags, "repo")
	if repo == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		repo, err = nearestRepoRoot(cwd)
		if err != nil {
			return err
		}
	}

	profile := loadWorkspaceProfile()

	node, err := inspectGitCheckout(ctx, repo, &profile)
	if err != nil {
		return err
	}
	// Doctor: hooks + error-severity only
	var doctorViols []gitViolation

	for _, v := range node.Violations {
		if v.Code == "hooks_drift" || v.Severity == gitSeverityError {
			doctorViols = append(doctorViols, v)
		}
	}

	node.Violations = doctorViols

	if asJSON(flags) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(node)
	}

	if len(doctorViols) == 0 {
		fmt.Println(color.GreenString("✓ git doctor: hooks and profile OK for " + repo))
		return nil
	}

	for _, v := range doctorViols {
		fmt.Printf("  %s %s\n", severityColor(v.Severity), v.Message)
	}

	return fmt.Errorf("git doctor: %d issue(s) in %s", len(doctorViols), repo)
}

func gitWorktreeHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) == 0 || args[0] != "list" {
		return errors.New("usage: lw git worktree list")
	}

	profile := loadWorkspaceProfile()

	report, err := buildGitAuditReport(ctx, &profile)
	if err != nil {
		return err
	}

	type row struct {
		Path       string `json:"path"`
		CommonDir  string `json:"common_dir"`
		Branch     string `json:"branch"`
		IsWorktree bool   `json:"is_worktree"`
	}

	rows := make([]row, 0, len(report.Repos))
	for i := range report.Repos {
		n := report.Repos[i]
		rows = append(rows, row{
			Path:       n.Path,
			CommonDir:  n.CommonDir,
			Branch:     n.Branch,
			IsWorktree: n.IsWorktree,
		})
	}

	if asJSON(flags) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(rows)
	}

	for _, r := range rows {
		mark := "main"
		if r.IsWorktree {
			mark = "linked"
		}

		fmt.Printf("%-8s %-40s %s\n", mark, r.Path, r.Branch)
	}

	return nil
}

func loadWorkspaceProfile() localSetupProfile {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".lightwave", "config", "workspace.yaml")
	def := localSetupProfile{
		ID:             "default",
		WorkspaceRoots: []string{filepath.Join(home, "dev")},
		WorktreeRoot:   filepath.Join(home, ".lightwave", "worktrees"),
		CursorWorktreeRoots: []string{
			filepath.Join(home, ".cursor", "worktrees"),
		},
		StrictMarkers: []string{"mise.toml", "AGENTS.md"},
		RequiredHooks: map[string][]string{
			"strict":   {"pre-commit", "pre-push", "commit-msg"},
			"advisory": {"pre-commit"},
		},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return def
	}

	var wrapper struct {
		Profile localSetupProfile `yaml:"profile"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return def
	}

	if wrapper.Profile.ID == "" {
		return def
	}

	p := wrapper.Profile
	for i, r := range p.WorkspaceRoots {
		p.WorkspaceRoots[i] = expandHome(r)
	}

	p.WorktreeRoot = expandHome(p.WorktreeRoot)
	for i, r := range p.CursorWorktreeRoots {
		p.CursorWorktreeRoots[i] = expandHome(r)
	}

	return p
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}

	return p
}

func buildGitAuditReport(ctx context.Context, profile *localSetupProfile) (*gitAuditReport, error) { //nolint:unparam // error reserved for future walk failures
	roots := append([]string{}, profile.WorkspaceRoots...)
	roots = append(roots, profile.CursorWorktreeRoots...)

	seenCommon := map[string]bool{}

	var nodes []gitTopologyNode

	for _, root := range roots {
		if root == "" {
			continue
		}

		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // skip unreadable subtrees during fleet scan
			}

			if d.Name() != ".git" {
				return nil
			}

			checkout := filepath.Dir(path)

			commonDir, err := gitOutput(ctx, checkout, "rev-parse", "--git-common-dir")
			if err != nil {
				return nil //nolint:nilerr // skip invalid git metadata during fleet scan
			}

			if !filepath.IsAbs(commonDir) {
				commonDir = filepath.Join(checkout, commonDir)
			}

			commonDir, _ = filepath.Abs(commonDir)
			if seenCommon[commonDir] {
				return filepath.SkipDir
			}

			seenCommon[commonDir] = true

			wtOut, _ := gitOutput(ctx, checkout, "worktree", "list", "--porcelain")
			for _, line := range strings.Split(wtOut, "\n") {
				if !strings.HasPrefix(line, "worktree ") {
					continue
				}

				wtPath := strings.TrimPrefix(line, "worktree ")

				node, nerr := inspectGitCheckout(ctx, wtPath, profile)
				if nerr != nil {
					continue
				}

				nodes = append(nodes, node)
			}

			return filepath.SkipDir
		})
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })

	report := &gitAuditReport{
		ScannedRoots: profile.WorkspaceRoots,
		ScanTimeISO:  time.Now().UTC().Format(time.RFC3339),
		MachineID:    hostname(),
		ProfileID:    profile.ID,
		Repos:        nodes,
	}
	report.Summary = summarizeNodes(nodes)

	return report, nil
}

func inspectGitCheckout(ctx context.Context, checkout string, profile *localSetupProfile) (gitTopologyNode, error) {
	checkout, _ = filepath.Abs(checkout)

	gitDir, err := gitOutput(ctx, checkout, "rev-parse", "--git-dir")
	if err != nil {
		return gitTopologyNode{}, err
	}

	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(checkout, gitDir)
	}

	commonDir, _ := gitOutput(ctx, checkout, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(checkout, commonDir)
	}

	commonDir, _ = filepath.Abs(commonDir)

	branch, _ := gitOutput(ctx, checkout, "rev-parse", "--abbrev-ref", "HEAD")
	upstream, _ := gitOutput(ctx, checkout, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	ahead, behind := gitAheadBehind(ctx, checkout)
	dirty := gitDirtyCountsFrom(ctx, checkout)

	tier := gitTierAdvisory

	for _, m := range profile.StrictMarkers {
		if _, err := os.Stat(filepath.Join(checkout, m)); err == nil {
			tier = gitTierStrict
			break
		}
	}

	hooksPath, _ := gitOutput(ctx, checkout, "config", "core.hooksPath")
	hooksInstalled := listInstalledHooks(checkout, hooksPath)

	var violations []gitViolation

	required := profile.RequiredHooks[gitTierAdvisory]
	if tier == gitTierStrict {
		required = profile.RequiredHooks[gitTierStrict]
	}

	for _, h := range required {
		if !containsString(hooksInstalled, h) {
			violations = append(violations, gitViolation{
				Code:     "hooks_drift",
				Severity: gitSeverityWarn,
				Message:  fmt.Sprintf("%s: missing hook %s (hooksPath=%q)", checkout, h, hooksPath),
			})
		}
	}

	if tier == gitTierStrict && hooksPath != "" && hooksPath != "dev/hooks" && !strings.HasSuffix(filepath.ToSlash(hooksPath), "dev/hooks") {
		violations = append(violations, gitViolation{
			Code:     "hooks_drift",
			Severity: gitSeverityWarn,
			Message:  fmt.Sprintf("%s: core.hooksPath=%q expected dev/hooks for strict tier", checkout, hooksPath),
		})
	}

	wtRoot := profile.WorktreeRoot
	if wtRoot != "" && strings.HasPrefix(checkout, wtRoot) {
		if branch != "main" && !strings.HasPrefix(branch, "feat/") {
			violations = append(violations, gitViolation{
				Code:     "branch_policy",
				Severity: "info",
				Message:  fmt.Sprintf("%s: task worktree on branch %q", checkout, branch),
			})
		}
	}

	isWT := commonDir != gitDir && !strings.HasSuffix(gitDir, string(filepath.Separator)+".git")

	node := gitTopologyNode{
		Path:           checkout,
		GitDir:         gitDir,
		CommonDir:      commonDir,
		IsWorktree:     isWT,
		Branch:         branch,
		Upstream:       upstream,
		Ahead:          ahead,
		Behind:         behind,
		Dirty:          dirty,
		HooksPath:      hooksPath,
		HooksInstalled: hooksInstalled,
		RepoInfraTier:  tier,
		RepoName:       filepath.Base(checkout),
		Violations:     violations,
		Reachability: gitReachability{
			CredentialHelper: gitConfig(ctx, checkout, "credential.helper"),
		},
	}

	return node, nil
}

func summarizeNodes(nodes []gitTopologyNode) gitAuditSummary {
	s := gitAuditSummary{TotalRepos: len(nodes), TotalWorktrees: len(nodes)}

	commonSet := map[string]int{}

	for i := range nodes {
		n := nodes[i]

		commonSet[n.CommonDir]++
		if n.RepoInfraTier == gitTierStrict {
			s.StrictRepos++
		} else {
			s.AdvisoryRepos++
		}

		for _, v := range n.Violations {
			switch v.Severity {
			case gitSeverityError:
				s.Errors++
			case gitSeverityWarn:
				s.Warnings++
			default:
				s.Info++
			}
		}
	}

	return s
}

func applyGitFixes(ctx context.Context, report *gitAuditReport, profile *localSetupProfile) error {
	commonDirs := map[string]string{}

	for i := range report.Repos {
		n := report.Repos[i]
		commonDirs[n.CommonDir] = n.Path
	}

	for common := range commonDirs {
		main := commonDirs[common]
		fmt.Printf("→ git worktree prune in %s\n", main)
		_ = exec.CommandContext(ctx, "git", "-C", main, "worktree", "prune").Run()
	}
	// Remove empty orphan dirs under worktree root
	if profile.WorktreeRoot != "" {
		entries, _ := os.ReadDir(profile.WorktreeRoot)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}

			p := filepath.Join(profile.WorktreeRoot, e.Name())

			entries2, _ := os.ReadDir(p)
			if len(entries2) == 0 {
				fmt.Printf("→ removing empty orphan worktree dir %s\n", p)
				_ = os.Remove(p)
			}
		}
	}
	// Re-install hooks where drift detected
	for i := range report.Repos {
		n := report.Repos[i]
		for _, v := range n.Violations {
			if v.Code != "hooks_drift" {
				continue
			}

			installPath := filepath.Join(n.Path, "dev", "hooks", "install.sh")
			if _, err := os.Stat(installPath); err != nil {
				continue
			}

			fmt.Printf("→ re-running %s\n", installPath)
			c := exec.CommandContext(ctx, installPath)
			c.Dir = n.Path
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			_ = c.Run()
		}
	}

	return nil
}

func writeAuditReportFile(report *gitAuditReport) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, ".lightwave", "reports")
	if err := os.MkdirAll(dir, gitDirPerm); err != nil {
		return err
	}

	ts := strings.ReplaceAll(report.ScanTimeISO, ":", "-")
	path := filepath.Join(dir, "git-audit-"+ts+".json")

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, gitFilePerm)
}

func printGitAuditTTY(report *gitAuditReport) {
	fmt.Printf("%s git audit — %d checkout(s), %d strict, %d advisory\n",
		color.CyanString("●"),
		report.Summary.TotalRepos,
		report.Summary.StrictRepos,
		report.Summary.AdvisoryRepos,
	)
	fmt.Printf("  errors=%d warnings=%d info=%d\n",
		report.Summary.Errors, report.Summary.Warnings, report.Summary.Info)

	for i := range report.Repos {
		n := report.Repos[i]
		if len(n.Violations) == 0 {
			continue
		}

		fmt.Printf("\n%s\n", n.Path)

		for _, v := range n.Violations {
			fmt.Printf("  %s [%s] %s\n", severityColor(v.Severity), v.Code, v.Message)
		}
	}
}

func filterNodesByRepo(nodes []gitTopologyNode, repo string) []gitTopologyNode {
	repo, _ = filepath.Abs(repo)

	var out []gitTopologyNode

	for i := range nodes {
		n := nodes[i]
		if n.Path == repo || strings.HasPrefix(n.Path, repo+string(filepath.Separator)) {
			out = append(out, n)
		}
	}

	return out
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = dir
	out, err := c.Output()

	return strings.TrimSpace(string(out)), err
}

func gitAheadBehind(ctx context.Context, dir string) (ahead, behind int) {
	out, err := gitOutput(ctx, dir, "rev-list", "--left-right", "--count", "@{u}...HEAD")
	if err != nil {
		return 0, 0
	}

	parts := strings.Fields(out)
	if len(parts) != gitStatusFieldWidth {
		return 0, 0
	}

	_, _ = fmt.Sscanf(parts[0], "%d", &behind)
	_, _ = fmt.Sscanf(parts[1], "%d", &ahead)

	return ahead, behind
}

func gitDirtyCountsFrom(ctx context.Context, dir string) gitDirtyCounts {
	out, _ := gitOutput(ctx, dir, "status", "--porcelain=v1")

	var d gitDirtyCounts

	for _, line := range strings.Split(out, "\n") {
		if len(line) < gitStatusFieldWidth {
			continue
		}

		x, y := line[0], line[1]
		switch {
		case x == '?' && y == '?':
			d.Untracked++
		case x != ' ' && x != '?':
			d.Staged++
		case y != ' ':
			d.Unstaged++
		}
	}

	return d
}

func gitConfig(ctx context.Context, dir, key string) string {
	v, _ := gitOutput(ctx, dir, "config", key)
	return v
}

func listInstalledHooks(checkout, hooksPath string) []string {
	var base string
	if hooksPath != "" {
		base = filepath.Join(checkout, hooksPath)
	} else {
		base = filepath.Join(checkout, ".git", "hooks")
	}

	var names []string
	for _, h := range []string{"pre-commit", "pre-push", "commit-msg"} {
		p := filepath.Join(base, h)

		info, err := os.Stat(p)
		if err == nil && info.Mode().IsRegular() {
			names = append(names, h)
		}
	}

	return names
}

func containsString(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}

	return false
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}

	return h
}

func severityColor(sev string) string {
	switch sev {
	case gitSeverityError:
		return color.RedString("ERROR")
	case gitSeverityWarn:
		return color.YellowString("WARN")
	default:
		return color.BlueString("INFO")
	}
}

func flagString(flags map[string]any, name string) string {
	if v, ok := flags[name]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}

	return ""
}
