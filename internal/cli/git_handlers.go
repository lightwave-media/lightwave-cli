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
	"regexp"
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

	// worktree_policy.yaml v2.0.0 + worktree_home_policy.yaml v2.0.0
	// (CORE-0051, operator ruling 2026-08-31): repo_structure invariants.
	worktreeMarkerFile = ".lw-worktree.yaml"
	worktreeMaxAge     = 72 * time.Hour

	// canonicalWorktreeHome is worktree_home_policy.canonical_root. It sits
	// outside every repo on purpose: a Stop-hook `git add -A` can no longer
	// stage a worktree as a gitlink. Layout underneath is {repo}/{slug}.
	canonicalWorktreeHome = ".worktrees"

	// harnessWorktreeDir left forbidden_roots in v2.0.0 and is now a
	// supplemental root: a tree there is CONFORMING when it carries the
	// project_workspace record and nonconforming when it does not — never
	// invisible, and never relocated.
	harnessWorktreeDir = ".claude/worktrees"

	// legacyRepoWorktreeDir was the v1.x canonical root. v2.0.0 puts it in
	// both legacy_roots and forbidden_roots: no NEW allocation here. Existing
	// trees register in place and drain; they are never moved.
	legacyRepoWorktreeDir = ".worktrees"

	// adoptCommand is the remediation the stamp names for an unregistered
	// tree. The previous string named `lw git worktree migrate`, which has
	// never been implemented — the advice pointed at nothing.
	adoptCommand = "lw workspace adopt"
)

// canonicalWorktreeSlugRe pins worktree_policy.repo_structure.naming, which
// v2.0.0 reduced to {slug} derived from the task's branch intent. The v1.x
// <YYYY-MM-DD>-<ticket-slug> form is no longer required.
var canonicalWorktreeSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

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
	RemoteProtection   []branchProtectionDiff `json:"remote_protection,omitempty"`
	RecommendedActions []gitRecommendedAction `json:"recommended_actions"`
	Summary            gitAuditSummary        `json:"summary"`
}

type branchProtectionDiff struct {
	Repo       string   `json:"repo"`
	Status     string   `json:"status"`
	Detail     string   `json:"detail,omitempty"`
	Expected   []string `json:"expected"`
	Actual     []string `json:"actual"`
	Missing    []string `json:"missing,omitempty"`
	Unexpected []string `json:"unexpected,omitempty"`
}

type branchProtectionStamp struct {
	Example struct {
		Organization string `yaml:"organization"`
		Repos        []struct {
			Repo           string   `yaml:"repo"`
			RequiredChecks []string `yaml:"required_checks"`
		} `yaml:"repos"`
	} `yaml:"example"`
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

	if flagBool(flags, "remote") {
		report.RemoteProtection, err = auditRemoteBranchProtection(ctx, hooksWorkspaceRoot())
		if err != nil {
			return err
		}

		for index := range report.RemoteProtection {
			if report.RemoteProtection[index].Status != "match" {
				report.Summary.Warnings++
			}
		}
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

		if err := enc.Encode(report); err != nil {
			return err
		}

		return gitAuditResultError(report.Summary.Errors)
	}

	printGitAuditTTY(report)

	return gitAuditResultError(report.Summary.Errors)
}

func gitAuditResultError(errorCount int) error {
	if errorCount == 0 {
		return nil
	}

	return fmt.Errorf(
		"%d error-level git violation(s); restore the declared topology and rerun "+
			"`lw git audit`; do not bypass or weaken the audit",
		errorCount,
	)
}

func auditRemoteBranchProtection(ctx context.Context, workspaceRoot string) ([]branchProtectionDiff, error) {
	path := filepath.Join(
		workspaceRoot,
		"lightwave-core",
		"src",
		"schemas",
		"policy",
		"governance",
		"branch_protection.yaml",
	)

	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read branch-protection stamp: %w", err)
	}

	var stamp branchProtectionStamp
	if err := yaml.Unmarshal(body, &stamp); err != nil {
		return nil, fmt.Errorf("parse branch-protection stamp: %w", err)
	}

	if stamp.Example.Organization == "" {
		return nil, errors.New("branch-protection stamp has no example.organization")
	}

	results := make([]branchProtectionDiff, 0, len(stamp.Example.Repos))
	for _, repo := range stamp.Example.Repos {
		actual, fetchErr := fetchRequiredChecks(ctx, stamp.Example.Organization, repo.Repo)
		if fetchErr != nil {
			if strings.Contains(fetchErr.Error(), "HTTP 404") {
				results = append(results, compareRequiredChecks(repo.Repo, repo.RequiredChecks, nil))
			} else {
				results = append(results, branchProtectionDiff{
					Repo:     repo.Repo,
					Expected: sortedUnique(repo.RequiredChecks),
					Status:   "unverified",
					Detail: "Could not verify live required checks: " + fetchErr.Error() +
						". Restore authentication and rerun; do not assume an unverified boundary is healthy.",
				})
			}

			continue
		}

		results = append(results, compareRequiredChecks(repo.Repo, repo.RequiredChecks, actual))
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Repo < results[j].Repo })

	return results, nil
}

func fetchRequiredChecks(ctx context.Context, organization, repo string) ([]string, error) {
	endpoint := fmt.Sprintf(
		"repos/%s/%s/branches/main/protection/required_status_checks",
		organization,
		repo,
	)
	command := exec.CommandContext(ctx, "gh", "api", endpoint)

	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gh api %s: %s", endpoint, strings.TrimSpace(string(output)))
	}

	var response struct {
		Contexts []string `json:"contexts"`
		Checks   []struct {
			Context string `json:"context"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("decode required checks for %s: %w", repo, err)
	}

	actual := append([]string(nil), response.Contexts...)
	for _, check := range response.Checks {
		actual = append(actual, check.Context)
	}

	return sortedUnique(actual), nil
}

func compareRequiredChecks(repo string, expected, actual []string) branchProtectionDiff {
	expected = sortedUnique(expected)
	actual = sortedUnique(actual)

	result := branchProtectionDiff{
		Repo:     repo,
		Expected: expected,
		Actual:   actual,
		Status:   "match",
	}

	if len(expected) == 0 && len(actual) == 0 {
		result.Status = "gap"
		result.Detail = "Protection has no required checks, so CI can report and block nothing. " +
			"Add one always-reporting aggregate check; do not treat empty agreement as healthy."

		return result
	}

	expectedSet := make(map[string]bool, len(expected))
	for _, check := range expected {
		expectedSet[check] = true
		if !containsString(actual, check) {
			result.Missing = append(result.Missing, check)
		}
	}

	for _, check := range actual {
		if !expectedSet[check] {
			result.Unexpected = append(result.Unexpected, check)
		}
	}

	if len(result.Missing) > 0 || len(result.Unexpected) > 0 {
		result.Status = "drift"
		result.Detail = "Live required checks differ from the stamp. Repair branch protection or " +
			"the always-reporting aggregate workflow; do not remove the requirement to make the audit green."
	}

	return result
}

func sortedUnique(values []string) []string {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		seen[value] = true
	}

	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}

	sort.Strings(result)

	return result
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

		if err := enc.Encode(node); err != nil {
			return err
		}

		return gitDoctorResultError(repo, len(doctorViols))
	}

	if len(doctorViols) == 0 {
		fmt.Println(color.GreenString("✓ git doctor: hooks and profile OK for " + repo))
		return nil
	}

	for _, v := range doctorViols {
		fmt.Printf("  %s %s\n", severityColor(v.Severity), v.Message)
	}

	return gitDoctorResultError(repo, len(doctorViols))
}

func gitDoctorResultError(repo string, issueCount int) error {
	if issueCount == 0 {
		return nil
	}

	return fmt.Errorf(
		"git doctor: %d issue(s) in %s; repair hooks/profile drift, then rerun; "+
			"do not set a blanket bypass",
		issueCount,
		repo,
	)
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
		// worktree_home_policy v2.0.0 (CORE-0051): the canonical root is
		// ~/.worktrees, with {repo}/{slug} underneath. It sits outside every
		// repo deliberately, so a Stop-hook `git add -A` cannot stage a
		// worktree as a gitlink. The v1.x repo-relative ".worktrees" is now a
		// legacy AND forbidden root; existing trees there drain in place.
		WorktreeRoot: filepath.Join(home, canonicalWorktreeHome),
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

// canonicalWorktreeRoot resolves the profile's worktree_root for one repo.
// Relative values (the stamped default ".worktrees") resolve against the
// repo's main checkout; absolute values are honored as a single legacy
// global root so pre-v1.1.0 prints keep working.
func canonicalWorktreeRoot(profile *localSetupProfile, mainCheckout string) string {
	root := profile.WorktreeRoot

	// A repo-relative root IS the v1.x <repo>/.worktrees layout, which v2.0.0
	// moved into legacy_roots and forbidden_roots. Honouring it here would let
	// a profile written before the 2026-08-31 ruling demote the stamped
	// canonical root, and the checker would then report a correctly-placed
	// tree as a violation. The stamp outranks a stale print, so anything that
	// is not an explicit absolute override resolves to canonical_root.
	if !filepath.IsAbs(root) {
		home, err := os.UserHomeDir()
		if err != nil {
			// No home means no canonical root to compare against. Fall back to
			// the legacy in-repo root so classification still resolves to
			// something bounded rather than matching every path.
			return filepath.Join(mainCheckout, legacyRepoWorktreeDir)
		}

		root = filepath.Join(home, canonicalWorktreeHome)
	}

	return filepath.Join(root, filepath.Base(mainCheckout))
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
			// Resolve symlinks for the dedup key: git reports resolved paths
			// (e.g. /private/var on macOS) while WalkDir sees the alias, and a
			// mismatched key double-counts every repo reachable both ways.
			if resolved, rerr := filepath.EvalSymlinks(commonDir); rerr == nil {
				commonDir = resolved
			}

			if seenCommon[commonDir] {
				return filepath.SkipDir
			}

			seenCommon[commonDir] = true

			wtOut, _ := gitOutput(ctx, checkout, "worktree", "list", "--porcelain")
			registered := map[string]bool{}

			var (
				wtPaths  []string
				prunable []string
			)

			for _, line := range strings.Split(wtOut, "\n") {
				switch {
				case strings.HasPrefix(line, "worktree "):
					p := strings.TrimPrefix(line, "worktree ")
					wtPaths = append(wtPaths, p)
					registered[p] = true
				case strings.HasPrefix(line, "prunable"):
					if len(wtPaths) > 0 {
						prunable = append(prunable, wtPaths[len(wtPaths)-1])
					}
				}
			}

			mainNodeIdx := -1

			for _, wtPath := range wtPaths {
				node, nerr := inspectGitCheckout(ctx, wtPath, profile)
				if nerr != nil {
					continue // prunable entries have no directory to inspect
				}

				nodes = append(nodes, node)
				if !node.IsWorktree && mainNodeIdx == -1 {
					mainNodeIdx = len(nodes) - 1
				}
			}

			if mainNodeIdx >= 0 {
				main := &nodes[mainNodeIdx]
				main.Violations = append(main.Violations,
					repoLevelWorktreeViolations(main.Path, registered, prunable, profile)...)
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

	isWT := commonDir != gitDir && !strings.HasSuffix(gitDir, string(filepath.Separator)+".git")

	if isWT {
		mainCheckout := filepath.Dir(commonDir)
		violations = append(violations, worktreePolicyViolations(checkout, mainCheckout, branch, profile)...)
	}

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

// worktreeRootClass is where a linked worktree lives relative to the roots
// declared in worktree_home_policy.yaml v2.0.0 (CORE-0051).
type worktreeRootClass string

const (
	// worktreeRootCanonical is ~/.worktrees/{repo} — the LightWave-managed root.
	worktreeRootCanonical worktreeRootClass = "canonical"
	// worktreeRootSupplemental is a vendor root from
	// workspace_broker_policy.root_taxonomy. Trees here are legitimate; the
	// project_workspace record, not the path, decides conformance.
	worktreeRootSupplemental worktreeRootClass = "supplemental"
	// worktreeRootLegacy is the v1.x <repo>/.worktrees root: no new
	// allocation, existing trees drain in place.
	worktreeRootLegacy worktreeRootClass = "legacy"
	// worktreeRootUndeclared is a root no policy names at all.
	worktreeRootUndeclared worktreeRootClass = "undeclared"
)

func isUnder(path, root string) bool {
	return root != "" && strings.HasPrefix(path, root+string(filepath.Separator))
}

// classifyWorktreeRoot resolves a checkout to the root that governs it.
// Order matters: the canonical root is checked first so that a repo whose
// override happens to alias a vendor path is still read as canonical.
func classifyWorktreeRoot(checkout, mainCheckout string, profile *localSetupProfile) worktreeRootClass {
	if isUnder(checkout, canonicalWorktreeRoot(profile, mainCheckout)) {
		return worktreeRootCanonical
	}

	if isUnder(checkout, filepath.Join(mainCheckout, harnessWorktreeDir)) {
		return worktreeRootSupplemental
	}

	for _, vendorRoot := range profile.CursorWorktreeRoots {
		if isUnder(checkout, vendorRoot) {
			return worktreeRootSupplemental
		}
	}

	if isUnder(checkout, filepath.Join(mainCheckout, legacyRepoWorktreeDir)) {
		return worktreeRootLegacy
	}

	return worktreeRootUndeclared
}

// worktreePolicyViolations checks one linked worktree against
// worktree_policy.yaml v2.0.0 + worktree_home_policy.yaml v2.0.0 (CORE-0051):
// root taxonomy, {slug} naming, the .lw-worktree.yaml claim marker, and the
// 72h expiry.
//
// v2.0.0 inverted two of the v1.1.0 rules this function used to encode.
// .claude/worktrees left forbidden_roots and became a supplemental root, so a
// registered tree there is conforming. The old canonical <repo>/.worktrees
// joined legacy_roots and forbidden_roots, so it is no longer somewhere to
// send anyone. Under v1.1.0 rules this reported the current stamp backwards.
func worktreePolicyViolations(checkout, mainCheckout, branch string, profile *localSetupProfile) []gitViolation {
	var violations []gitViolation

	canonicalRoot := canonicalWorktreeRoot(profile, mainCheckout)

	switch classifyWorktreeRoot(checkout, mainCheckout, profile) {
	case worktreeRootCanonical:
		if !canonicalWorktreeSlugRe.MatchString(filepath.Base(checkout)) {
			violations = append(violations, gitViolation{
				Code:     "naming_violation",
				Severity: gitSeverityWarn,
				Message:  fmt.Sprintf("%s: name %q is not a {slug} (lowercase, digits, dashes)", checkout, filepath.Base(checkout)),
			})
		}

	case worktreeRootSupplemental:
		// No root violation by design. A vendor tree is conforming when it
		// carries the record; the marker check below is the whole test.

	case worktreeRootLegacy:
		violations = append(violations, gitViolation{
			Code:     "legacy_worktree_root",
			Severity: gitSeverityWarn,
			Message: fmt.Sprintf("%s: legacy root %s takes no new allocation — canonical is %s (CORE-0051). Register in place with %s and let it drain; do not move it.",
				checkout, legacyRepoWorktreeDir, canonicalRoot, adoptCommand),
		})

	case worktreeRootUndeclared:
		violations = append(violations, gitViolation{
			Code:     "undeclared_worktree_root",
			Severity: gitSeverityWarn,
			Message: fmt.Sprintf("%s: root is named by no policy — canonical is %s, vendor roots come from workspace_broker_policy.root_taxonomy (CORE-0051)",
				checkout, canonicalRoot),
		})
	}

	if _, err := os.Stat(filepath.Join(checkout, worktreeMarkerFile)); err != nil {
		violations = append(violations, gitViolation{
			Code:     "missing_marker",
			Severity: gitSeverityWarn,
			Message: fmt.Sprintf("%s: missing %s — the tree carries no project_workspace record, so it has no owner, task or heartbeat. Register it with %s.",
				checkout, worktreeMarkerFile, adoptCommand),
		})
	}

	if info, err := os.Stat(checkout); err == nil {
		if age := time.Since(info.ModTime()); age > worktreeMaxAge {
			violations = append(violations, gitViolation{
				Code:     "expired_worktree",
				Severity: gitSeverityWarn,
				Message: fmt.Sprintf("%s: age %s exceeds max_age_hours=72 (branch %q) — reap via save_gate, never plain remove",
					checkout, age.Round(time.Hour), branch),
			})
		}
	}

	return violations
}

// repoLevelWorktreeViolations reports problems visible only at the repo level:
// registry entries whose directory is gone (prunable) and directories under
// the canonical root that git does not recognize as worktrees (orphan gitdir —
// e.g. a cloud-session .git file pointing at a sandbox path).
func repoLevelWorktreeViolations(mainCheckout string, registered map[string]bool, prunable []string, profile *localSetupProfile) []gitViolation {
	violations := make([]gitViolation, 0, len(prunable))

	for _, p := range prunable {
		violations = append(violations, gitViolation{
			Code:     "prunable_registry_entry",
			Severity: gitSeverityWarn,
			Message:  fmt.Sprintf("%s: registered worktree %s no longer exists — run git worktree prune from main", mainCheckout, p),
		})
	}

	seen := map[string]bool{}

	for _, root := range worktreeScanRoots(profile, mainCheckout) {
		if seen[root] {
			continue
		}

		seen[root] = true

		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}

			p := filepath.Join(root, e.Name())
			if registered[p] {
				continue
			}

			violations = append(violations, gitViolation{
				Code:     "orphan_gitdir",
				Severity: gitSeverityWarn,
				Message:  fmt.Sprintf("%s: directory under %s is not a registered worktree — unresolvable gitdir? archive contents, then remove", p, root),
			})
		}
	}

	return violations
}

// worktreeScanRoots lists every directory that may legitimately hold a
// worktree for this repo. Scanning only the canonical root would hide orphans
// in the legacy and vendor roots, and v2.0.0 is explicit that a tree outside
// the canonical root is "never invisible" — it drains in place, so it has to
// stay observable while it does.
func worktreeScanRoots(profile *localSetupProfile, mainCheckout string) []string {
	const namedRoots = 3

	roots := make([]string, 0, namedRoots+len(profile.CursorWorktreeRoots))
	roots = append(roots,
		canonicalWorktreeRoot(profile, mainCheckout),
		filepath.Join(mainCheckout, legacyRepoWorktreeDir),
		filepath.Join(mainCheckout, harnessWorktreeDir),
	)

	return append(roots, profile.CursorWorktreeRoots...)
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

		// Remove empty orphan dirs under this repo's canonical worktree root
		root := canonicalWorktreeRoot(profile, main)

		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}

			p := filepath.Join(root, e.Name())

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

	if len(report.RemoteProtection) > 0 {
		fmt.Println("\nremote branch protection (advisory):")

		for index := range report.RemoteProtection {
			result := &report.RemoteProtection[index]

			marker := color.GreenString("✓")
			if result.Status != "match" {
				marker = color.YellowString("⚠")
			}

			fmt.Printf("  %s %s status=%s missing=%v unexpected=%v\n",
				marker, result.Repo, result.Status, result.Missing, result.Unexpected)

			if result.Detail != "" {
				fmt.Printf("      %s\n", result.Detail)
			}
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

// resolveHooksDir resolves core.hooksPath against a checkout the way git does.
//
// git honours an ABSOLUTE core.hooksPath as-is. Joining it onto the checkout
// (as this used to do unconditionally) yields a nonsense path like
// <worktree>/Users/joel/dev/<repo>/dev/hooks, so every hook stats as missing.
// That is why `lw git doctor` reported "missing hook" for every worktree whose
// repo sets an absolute hooksPath, even though the hooks demonstrably ran —
// and why worktree sessions were pushed toward a blanket LW_SKIP_GIT_DOCTOR=1
// bypass instead of a real fix (lightwave-cli#300).
func resolveHooksDir(checkout, hooksPath string) string {
	switch {
	case hooksPath == "":
		return filepath.Join(checkout, ".git", "hooks")
	case filepath.IsAbs(hooksPath):
		return hooksPath
	default:
		return filepath.Join(checkout, hooksPath)
	}
}

func listInstalledHooks(checkout, hooksPath string) []string {
	base := resolveHooksDir(checkout, hooksPath)

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
