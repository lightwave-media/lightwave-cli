package cli

// check_repo_infra.go — lw check repo-infra
//
// linked-incident: failures/2026-06-15-repo-infra-drift.yaml
// linked-incident (ci-node): lightwave-media/lightwave-cli#122
//
// Anti-pattern caught:
//   BEFORE: agent sessions start without AGENTS.md/CLAUDE.md — no rules loaded,
//           no pre-push gate installed (missing dev/), context blind to repo conventions.
//   AFTER:  every session loads AGENTS.md via CLAUDE.md thin pointer; dev/hooks/
//           installs the pre-push gate; lw check repo-infra --all catches drift before it accumulates.
//
// Schema source: lightwave-core/src/schemas/policy/governance/repo-infra.yaml
// This handler reads the schema dynamically at each invocation — never hardcodes paths.

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
	"github.com/lightwave-media/lightwave-cli/internal/sst"
)

func init() {
	RegisterHandler("check.repo-infra", checkRepoInfraHandler)
}

const (
	claudeMaxLines    = 32
	driftThresholdDiv = 2
	driftThresholdMin = 2
)

// repoInfraExempt lists repos exempt from conformance checks.
var repoInfraExempt = map[string]string{
	"boilerplate": "external gruntwork.io fork",
	"runbooks":    "external gruntwork.io fork",
}

type repoInfraViolation struct {
	Repo     string `json:"repo"`
	RepoPath string `json:"-"`
	Kind     string `json:"kind"`
	Missing  string `json:"missing"`
	Detail   string `json:"detail,omitempty"`
	Severity string `json:"severity,omitempty"`
	Fixable  bool   `json:"fixable"`
}

type repoInfraDrift struct {
	Pattern  string `json:"pattern"`
	Evidence string `json:"evidence"`
	Repos    int    `json:"repos"`
}

type repoInfraReport struct {
	Root       string               `json:"root"`
	SchemaVer  string               `json:"schema_version"`
	Checked    []string             `json:"checked"`
	Exempt     []string             `json:"exempt"`
	Violations []repoInfraViolation `json:"violations"`
	Drift      []repoInfraDrift     `json:"drift,omitempty"`
	HardCount  int                  `json:"hard_violations"`
	LearnCount int                  `json:"learn_count,omitempty"`
}

func checkRepoInfraHandler(_ context.Context, args []string, flags map[string]any) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded (exit 2)")
	}

	root := cfg.Paths.LightwaveRoot

	// Load schema dynamically
	infraCfg, err := sst.LoadRepoInfra(root)
	if err != nil {
		return fmt.Errorf("load repo-infra schema: %w (exit 2)", err)
	}

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

	report := repoInfraReport{
		Root:      root,
		SchemaVer: infraCfg.Version,
	}

	for _, p := range repoPaths {
		name := filepath.Base(p)
		if reason, ok := repoInfraExempt[name]; ok {
			report.Exempt = append(report.Exempt, fmt.Sprintf("%s (%s)", name, reason))
			continue
		}

		report.Checked = append(report.Checked, name)
		report.Violations = append(report.Violations, checkOneRepo(p, name, infraCfg)...)
	}

	// Self-learning mode: detect patterns across repos and flag drift
	if flagBool(flags, "learn") && len(repoPaths) > 1 {
		report.Drift = detectDrift(repoPaths, infraCfg)
		report.LearnCount = len(report.Drift)
	}

	if flagBool(flags, "fix") {
		return applyRepoInfraFixes(report.Violations)
	}

	report.HardCount = countHardRepoInfraViolations(report.Violations)

	if asJSON(flags) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		if err := enc.Encode(report); err != nil {
			return err
		}

		return repoInfraResultError(report.HardCount, len(report.Checked))
	}

	printRepoInfraReport(&report)

	return repoInfraResultError(report.HardCount, len(report.Checked))
}

func countHardRepoInfraViolations(violations []repoInfraViolation) int {
	hardViolations := 0

	for _, v := range violations {
		if v.Severity != gitSeverityWarn {
			hardViolations++
		}
	}

	return hardViolations
}

func repoInfraResultError(hardViolations, checked int) error {
	if hardViolations == 0 {
		return nil
	}

	return fmt.Errorf(
		"%d violation(s) found in %d repo(s); restore the stamped structure and rerun "+
			"`lw check repo-infra`; do not remove, weaken, or bypass the check",
		hardViolations,
		checked,
	)
}

func checkOneRepo(repoPath, name string, infraCfg *sst.RepoInfraConfig) []repoInfraViolation {
	var viols []repoInfraViolation

	// Check each universal required file
	for _, f := range infraCfg.RequiredFiles {
		fpath := filepath.Join(repoPath, f.Path)

		fi, err := os.Stat(fpath)
		if os.IsNotExist(err) {
			v := repoInfraViolation{Repo: name, RepoPath: repoPath, Kind: "file", Missing: f.Path}
			if f.Path == "CLAUDE.md" {
				if _, aerr := os.Stat(filepath.Join(repoPath, "AGENTS.md")); aerr == nil {
					v.Fixable = true
				}
			}

			viols = append(viols, v)

			continue
		}

		if err != nil {
			viols = append(viols, repoInfraViolation{
				Repo: name, RepoPath: repoPath, Kind: "file", Missing: f.Path,
				Detail: fmt.Sprintf("stat error: %v", err),
			})

			continue
		}

		// Content validation
		if fi.Size() == 0 {
			viols = append(viols, repoInfraViolation{
				Repo: name, RepoPath: repoPath, Kind: "file", Missing: f.Path,
				Detail: "file is empty (0 bytes)",
			})

			continue
		}

		// CLAUDE.md must be a thin pointer (≤30 lines, references AGENTS.md)
		if f.Path == "CLAUDE.md" {
			content, rerr := os.ReadFile(fpath)
			if rerr == nil {
				lines := strings.Split(string(content), "\n")
				if len(lines) > claudeMaxLines {
					viols = append(viols, repoInfraViolation{
						Repo: name, RepoPath: repoPath, Kind: "file", Missing: f.Path,
						Detail: fmt.Sprintf("CLAUDE.md is %d lines (should be ≤30 — thin pointer only)", len(lines)),
					})
				}

				if !strings.Contains(string(content), "AGENTS.md") {
					viols = append(viols, repoInfraViolation{
						Repo: name, RepoPath: repoPath, Kind: "file", Missing: f.Path,
						Detail: "CLAUDE.md must reference AGENTS.md (thin pointer pattern)",
					})
				}
			}
		}

		// mise.toml must define a [tasks] section
		if f.Path == "mise.toml" {
			content, rerr := os.ReadFile(fpath)
			if rerr == nil {
				if !strings.Contains(string(content), "[tasks") {
					viols = append(viols, repoInfraViolation{
						Repo: name, RepoPath: repoPath, Kind: "file", Missing: f.Path,
						Detail: "mise.toml must define [tasks] section for ci/dispatch",
					})
				}
			}
		}
	}

	// Check each universal required dir
	for _, d := range infraCfg.RequiredDirs {
		dpath := filepath.Join(repoPath, d.Path)
		if fi, err := os.Stat(dpath); os.IsNotExist(err) {
			viols = append(viols, repoInfraViolation{
				Repo: name, RepoPath: repoPath, Kind: "dir", Missing: d.Path + "/",
			})
		} else if err != nil {
			viols = append(viols, repoInfraViolation{
				Repo: name, RepoPath: repoPath, Kind: "dir", Missing: d.Path + "/",
				Detail: fmt.Sprintf("stat error: %v", err),
			})
		} else if !fi.IsDir() {
			viols = append(viols, repoInfraViolation{
				Repo: name, RepoPath: repoPath, Kind: "dir", Missing: d.Path + "/",
				Detail: "path exists but is not a directory",
			})
		}
	}

	// ci-node conformance: warn if a Node/TS repo doesn't delegate to the shared workflow
	viols = append(viols, checkCINodeConformance(repoPath, name)...)
	viols = append(viols, checkCIParity(repoPath, name)...)

	return viols
}

func checkCIParity(repoPath, name string) []repoInfraViolation {
	workflowDir := filepath.Join(repoPath, ".github", "workflows")

	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		return []repoInfraViolation{{
			Repo:     name,
			RepoPath: repoPath,
			Kind:     "ci-parity",
			Missing:  ".github/workflows/",
			Detail: "No cloud workflow proves the local `mise run ci` contract. " +
				"Add an aggregate workflow that invokes it; do not bypass local CI.",
			Severity: gitSeverityWarn,
		}}
	}

	workflowNames := make([]string, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		extension := filepath.Ext(entry.Name())
		if extension != ".yml" && extension != ".yaml" {
			continue
		}

		workflowNames = append(workflowNames, entry.Name())

		body, readErr := os.ReadFile(filepath.Join(workflowDir, entry.Name()))
		if readErr != nil {
			continue
		}

		text := string(body)
		if strings.Contains(text, "mise run ci") ||
			strings.Contains(text, "lightwave-media/.github/.github/workflows/ci-") {
			return nil
		}
	}

	evidence := "No workflow invokes the canonical `mise run ci` graph"
	if len(workflowNames) > 0 {
		evidence += "; inspected " + strings.Join(workflowNames, ", ")
	}

	return []repoInfraViolation{{
		Repo:     name,
		RepoPath: repoPath,
		Kind:     "ci-parity",
		Missing:  ".github/workflows/ci.yml",
		Detail: evidence + ". Delegate to the local graph or document a runner-only exception; " +
			"do not duplicate and silently drift the checks.",
		Severity: gitSeverityWarn,
	}}
}

// checkCINodeConformance warns when a Node/TS repo hand-crafts inline CI steps
// instead of delegating to lightwave-media/.github/.github/workflows/ci-node.yml.
// Linked incident: lightwave-media/lightwave-cli#122
func checkCINodeConformance(repoPath, name string) []repoInfraViolation {
	// Only applies to repos with a Node/TS footprint.
	hasNode := false

	for _, indicator := range []string{"package.json", "tsconfig.json", ".nvmrc"} {
		if _, err := os.Stat(filepath.Join(repoPath, indicator)); err == nil {
			hasNode = true

			break
		}
	}

	if !hasNode {
		return nil
	}

	ciFile := filepath.Join(repoPath, ".github", "workflows", "ci.yml")

	data, err := os.ReadFile(ciFile)
	if err != nil {
		// No ci.yml — nothing to check.
		return nil
	}

	const sharedWorkflow = "lightwave-media/.github/.github/workflows/ci-node.yml"

	if strings.Contains(string(data), sharedWorkflow) {
		return nil
	}

	return []repoInfraViolation{{
		Repo:     name,
		RepoPath: repoPath,
		Kind:     "ci-node",
		Missing:  ".github/workflows/ci.yml",
		Detail: "Node/TS repo does not delegate to " + sharedWorkflow +
			" — replace inline steps. Fix hint: jobs.ci.uses: " + sharedWorkflow + "@<sha>",
		Severity: gitSeverityWarn,
	}}
}

// detectDrift scans all checked repos for patterns that exist but aren't in the schema.
// This is the self-learning loop: it finds common files/dirs/patterns across repos
// that the schema doesn't yet mandate.
func detectDrift(repoPaths []string, infraCfg *sst.RepoInfraConfig) []repoInfraDrift {
	var drift []repoInfraDrift

	knownFiles := infraCfg.UniversalFilePaths()
	knownDirs := infraCfg.UniversalDirPaths()

	// Count file/dir occurrences across all repos
	fileCount := map[string]int{}
	dirCount := map[string]int{}

	for _, rp := range repoPaths {
		entries, err := os.ReadDir(rp)
		if err != nil {
			continue
		}

		for _, e := range entries {
			if e.IsDir() {
				dirCount[e.Name()]++
			} else {
				fileCount[e.Name()]++
			}
		}
	}

	// Files that exist in >50% of repos but aren't in the schema
	threshold := len(repoPaths) / driftThresholdDiv
	if threshold < driftThresholdMin {
		threshold = driftThresholdMin
	}

	for name, count := range fileCount {
		if count < threshold {
			continue
		}

		known := false

		for _, kf := range knownFiles {
			if kf == name {
				known = true
				break
			}
		}

		if !known {
			drift = append(drift, repoInfraDrift{
				Pattern:  "file:" + name,
				Repos:    count,
				Evidence: fmt.Sprintf("present in %d/%d repos, not in repo-infra.yaml v%s", count, len(repoPaths), infraCfg.Version),
			})
		}
	}

	// Dirs that exist in >50% of repos but aren't in the schema
	for name, count := range dirCount {
		if count < threshold {
			continue
		}

		known := false

		for _, kd := range knownDirs {
			if kd == name {
				known = true
				break
			}
		}

		if !known {
			drift = append(drift, repoInfraDrift{
				Pattern:  "dir:" + name + "/",
				Repos:    count,
				Evidence: fmt.Sprintf("present in %d/%d repos, not in repo-infra.yaml v%s", count, len(repoPaths), infraCfg.Version),
			})
		}
	}

	return drift
}

func resolveRepoPath(root, nameOrPath string) string {
	if filepath.IsAbs(nameOrPath) {
		return nameOrPath
	}

	return filepath.Join(root, nameOrPath)
}

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
	fmt.Printf("%s %s (schema v%s)\n", color.CyanString("repo-infra check:"), r.Root, r.SchemaVer)
	fmt.Printf("  checked: %s\n", strings.Join(r.Checked, ", "))

	if len(r.Exempt) > 0 {
		fmt.Printf("  exempt:  %s\n", strings.Join(r.Exempt, ", "))
	}

	if len(r.Violations) == 0 && len(r.Drift) == 0 {
		fmt.Println(color.GreenString("\n✓ all repos conform"))
		return
	}

	var hardViols, warnViols []repoInfraViolation

	for _, v := range r.Violations {
		if v.Severity == gitSeverityWarn {
			warnViols = append(warnViols, v)
		} else {
			hardViols = append(hardViols, v)
		}
	}

	if len(hardViols) > 0 {
		fmt.Printf("\n%s (%d)\n", color.YellowString("violations"), len(hardViols))

		byRepo := map[string][]repoInfraViolation{}
		for _, v := range hardViols {
			byRepo[v.Repo] = append(byRepo[v.Repo], v)
		}

		for repo, viols := range byRepo {
			fmt.Printf("\n  %s\n", color.YellowString(repo))

			for _, v := range viols {
				fix := ""
				if v.Fixable {
					fix = color.CyanString("  [--fix available]")
				}

				d := ""
				if v.Detail != "" {
					d = " — " + v.Detail
				}

				fmt.Printf("    missing %s: %s%s%s\n", v.Kind, v.Missing, d, fix)
			}
		}

		fmt.Printf("\n%s\n", color.CyanString("Run `lw check repo-infra --fix` to apply mechanical fixes."))
	}

	if len(warnViols) > 0 {
		fmt.Printf("\n%s (%d)\n", color.YellowString("warnings (advisory)"), len(warnViols))

		for _, v := range warnViols {
			d := ""
			if v.Detail != "" {
				d = "\n      " + v.Detail
			}

			fmt.Printf("  ⚠  %s [%s]: %s%s\n", v.Repo, v.Kind, v.Missing, d)
		}
	}

	if len(r.Drift) > 0 {
		fmt.Printf("\n%s (%d)\n", color.MagentaString("drift (patterns not in schema)"), len(r.Drift))

		for _, d := range r.Drift {
			fmt.Printf("  %s — %s\n", d.Pattern, d.Evidence)
		}

		fmt.Printf("\n%s\n",
			color.MagentaString("Run `lw check repo-infra --learn` to regenerate drift analysis."))
		fmt.Printf("Review drift patterns and update repo-infra.yaml if they should be universal.\n")
	}
}
