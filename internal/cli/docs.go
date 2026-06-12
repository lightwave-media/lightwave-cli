package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/docsfactory"
	"github.com/lightwave-media/lightwave-cli/internal/version"
	"github.com/spf13/cobra"
)

// `lw docs` — the spec/+docs/ factory front door.
//
// Three verbs:
//
//   - `lw docs spec-lint` — validate <repo>/spec/ against
//     spec_artifact_kinds.yaml (frontmatter + sections per kind).
//   - `lw docs check`     — validate <repo>/docs/ against
//     doc_artifact_kinds.yaml + repo_doc_manifest.yaml (presence +
//     shape + freshness vs HEAD).
//   - `lw docs sync`      — refresh source_commit / generated_at
//     frontmatter on generated docs, idempotently. v1 does NOT regenerate
//     bodies from refresh_source — that's a future pass. Refreshing the
//     header makes `lw docs check`'s drift signal actionable today.
//
// `lw scaffold spec-repo` + `lw scaffold docs-repo` are the bootstrap
// path; they already work via internal/blueprint/ — these verbs operate
// on the rendered tree.
//
// Trust policy: registered in VerifiedCommands once the tests in
// internal/docsfactory/*_test.go pass in CI. See docs/command-status.md
// for the per-verb verification record.

var (
	docsRepoFlag   string
	docsAllFlag    bool
	docsDryRunFlag bool
	docsJSONFlag   bool
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Documentation factory — spec/ + docs/ across every LightWave repo",
	Long: `The lw docs subtree drives the spec/ + docs/ factory:

  spec/  — aspirational. PRDs, ADRs, designs, plans. Shape-linted only.
  docs/  — descriptive. Architecture, contract, dep-graph, runbook.
           Generated kinds drift-checked against the source commit.

Schemas:
  policy/governance/spec_artifact_kinds.yaml — spec shape contract
  policy/governance/doc_artifact_kinds.yaml  — docs shape + refresh sources
  policy/governance/repo_doc_manifest.yaml   — per-tier required kinds`,
}

var docsSpecLintCmd = &cobra.Command{
	Use:   "spec-lint",
	Short: "Validate <repo>/spec/ against spec_artifact_kinds.yaml",
	Long: `Walk <repo>/spec/ and validate every .md file against its kind's contract:
extension, frontmatter required keys, status enum, required level-2
headings. Kind discovery: frontmatter 'kind:' first, then parent
directory name as fallback.

Exit codes:
  0  clean
  1  one or more violations
  2  tool error (config / schema load failure)

Examples:
  lw docs spec-lint            # current repo
  lw docs spec-lint --repo /path/to/other/repo`,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo := resolveDocsRepo()
		schemas, err := loadDocsSchemas()
		if err != nil {
			return toolError(err)
		}
		res, err := docsfactory.LintSpec(repo, schemas)
		if err != nil {
			return toolError(err)
		}
		return reportSpecLint(repo, res)
	},
}

var docsCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Validate <repo>/docs/ against repo_doc_manifest + doc_artifact_kinds",
	Long: `Compute drift for <repo>/docs/:
  - Required kinds missing (per repo_doc_manifest.yaml + .lwdocs.yaml override)
  - Generated files whose source_commit differs from current HEAD
  - Authored files violating their kind's shape contract

Exit codes:
  0  clean
  1  drift detected
  2  tool error

Examples:
  lw docs check
  lw docs check --repo /path/to/repo
  lw docs check --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo := resolveDocsRepo()
		schemas, err := loadDocsSchemas()
		if err != nil {
			return toolError(err)
		}
		res, err := docsfactory.CheckDocs(repo, schemas)
		if err != nil {
			return toolError(err)
		}
		return reportDocsCheck(repo, res)
	},
}

var docsSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Refresh source_commit + generated_at headers on docs/ generated kinds",
	Long: `Refresh the source_commit, generator_version, and generated_at headers
on every generated doc in <repo>/docs/, then exit. v1 does NOT regenerate
bodies from refresh_source — that's a planned follow-up. Updating the
header anchors the determinism contract: 'lw docs check' fails when
source_commit < HEAD, and 'lw docs sync' is how you cure it.

Idempotent: a second run with no commits between is a no-op.

Exit codes:
  0  clean (nothing written, or written successfully)
  1  reserved (no current case; --check delegate to docs check)
  2  tool error`,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo := resolveDocsRepo()
		schemas, err := loadDocsSchemas()
		if err != nil {
			return toolError(err)
		}
		res, err := docsfactory.SyncDocs(repo, schemas, docsfactory.SyncOptions{
			GeneratorVersion: version.Version,
			DryRun:           docsDryRunFlag,
		})
		if err != nil {
			return toolError(err)
		}
		return reportDocsSync(repo, res, docsDryRunFlag)
	},
}

func init() {
	docsCmd.PersistentFlags().StringVar(&docsRepoFlag, "repo", "", "repo root (default: cwd)")
	docsCheckCmd.Flags().BoolVar(&docsAllFlag, "all", false, "reserved — currently always full repo")
	docsCheckCmd.Flags().BoolVar(&docsJSONFlag, "json", false, "JSON output")
	docsSyncCmd.Flags().BoolVar(&docsDryRunFlag, "dry-run", false, "preview without writing")
	docsCmd.AddCommand(docsSpecLintCmd)
	docsCmd.AddCommand(docsCheckCmd)
	docsCmd.AddCommand(docsSyncCmd)
	rootCmd.AddCommand(docsCmd)
}

// resolveDocsRepo honors --repo, else cwd. We do not fall back to
// config.LightwaveRoot — the docs factory is per-repo, not monorepo-wide.
func resolveDocsRepo() string {
	if docsRepoFlag != "" {
		abs, err := filepath.Abs(docsRepoFlag)
		if err == nil {
			return abs
		}
		return docsRepoFlag
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// loadDocsSchemas reads the three governance YAMLs from a checked-out
// lightwave-core. Honors LW_LIGHTWAVE_CORE env var first.
func loadDocsSchemas() (*docsfactory.Schemas, error) {
	cfg := config.Get()
	root := ""
	if cfg != nil {
		root = cfg.Paths.LightwaveRoot
	}
	return docsfactory.LoadSchemas(root)
}

// toolError wraps a setup-stage error so cobra's RunE can flag "exit code 2"
// for the caller. We use a distinctive prefix the dispatcher / pre-commit
// hook can grep for.
func toolError(err error) error {
	return fmt.Errorf("docs: tool error: %w", err)
}

func reportSpecLint(repo string, res *docsfactory.SpecLintResult) error {
	rel := func(p string) string {
		return filepath.Join("spec", p)
	}
	fmt.Printf("%s %d files, %d clean\n", color.CyanString("spec-lint:"), res.Total, res.Clean)
	if len(res.Violations) == 0 {
		fmt.Println(color.GreenString("✓ clean"))
		return nil
	}
	sort.Slice(res.Violations, func(i, j int) bool {
		return res.Violations[i].Path < res.Violations[j].Path
	})
	fmt.Printf("\n%s %d violation(s):\n", color.RedString("✗"), len(res.Violations))
	for _, v := range res.Violations {
		fmt.Printf("  %s  (%s)  %s\n", rel(v.Path), v.Kind, v.Reason)
	}
	return fmt.Errorf("%d violation(s) in %s", len(res.Violations), filepath.Join(repo, "spec"))
}

func reportDocsCheck(repo string, res *docsfactory.DocsCheckResult) error {
	fmt.Printf("%s tier=%s head=%s\n", color.CyanString("docs-check:"), res.Tier, res.HeadCommit)
	if res.Clean() {
		fmt.Println(color.GreenString("✓ no drift"))
		return nil
	}
	if len(res.MissingRequired) > 0 {
		fmt.Printf("\n%s required kinds missing (%d):\n",
			color.RedString("✗"), len(res.MissingRequired))
		for _, k := range res.MissingRequired {
			fmt.Printf("  - %s\n", k)
		}
	}
	if len(res.StaleByCommit) > 0 {
		fmt.Printf("\n%s stale by commit (%d):\n",
			color.YellowString("✗"), len(res.StaleByCommit))
		for _, e := range res.StaleByCommit {
			fmt.Printf("  - %s (%s): %s → %s\n",
				e.Path, e.Kind, e.SourceCommit, e.CurrentCommit)
		}
	}
	if len(res.StaleByAge) > 0 {
		fmt.Printf("\n%s stale by age (%d):\n",
			color.YellowString("✗"), len(res.StaleByAge))
		for _, e := range res.StaleByAge {
			fmt.Printf("  - %s (%s): %d days\n", e.Path, e.Kind, e.AgeDays)
		}
	}
	if len(res.ShapeViolations) > 0 {
		fmt.Printf("\n%s shape violations (%d):\n",
			color.RedString("✗"), len(res.ShapeViolations))
		for _, v := range res.ShapeViolations {
			fmt.Printf("  - %s (%s): %s\n", v.Path, v.Kind, v.Reason)
		}
	}
	fmt.Printf("\nCure: `lw docs sync` (refreshes source_commit), then commit. " +
		"For missing kinds: `lw scaffold docs-repo` or write the file.\n")
	return fmt.Errorf("docs drift detected in %s", filepath.Join(repo, "docs"))
}

func reportDocsSync(repo string, res *docsfactory.SyncResult, dryRun bool) error {
	tag := "docs-sync:"
	if dryRun {
		tag = "docs-sync (dry-run):"
	}
	fmt.Printf("%s head=%s\n", color.CyanString(tag), res.HeadCommit)
	if len(res.Updated) > 0 {
		label := color.GreenString("updated")
		if dryRun {
			label = color.YellowString("would update")
		}
		fmt.Printf("\n%s %d file(s):\n", label, len(res.Updated))
		for _, p := range res.Updated {
			fmt.Printf("  - %s\n", p)
		}
	}
	if len(res.Skipped) > 0 {
		fmt.Printf("\nskipped (already at HEAD): %d\n", len(res.Skipped))
	}
	if len(res.Authored) > 0 {
		fmt.Printf("authored (not generated): %d\n", len(res.Authored))
	}
	if len(res.Ignored) > 0 {
		fmt.Printf("ignored (per .lwdocs.yaml ignore_globs): %d\n", len(res.Ignored))
	}
	if len(res.Updated) == 0 && len(res.Skipped) == 0 && len(res.Authored) == 0 && len(res.Ignored) == 0 {
		fmt.Println(color.YellowString("docs/ is empty — run `lw scaffold docs-repo` first"))
	}
	if len(res.Updated) == 0 && !dryRun {
		fmt.Println(color.GreenString("✓ no changes — all generated kinds already at HEAD"))
	}
	_ = repo
	return nil
}
