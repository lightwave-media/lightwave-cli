package cli

// check_repo_infra.go — lw check repo-infra
//
// linked-incident: failures/2026-06-15-repo-infra-drift.yaml
//
// Anti-pattern caught:
//   BEFORE: agent sessions start without AGENTS.md/CLAUDE.md — no rules loaded,
//           no pre-push gate installed (missing dev/), context blind to repo conventions.
//   AFTER:  every session loads AGENTS.md via CLAUDE.md thin pointer; dev/hooks/
//           installs the pre-push gate; lw check repo-infra --all catches drift before it accumulates.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
)

func init() {
	RegisterHandler("check.repo-infra", checkRepoInfraHandler)
}

// repoInfraRequiredFiles mirrors the universal required_files from
// lightwave-core/src/schemas/policy/governance/repo-infra.yaml v1.2.0.
// Conditional entries (.pre-commit-config.yaml etc.) are omitted.
var repoInfraRequiredFiles = []string{
	"AGENTS.md",
	"CLAUDE.md",
	"README.md",
	"mise.toml",
	".gitignore",
}

var repoInfraRequiredDirs = []string{
	".github",
	"dev",
	"docs",
}

// repoInfraExempt lists repos exempt from conformance checks.
var repoInfraExempt = map[string]string{
	"boilerplate": "external gruntwork.io fork",
	"runbooks":    "external gruntwork.io fork",
}

type repoInfraViolation struct {
	Repo     string `json:"repo"`
	RepoPath string `json:"-"`    // absolute path, not serialised
	Kind     string `json:"kind"` // "file" | "dir"
	Missing  string `json:"missing"`
	Fixable  bool   `json:"fixable"`
}

type repoInfraReport struct {
	Root       string               `json:"root"`
	Checked    []string             `json:"checked"`
	Exempt     []string             `json:"exempt"`
	Violations []repoInfraViolation `json:"violations"`
}

func checkRepoInfraHandler(_ context.Context, args []string, flags map[string]any) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded (exit 2)")
	}

	root := cfg.Paths.LightwaveRoot

	var repoPaths []string

	switch {
	case flagBool(flags, "all"):
		discovered, err := discoverLightwaveRepos(root)
		if err != nil {
			return fmt.Errorf("discover repos: %w (exit 2)", err)
		}

		repoPaths = discovered
	case flagStr(flags, "repo") != "":
		repoPaths = []string{resolveRepoPath(root, flagStr(flags, "repo"))}
	default:
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w (exit 2)", err)
		}

		repoPaths = []string{cwd}
	}

	report := repoInfraReport{Root: root}

	for _, p := range repoPaths {
		name := filepath.Base(p)
		if reason, ok := repoInfraExempt[name]; ok {
			report.Exempt = append(report.Exempt, fmt.Sprintf("%s (%s)", name, reason))

			continue
		}

		report.Checked = append(report.Checked, name)
		report.Violations = append(report.Violations, checkOneRepo(p, name)...)
	}

	if flagBool(flags, "fix") {
		return applyRepoInfraFixes(report.Violations)
	}

	if asJSON(flags) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(report)
	}

	printRepoInfraReport(&report)

	if len(report.Violations) > 0 {
		return fmt.Errorf("%d violation(s) found in %d repo(s)", len(report.Violations), len(report.Checked))
	}

	return nil
}

func checkOneRepo(repoPath, name string) []repoInfraViolation {
	var viols []repoInfraViolation

	for _, f := range repoInfraRequiredFiles {
		if _, err := os.Stat(filepath.Join(repoPath, f)); os.IsNotExist(err) {
			v := repoInfraViolation{Repo: name, RepoPath: repoPath, Kind: "file", Missing: f}
			if f == "CLAUDE.md" {
				if _, aerr := os.Stat(filepath.Join(repoPath, "AGENTS.md")); aerr == nil {
					v.Fixable = true
				}
			}

			viols = append(viols, v)
		}
	}

	for _, d := range repoInfraRequiredDirs {
		if _, err := os.Stat(filepath.Join(repoPath, d)); os.IsNotExist(err) {
			viols = append(viols, repoInfraViolation{
				Repo:     name,
				RepoPath: repoPath,
				Kind:     "dir",
				Missing:  d + "/",
			})
		}
	}

	return viols
}

// resolveRepoPath returns an absolute path: absolute args used as-is, bare names joined with root.
func resolveRepoPath(root, nameOrPath string) string {
	if filepath.IsAbs(nameOrPath) {
		return nameOrPath
	}

	return filepath.Join(root, nameOrPath)
}

// discoverLightwaveRepos returns sibling dirs under root that contain a mise.toml.
func discoverLightwaveRepos(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var repos []string

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}

		p := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(p, "mise.toml")); err == nil {
			repos = append(repos, p)
		}
	}

	return repos, nil
}

// applyRepoInfraFixes applies mechanical fixes only (CLAUDE.md creation).
// Non-fixable violations are reported but not touched.
func applyRepoInfraFixes(viols []repoInfraViolation) error {
	var fixed, skipped int

	for _, v := range viols {
		if !v.Fixable {
			fmt.Printf("%s %s/%s — manual fix required\n",
				color.YellowString("SKIP"), v.Repo, v.Missing)

			skipped++

			continue
		}

		if v.Missing == "CLAUDE.md" {
			claudePath := filepath.Join(v.RepoPath, "CLAUDE.md")

			const filePerms = 0o644
			if err := os.WriteFile(claudePath, []byte("@AGENTS.md\n"), filePerms); err != nil {
				fmt.Printf("%s %s/CLAUDE.md: %v\n", color.RedString("ERROR"), v.Repo, err)

				continue
			}

			fmt.Printf("%s %s/CLAUDE.md created\n", color.GreenString("FIXED"), v.Repo)

			fixed++
		}
	}

	if skipped > 0 {
		fmt.Printf("\n%d violation(s) require manual intervention (dev/, .github/ scaffold)\n", skipped)
	}

	if fixed > 0 && skipped == 0 {
		return nil
	}

	if skipped > 0 {
		return fmt.Errorf("%d violation(s) not auto-fixed", skipped)
	}

	return nil
}

func printRepoInfraReport(r *repoInfraReport) {
	fmt.Printf("%s %s\n", color.CyanString("repo-infra check:"), r.Root)
	fmt.Printf("  checked: %s\n", strings.Join(r.Checked, ", "))

	if len(r.Exempt) > 0 {
		fmt.Printf("  exempt:  %s\n", strings.Join(r.Exempt, ", "))
	}

	if len(r.Violations) == 0 {
		fmt.Println(color.GreenString("\n✓ all repos conform to repo-infra.yaml v1.2.0"))

		return
	}

	fmt.Printf("\n%s (%d)\n",
		color.YellowString("violations (per repo-infra.yaml v1.2.0)"),
		len(r.Violations))

	byRepo := map[string][]repoInfraViolation{}

	for _, v := range r.Violations {
		byRepo[v.Repo] = append(byRepo[v.Repo], v)
	}

	for repo, viols := range byRepo {
		fmt.Printf("\n  %s\n", color.YellowString(repo))

		for _, v := range viols {
			fix := ""
			if v.Fixable {
				fix = color.CyanString("  [--fix available]")
			}

			fmt.Printf("    missing %s: %s%s\n", v.Kind, v.Missing, fix)
		}
	}

	fmt.Printf("\n%s\n", color.CyanString("Run `lw check repo-infra --fix` to apply mechanical fixes (CLAUDE.md only)."))
	fmt.Printf("See repo-infra.yaml v1.2.0 for dev/ and .github/ scaffold instructions.\n")
}
