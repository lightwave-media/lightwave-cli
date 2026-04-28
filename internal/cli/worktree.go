package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/git"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// =============================================================================
// Types
// =============================================================================

type worktreeMeta struct {
	Issue     string `yaml:"issue"`
	Branch    string `yaml:"branch"`
	CreatedAt string `yaml:"created_at"`
	CreatedBy string `yaml:"created_by"`
	State     string `yaml:"state"`
}

type worktreeInfo struct {
	Path      string `json:"path"`
	Issue     string `json:"issue"`
	Branch    string `json:"branch"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
	ActStatus string `json:"act_status,omitempty"`
	IdleDays  int    `json:"idle_days"`
	modTime   time.Time
}

// =============================================================================
// Flags
// =============================================================================

var (
	worktreeBranch      string
	worktreeType        string
	worktreeDescription string
	worktreeJSON        bool
	worktreeStale       bool
	worktreeIssue       string
	worktreeCurrent     bool
	worktreeCheckAct    bool
	worktreeForce       bool
	worktreeDryRun      bool
	worktreeIdleDays    int
)

// branchPattern matches valid branch names from governance/naming/conventions.yaml:
//
//	feature/{taskId}-{description}
//	fix/{taskId}-{description}
//	hotfix/v{semver}-{description}
var branchPattern = regexp.MustCompile(`^(feature|fix)/[a-z0-9]+-[a-z0-9][a-z0-9-]*$|^hotfix/v[0-9]+\.[0-9]+\.[0-9]+-[a-z0-9][a-z0-9-]*$`)

// =============================================================================
// Helpers
// =============================================================================

func worktreeRoot() string {
	return filepath.Join(config.Get().Paths.LightwaveRoot, ".worktrees")
}

func worktreePath(issue string) string {
	return filepath.Join(worktreeRoot(), "issue-"+issue)
}

func metaPath(wpath string) string {
	return filepath.Join(wpath, ".lw-worktree.yaml")
}

func actStatusPath(wpath string) string {
	return filepath.Join(wpath, ".act-status")
}

func readMeta(wpath string) (*worktreeMeta, error) {
	data, err := os.ReadFile(metaPath(wpath))
	if err != nil {
		return nil, err
	}
	var m worktreeMeta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing .lw-worktree.yaml: %w", err)
	}
	return &m, nil
}

func writeMeta(wpath string, m *worktreeMeta) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath(wpath), data, 0644)
}

func readActStatus(wpath string) string {
	data, err := os.ReadFile(actStatusPath(wpath))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func validateBranch(branch string) error {
	if !branchPattern.MatchString(branch) {
		return fmt.Errorf("branch %q violates naming convention\n  expected: feature/<id>-<desc>, fix/<id>-<desc>, or hotfix/v<semver>-<desc>", branch)
	}
	return nil
}

func buildBranch(issue, branchType, description string) (string, error) {
	if branchType == "" {
		branchType = "feature"
	}
	if branchType == "hotfix" {
		return "", fmt.Errorf("hotfix branches require --branch with explicit semver: hotfix/v<semver>-<desc>")
	}
	if description == "" {
		return "", fmt.Errorf("--description is required when --branch is not set")
	}
	branch := fmt.Sprintf("%s/%s-%s", branchType, issue, description)
	if err := validateBranch(branch); err != nil {
		return "", err
	}
	return branch, nil
}

func loadAllWorktrees() ([]worktreeInfo, error) {
	root := worktreeRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var infos []worktreeInfo
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "issue-") {
			continue
		}
		wpath := filepath.Join(root, e.Name())
		meta, err := readMeta(wpath)
		if err != nil {
			continue
		}
		info, _ := e.Info()
		var modTime time.Time
		if info != nil {
			modTime = info.ModTime()
		}
		idle := int(time.Since(modTime).Hours() / 24)
		infos = append(infos, worktreeInfo{
			Path:      wpath,
			Issue:     meta.Issue,
			Branch:    meta.Branch,
			State:     meta.State,
			CreatedAt: meta.CreatedAt,
			ActStatus: readActStatus(wpath),
			IdleDays:  idle,
			modTime:   modTime,
		})
	}
	return infos, nil
}

// =============================================================================
// Commands
// =============================================================================

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Manage issue-scoped git worktrees",
	Long: `Create and manage git worktrees for Paperclip issue sessions.

Every agent session runs in an isolated worktree so it never operates on main.
The claude_local adapter calls these commands automatically on heartbeat start
and issue close.

Examples:
  lw worktree create 766 --type feature --description add-worktree-cli
  lw worktree create 766 --branch feature/liga766-add-worktree-cli
  lw worktree list
  lw worktree status --current
  lw worktree status --check-act
  lw worktree status --issue 766 --json
  lw worktree gc 766
  lw worktree prune --dry-run`,
}

// --- create ---

var worktreeCreateCmd = &cobra.Command{
	Use:   "create <issue>",
	Short: "Create an issue-scoped worktree (idempotent)",
	Long: `Create a git worktree at ~/.worktrees/issue-{n} for the given Paperclip issue.

Idempotent: if the worktree already exists and is active, exits 0 and reports the
existing path. The claude_local adapter calls this on every heartbeat.

Branch name: provide --branch, or --type + --description to auto-assemble one.
The branch must match governance/naming/conventions.yaml patterns:
  feature/<id>-<kebab-desc>
  fix/<id>-<kebab-desc>
  hotfix/v<semver>-<kebab-desc>

Exit codes:
  0 — created or already active
  2 — branch name violates naming convention
  3 — worktree exists for a different branch (single-owner invariant violated)

Examples:
  lw worktree create 766 --type feature --description add-worktree-cli
  lw worktree create 766 --branch feature/liga766-add-worktree-cli --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue := args[0]
		wpath := worktreePath(issue)

		// Check idempotent: already active?
		if meta, err := readMeta(wpath); err == nil {
			info := worktreeInfo{
				Path:   wpath,
				Issue:  meta.Issue,
				Branch: meta.Branch,
				State:  meta.State,
			}
			if worktreeJSON {
				return printWorktreeJSON(info, false)
			}
			color.Cyan("worktree already active: %s", wpath)
			fmt.Printf("  branch: %s\n", meta.Branch)
			fmt.Printf("  state:  %s\n", meta.State)
			return nil
		}

		// Resolve branch name
		branch := worktreeBranch
		if branch == "" {
			var err error
			branch, err = buildBranch(issue, worktreeType, worktreeDescription)
			if err != nil {
				os.Exit(2)
				return err
			}
		} else {
			if err := validateBranch(branch); err != nil {
				os.Exit(2)
				return err
			}
		}

		// Check if path exists with a different branch (exit 3)
		if _, err := os.Stat(wpath); err == nil {
			os.Exit(3)
			return fmt.Errorf("worktree path exists but no metadata found (manual interference?): %s", wpath)
		}

		// Create the worktree
		repoRoot := config.Get().Paths.LightwaveRoot
		g := git.NewGit(repoRoot)

		if err := g.WorktreeAddFromRef(wpath, branch, "origin/main"); err != nil {
			return fmt.Errorf("git worktree add: %w", err)
		}

		// Write metadata
		meta := &worktreeMeta{
			Issue:     issue,
			Branch:    branch,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			CreatedBy: os.Getenv("PAPERCLIP_AGENT_ID"),
			State:     "created",
		}
		if meta.CreatedBy == "" {
			meta.CreatedBy = "operator"
		}
		if err := writeMeta(wpath, meta); err != nil {
			return fmt.Errorf("writing metadata: %w", err)
		}

		info := worktreeInfo{
			Path:      wpath,
			Issue:     issue,
			Branch:    branch,
			State:     "created",
			CreatedAt: meta.CreatedAt,
		}
		if worktreeJSON {
			return printWorktreeJSON(info, true)
		}
		color.Green("✓ created worktree: %s", wpath)
		fmt.Printf("  branch: %s\n", branch)
		return nil
	},
}

// --- list ---

var worktreeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all issue-scoped worktrees",
	Long: `List all worktrees under ~/.worktrees/.

Examples:
  lw worktree list
  lw worktree list --json
  lw worktree list --stale
  lw worktree list --issue 766`,
	RunE: func(cmd *cobra.Command, args []string) error {
		infos, err := loadAllWorktrees()
		if err != nil {
			return err
		}

		// Apply filters
		var filtered []worktreeInfo
		for _, w := range infos {
			if worktreeIssue != "" && w.Issue != worktreeIssue {
				continue
			}
			if worktreeStale && w.IdleDays <= 7 {
				continue
			}
			filtered = append(filtered, w)
		}

		if worktreeJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(filtered)
		}

		if len(filtered) == 0 {
			fmt.Println("no worktrees found")
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Issue", "Branch", "State", "Act", "Idle"})
		table.SetBorder(false)
		for _, w := range filtered {
			act := w.ActStatus
			if act == "" {
				act = "-"
			}
			idle := fmt.Sprintf("%dd", w.IdleDays)
			table.Append([]string{w.Issue, w.Branch, w.State, act, idle})
		}
		table.Render()
		return nil
	},
}

// --- status ---

var worktreeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Query worktree state (gate-friendly)",
	Long: `Check worktree state for audit gates and adapter integration.

Modes:
  --current      Exit 0 if cwd is inside a .worktrees/issue-*/ directory
  --check-act    Exit 0 if .act-status is "passed" and less than 10 minutes old
  --issue <n>    Emit JSON describing the worktree's lifecycle state
  (no flag)      Same as --current

Examples:
  lw worktree status --current
  lw worktree status --check-act
  lw worktree status --issue 766 --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// --issue mode: emit full state
		if worktreeIssue != "" {
			wpath := worktreePath(worktreeIssue)
			meta, err := readMeta(wpath)
			if err != nil {
				return fmt.Errorf("no worktree for issue %s: %w", worktreeIssue, err)
			}
			info := worktreeInfo{
				Path:      wpath,
				Issue:     meta.Issue,
				Branch:    meta.Branch,
				State:     meta.State,
				CreatedAt: meta.CreatedAt,
				ActStatus: readActStatus(wpath),
			}
			if worktreeJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}
			fmt.Printf("issue:  %s\n", info.Issue)
			fmt.Printf("path:   %s\n", info.Path)
			fmt.Printf("branch: %s\n", info.Branch)
			fmt.Printf("state:  %s\n", info.State)
			fmt.Printf("act:    %s\n", info.ActStatus)
			return nil
		}

		// --check-act mode
		if worktreeCheckAct {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			actFile := actStatusPath(cwd)
			data, err := os.ReadFile(actFile)
			if err != nil {
				return fmt.Errorf("no .act-status file in cwd")
			}
			parts := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
			if len(parts) == 0 || parts[0] != "passed" {
				return fmt.Errorf("act-status is %q (need: passed)", parts[0])
			}
			// Check freshness (second line is RFC3339 timestamp if present)
			if len(parts) == 2 {
				ts, err := time.Parse(time.RFC3339, strings.TrimSpace(parts[1]))
				if err == nil && time.Since(ts) > 10*time.Minute {
					return fmt.Errorf("act-status passed but stale (%s ago)", time.Since(ts).Round(time.Second))
				}
			}
			if !worktreeJSON {
				fmt.Println("act: passed")
			}
			return nil
		}

		// --current mode (default)
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root := worktreeRoot()
		if !strings.HasPrefix(cwd, root) {
			return fmt.Errorf("not inside a worktree directory (cwd: %s)", cwd)
		}
		if !worktreeJSON {
			fmt.Printf("inside worktree: %s\n", cwd)
		}
		return nil
	},
}

// --- gc ---

var worktreeGcCmd = &cobra.Command{
	Use:   "gc <issue>",
	Short: "Remove a worktree after merge/close",
	Long: `Remove the worktree for the given issue and prune git metadata.

Called by the claude_local adapter on issue-close webhook. Also callable manually.

By default refuses to remove a dirty worktree. Use --force to override.

Examples:
  lw worktree gc 766
  lw worktree gc 766 --force`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issue := args[0]
		wpath := worktreePath(issue)

		if _, err := os.Stat(wpath); os.IsNotExist(err) {
			fmt.Printf("worktree for issue %s not found (already removed?)\n", issue)
			return nil
		}

		repoRoot := config.Get().Paths.LightwaveRoot
		g := git.NewGit(repoRoot)

		if err := g.WorktreeRemove(wpath, worktreeForce); err != nil {
			return fmt.Errorf("git worktree remove: %w\n  (use --force to remove dirty worktree)", err)
		}

		if err := g.WorktreePrune(); err != nil {
			// Non-fatal: prune failure doesn't invalidate the remove
			fmt.Fprintf(os.Stderr, "warning: git worktree prune: %v\n", err)
		}

		color.Green("✓ removed worktree for issue %s", issue)
		return nil
	},
}

// --- prune ---

var worktreePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Sweep stale worktrees (for cron / weekly cleanup)",
	Long: `Remove worktrees that have been idle longer than --idle-days (default 7).

Only removes worktrees whose issue is closed (state == "merged" or idle > threshold).
Use --dry-run to preview what would be removed.

Examples:
  lw worktree prune --dry-run
  lw worktree prune --idle-days 14`,
	RunE: func(cmd *cobra.Command, args []string) error {
		infos, err := loadAllWorktrees()
		if err != nil {
			return err
		}

		type pruneResult struct {
			Issue  string `json:"issue"`
			Branch string `json:"branch"`
			Path   string `json:"path"`
			Reason string `json:"reason"`
		}
		var results []pruneResult

		repoRoot := config.Get().Paths.LightwaveRoot
		g := git.NewGit(repoRoot)

		for _, w := range infos {
			if w.IdleDays <= worktreeIdleDays {
				continue
			}
			reason := fmt.Sprintf("idle %d days (threshold: %d)", w.IdleDays, worktreeIdleDays)
			results = append(results, pruneResult{
				Issue:  w.Issue,
				Branch: w.Branch,
				Path:   w.Path,
				Reason: reason,
			})
			if !worktreeDryRun {
				if err := g.WorktreeRemove(w.Path, false); err != nil {
					fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", w.Path, err)
					continue
				}
			}
		}

		if !worktreeDryRun && len(results) > 0 {
			_ = g.WorktreePrune()
		}

		if worktreeJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(results)
		}

		if len(results) == 0 {
			fmt.Println("no stale worktrees found")
			return nil
		}

		prefix := "removed"
		if worktreeDryRun {
			prefix = "[dry-run] would remove"
		}
		for _, r := range results {
			fmt.Printf("%s: issue-%s (%s)\n", prefix, r.Issue, r.Reason)
		}
		return nil
	},
}

// =============================================================================
// JSON output helper
// =============================================================================

func printWorktreeJSON(info worktreeInfo, created bool) error {
	out := map[string]any{
		"path":    info.Path,
		"issue":   info.Issue,
		"branch":  info.Branch,
		"state":   info.State,
		"created": created,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// =============================================================================
// init
// =============================================================================

func init() {
	// create flags
	worktreeCreateCmd.Flags().StringVar(&worktreeBranch, "branch", "", "explicit branch name (must match naming convention)")
	worktreeCreateCmd.Flags().StringVar(&worktreeType, "type", "feature", "branch type: feature|fix|hotfix")
	worktreeCreateCmd.Flags().StringVar(&worktreeDescription, "description", "", "kebab-case description for auto-assembled branch name")
	worktreeCreateCmd.Flags().BoolVar(&worktreeJSON, "json", false, "emit JSON (path, branch, created)")

	// list flags
	worktreeListCmd.Flags().BoolVar(&worktreeJSON, "json", false, "emit JSON array")
	worktreeListCmd.Flags().BoolVar(&worktreeStale, "stale", false, "show only worktrees idle >7 days")
	worktreeListCmd.Flags().StringVar(&worktreeIssue, "issue", "", "filter to a single issue number")

	// status flags
	worktreeStatusCmd.Flags().BoolVar(&worktreeCurrent, "current", false, "exit 0 if cwd is inside a worktree directory")
	worktreeStatusCmd.Flags().BoolVar(&worktreeCheckAct, "check-act", false, "exit 0 if .act-status is passed and <10m old")
	worktreeStatusCmd.Flags().StringVar(&worktreeIssue, "issue", "", "describe worktree state for this issue number")
	worktreeStatusCmd.Flags().BoolVar(&worktreeJSON, "json", false, "emit JSON")

	// gc flags
	worktreeGcCmd.Flags().BoolVar(&worktreeForce, "force", false, "remove even if worktree is dirty")

	// prune flags
	worktreePruneCmd.Flags().BoolVar(&worktreeDryRun, "dry-run", false, "show what would be removed without removing")
	worktreePruneCmd.Flags().IntVar(&worktreeIdleDays, "idle-days", 7, "remove worktrees idle longer than N days")
	worktreePruneCmd.Flags().BoolVar(&worktreeJSON, "json", false, "emit JSON report")

	worktreeCmd.AddCommand(worktreeCreateCmd)
	worktreeCmd.AddCommand(worktreeListCmd)
	worktreeCmd.AddCommand(worktreeStatusCmd)
	worktreeCmd.AddCommand(worktreeGcCmd)
	worktreeCmd.AddCommand(worktreePruneCmd)
}
