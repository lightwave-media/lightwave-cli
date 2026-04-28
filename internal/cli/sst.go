package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// =============================================================================
// Types
// =============================================================================

type sstFileCoverage struct {
	RelPath   string   `json:"rel_path"`
	Status    string   `json:"status"`
	HasStatus bool     `json:"has_status"`
	Consumers []string `json:"consumers"`
	Domain    string   `json:"domain"`
}

// =============================================================================
// Flags
// =============================================================================

var (
	sstCoverageOrphans     bool
	sstCoverageCheckStatus bool
	sstCoverageByDomain    bool
	sstCoverageJSON        bool
	sstCoverageAutoPromote bool
	sstCoverageApply       bool
)

// =============================================================================
// Commands
// =============================================================================

var sstCmd = &cobra.Command{
	Use:   "sst",
	Short: "Single Source of Truth (SST) brain YAML tools",
	Long: `Tools for managing the brain's YAML definitions and their lifecycle status.

The SST system tracks _meta.status on every brain YAML file:
  enforced    — has a live consumer; visible to agents by default
  aspirational — draft, no consumer yet; hidden from agents
  orphan      — had a consumer, doesn't now; never injected

Examples:
  lw sst coverage               # Full coverage report
  lw sst coverage --orphans     # Only files with zero consumers
  lw sst coverage --check-status # Exit non-zero if any file lacks _meta.status`,
}

var sstCoverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "Report _meta.status coverage across brain YAML files",
	Long: `Walk ~/.brain/**/*.yaml and report lifecycle status coverage.

Checks each file for _meta.status and detects known consumers via
grep heuristics (build_prompt.py, gates.yaml, lw codegen, etc.).

Status values:
  enforced    — confirmed consumer exists; injected by default
  aspirational — no consumer detected (default when field is absent)
  orphan      — had a consumer, doesn't now

Examples:
  lw sst coverage                     # Full report
  lw sst coverage --orphans           # Only files with zero consumers
  lw sst coverage --check-status      # Exit non-zero if any file lacks _status
  lw sst coverage --by-domain         # Group output by directory domain
  lw sst coverage --json              # Machine-readable JSON
  lw sst coverage --auto-promote      # Show diff: files that should be enforced
  lw sst coverage --auto-promote --apply  # Write enforced status to files`,
	RunE: runSSTCoverage,
}

func init() {
	sstCoverageCmd.Flags().BoolVar(&sstCoverageOrphans, "orphans", false, "only show files with zero consumers")
	sstCoverageCmd.Flags().BoolVar(&sstCoverageCheckStatus, "check-status", false, "exit non-zero if any YAML lacks _meta.status")
	sstCoverageCmd.Flags().BoolVar(&sstCoverageByDomain, "by-domain", false, "group output by domain directory")
	sstCoverageCmd.Flags().BoolVar(&sstCoverageJSON, "json", false, "output as JSON")
	sstCoverageCmd.Flags().BoolVar(&sstCoverageAutoPromote, "auto-promote", false, "propose _status: enforced for files with detected consumers")
	sstCoverageCmd.Flags().BoolVar(&sstCoverageApply, "apply", false, "write auto-promote changes to files (requires --auto-promote)")

	sstCmd.AddCommand(sstCoverageCmd)
}

// =============================================================================
// Runner
// =============================================================================

func runSSTCoverage(cmd *cobra.Command, args []string) error {
	brainDir, err := resolveBrainDir()
	if err != nil {
		return err
	}

	files, err := walkBrainYAMLs(brainDir)
	if err != nil {
		return fmt.Errorf("walking brain dir: %w", err)
	}

	for i := range files {
		files[i].Consumers = detectSSTConsumers(brainDir, filepath.Join(brainDir, files[i].RelPath))
	}

	if sstCoverageCheckStatus {
		return runCheckStatus(files)
	}

	if sstCoverageAutoPromote {
		return runAutoPromote(brainDir, files)
	}

	display := files
	if sstCoverageOrphans {
		display = filterSSTOrphans(files)
	}

	if sstCoverageJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(display)
	}

	if sstCoverageByDomain {
		printSSTByDomain(display)
	} else {
		printSSTTable(display)
	}

	printSSTSummary(files)
	return nil
}

// =============================================================================
// Brain dir resolution
// =============================================================================

func resolveBrainDir() (string, error) {
	// Honour BRAIN env var like build_prompt.py does
	if b := os.Getenv("BRAIN"); b != "" {
		return b, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir: %w", err)
	}
	return filepath.Join(home, ".brain"), nil
}

// =============================================================================
// YAML walker
// =============================================================================

func walkBrainYAMLs(brainDir string) ([]sstFileCoverage, error) {
	var files []sstFileCoverage

	err := filepath.Walk(brainDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable paths
		}
		if info.IsDir() {
			// Skip hidden dirs and common non-spec dirs
			base := info.Name()
			if base == "__pycache__" || base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		rel, _ := filepath.Rel(brainDir, path)
		cov := parseSSTFileCoverage(path, rel)
		files = append(files, cov)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].RelPath < files[j].RelPath
	})
	return files, nil
}

func parseSSTFileCoverage(absPath, relPath string) sstFileCoverage {
	cov := sstFileCoverage{
		RelPath:   relPath,
		Status:    "aspirational",
		HasStatus: false,
		Domain:    topLevelDir(relPath),
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return cov
	}

	var doc map[string]interface{}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return cov
	}

	meta, _ := doc["_meta"].(map[string]interface{})
	if meta == nil {
		return cov
	}

	if statusRaw, ok := meta["status"]; ok {
		if s, ok := statusRaw.(string); ok && s != "" {
			cov.Status = s
			cov.HasStatus = true
		}
	}

	return cov
}

func topLevelDir(relPath string) string {
	parts := strings.SplitN(relPath, string(filepath.Separator), 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// =============================================================================
// Consumer detection
// =============================================================================

// detectSSTConsumers runs grep heuristics to find known consumers of a brain YAML file.
// Returns a slice of short consumer-type labels (e.g., "prompt_builder", "ci_rule").
func detectSSTConsumers(brainDir, absPath string) []string {
	var consumers []string
	rel, _ := filepath.Rel(brainDir, absPath)

	// Heuristic 1: prompt_builder — is the relative path referenced in build_prompt.py?
	buildPrompt := filepath.Join(brainDir, "tools", "build_prompt.py")
	if fileContains(buildPrompt, rel) || fileContains(buildPrompt, filepath.Base(absPath)) {
		consumers = append(consumers, "prompt_builder")
	}

	// Heuristic 2: ci_rule — referenced in governance/audit/gates.yaml?
	gatesYAML := filepath.Join(brainDir, "governance", "audit", "gates.yaml")
	if fileContains(gatesYAML, rel) || fileContains(gatesYAML, filepath.Base(absPath)) {
		consumers = append(consumers, "ci_rule")
	}

	// Heuristic 3: skills/ directory — YAML files in skills/ are directly invocable sessions.
	if strings.HasPrefix(rel, "skills"+string(filepath.Separator)) {
		consumers = append(consumers, "skill_ref")
	}

	// Heuristic 4: soul/ files are always consumed by build_prompt.py soul layer.
	if strings.HasPrefix(rel, "soul"+string(filepath.Separator)) {
		if !containsStr(consumers, "prompt_builder") {
			consumers = append(consumers, "prompt_builder")
		}
	}

	// Heuristic 5: cortex/ session YAMLs — consumed whenever that session is spawned.
	if strings.HasPrefix(rel, "cortex"+string(filepath.Separator)) && !strings.HasSuffix(rel, "__index.yaml") {
		if !containsStr(consumers, "prompt_builder") {
			consumers = append(consumers, "prompt_builder")
		}
	}

	return consumers
}

// fileContains returns true if the file at path contains needle as a substring on any line.
func fileContains(path, needle string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), needle) {
			return true
		}
	}
	return false
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// =============================================================================
// --check-status
// =============================================================================

func runCheckStatus(files []sstFileCoverage) error {
	var missing []string
	for _, f := range files {
		if !f.HasStatus {
			missing = append(missing, f.RelPath)
		}
	}
	if len(missing) == 0 {
		fmt.Printf("%s All %d brain YAML files have _meta.status\n",
			color.GreenString("✓"), len(files))
		return nil
	}
	fmt.Fprintf(os.Stderr, "%s %d files missing _meta.status:\n",
		color.RedString("✗"), len(missing))
	for _, p := range missing {
		fmt.Fprintf(os.Stderr, "  %s\n", p)
	}
	return fmt.Errorf("status coverage incomplete: %d/%d files missing _meta.status",
		len(missing), len(files))
}

// =============================================================================
// --auto-promote
// =============================================================================

// runAutoPromote proposes (or applies) _meta.status: enforced for files with detected consumers.
func runAutoPromote(brainDir string, files []sstFileCoverage) error {
	type proposal struct {
		rel       string
		absPath   string
		consumers []string
	}

	var proposals []proposal
	for _, f := range files {
		if len(f.Consumers) > 0 && f.Status != "enforced" && f.Status != "orphan" {
			proposals = append(proposals, proposal{
				rel:       f.RelPath,
				absPath:   filepath.Join(brainDir, f.RelPath),
				consumers: f.Consumers,
			})
		}
	}

	if len(proposals) == 0 {
		fmt.Println("No promotion candidates found (all consumer files already have enforced or orphan status).")
		return nil
	}

	fmt.Printf("%s %d files proposed for promotion to enforced:\n\n",
		color.CyanString("→"), len(proposals))

	for _, p := range proposals {
		fmt.Printf("  %s\n    consumers: %s\n",
			color.YellowString(p.rel),
			strings.Join(p.consumers, ", "))
	}

	if !sstCoverageApply {
		fmt.Printf("\n%s Run with --apply to write changes.\n",
			color.CyanString("ℹ"))
		return nil
	}

	// Apply: write _meta.status: enforced into each file
	applied := 0
	for _, p := range proposals {
		if err := injectStatusEnforced(p.absPath); err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s: %v\n", color.RedString("✗"), p.rel, err)
		} else {
			fmt.Printf("  %s %s\n", color.GreenString("✓"), p.rel)
			applied++
		}
	}

	fmt.Printf("\n%d/%d files updated.\n", applied, len(proposals))
	return nil
}

// injectStatusEnforced adds or updates _meta.status: enforced in a YAML file.
func injectStatusEnforced(absPath string) error {
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}

	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		root := doc.Content[0]
		if root.Kind == yaml.MappingNode {
			if err := setYAMLMetaStatus(root, "enforced"); err != nil {
				return err
			}
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("encode YAML: %w", err)
	}

	return os.WriteFile(absPath, buf.Bytes(), 0644)
}

// setYAMLMetaStatus inserts or updates _meta.status in a mapping YAML node.
func setYAMLMetaStatus(root *yaml.Node, status string) error {
	// Find _meta key in root mapping
	for i := 0; i+1 < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		valNode := root.Content[i+1]

		if keyNode.Value == "_meta" && valNode.Kind == yaml.MappingNode {
			// Found _meta — look for existing status key
			for j := 0; j+1 < len(valNode.Content); j += 2 {
				if valNode.Content[j].Value == "status" {
					valNode.Content[j+1].Value = status
					return nil
				}
			}
			// No status key — append it
			valNode.Content = append(valNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "status"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: status},
			)
			return nil
		}
	}

	// No _meta key at all — insert at top of mapping
	metaVal := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "status"},
			{Kind: yaml.ScalarNode, Value: status},
		},
	}
	// Prepend _meta: {status: enforced}
	metaKey := &yaml.Node{Kind: yaml.ScalarNode, Value: "_meta"}
	root.Content = append([]*yaml.Node{metaKey, metaVal}, root.Content...)
	return nil
}

// =============================================================================
// Filters
// =============================================================================

func filterSSTOrphans(files []sstFileCoverage) []sstFileCoverage {
	var out []sstFileCoverage
	for _, f := range files {
		if len(f.Consumers) == 0 {
			out = append(out, f)
		}
	}
	return out
}

// =============================================================================
// Output: table
// =============================================================================

func printSSTTable(files []sstFileCoverage) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"File", "Status", "Has Status", "Consumers"})
	table.SetAutoWrapText(false)
	table.SetBorder(false)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)

	for _, f := range files {
		hasStr := color.RedString("✗")
		if f.HasStatus {
			hasStr = color.GreenString("✓")
		}

		statusStr := f.Status
		switch f.Status {
		case "enforced":
			statusStr = color.GreenString(f.Status)
		case "aspirational":
			statusStr = color.YellowString(f.Status)
		case "orphan":
			statusStr = color.RedString(f.Status)
		}

		table.Append([]string{
			f.RelPath,
			statusStr,
			hasStr,
			strings.Join(f.Consumers, ", "),
		})
	}

	table.Render()
}

// =============================================================================
// Output: by-domain
// =============================================================================

func printSSTByDomain(files []sstFileCoverage) {
	domains := map[string][]sstFileCoverage{}
	for _, f := range files {
		domains[f.Domain] = append(domains[f.Domain], f)
	}

	keys := make([]string, 0, len(domains))
	for k := range domains {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, domain := range keys {
		fmt.Printf("\n%s\n", color.CyanString("── "+domain+" ──"))
		domainFiles := domains[domain]

		enforced, aspirational, orphan := 0, 0, 0
		for _, f := range domainFiles {
			switch f.Status {
			case "enforced":
				enforced++
			case "orphan":
				orphan++
			default:
				aspirational++
			}
		}
		fmt.Printf("  %s enforced  %s aspirational  %s orphan\n\n",
			color.GreenString(fmt.Sprintf("%d", enforced)),
			color.YellowString(fmt.Sprintf("%d", aspirational)),
			color.RedString(fmt.Sprintf("%d", orphan)),
		)

		for _, f := range domainFiles {
			statusIcon := "○"
			switch f.Status {
			case "enforced":
				statusIcon = color.GreenString("●")
			case "orphan":
				statusIcon = color.RedString("◆")
			default:
				statusIcon = color.YellowString("○")
			}
			consumerStr := ""
			if len(f.Consumers) > 0 {
				consumerStr = " [" + strings.Join(f.Consumers, ",") + "]"
			}
			fmt.Printf("  %s %s%s\n", statusIcon, f.RelPath, consumerStr)
		}
	}
}

// =============================================================================
// Output: summary
// =============================================================================

func printSSTSummary(files []sstFileCoverage) {
	total := len(files)
	enforced, aspirational, orphan, missing := 0, 0, 0, 0
	for _, f := range files {
		switch f.Status {
		case "enforced":
			enforced++
		case "orphan":
			orphan++
		default:
			aspirational++
		}
		if !f.HasStatus {
			missing++
		}
	}

	fmt.Printf("\n%s\n", color.CyanString("─── Summary ───"))
	fmt.Printf("  Total files:   %d\n", total)
	fmt.Printf("  %s enforced\n", color.GreenString(fmt.Sprintf("%d", enforced)))
	fmt.Printf("  %s aspirational\n", color.YellowString(fmt.Sprintf("%d", aspirational)))
	fmt.Printf("  %s orphan\n", color.RedString(fmt.Sprintf("%d", orphan)))
	if missing > 0 {
		fmt.Printf("  %s missing _meta.status (run --check-status for details)\n",
			color.RedString(fmt.Sprintf("%d", missing)))
	}
}
