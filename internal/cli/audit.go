package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// =============================================================================
// Types — match governance/security/audit_finding.yaml + audit_report.yaml
// =============================================================================

type AuditFinding struct {
	Source         string `json:"source" yaml:"source"`
	Severity       string `json:"severity" yaml:"severity"`
	Category       string `json:"category" yaml:"category"`
	File           string `json:"file,omitempty" yaml:"file,omitempty"`
	Line           int    `json:"line,omitempty" yaml:"line,omitempty"`
	Finding        string `json:"finding" yaml:"finding"`
	Detail         string `json:"detail,omitempty" yaml:"detail,omitempty"`
	Recommendation string `json:"recommendation" yaml:"recommendation"`
	Gateable       bool   `json:"gateable" yaml:"gateable"`
}

func (f AuditFinding) CompositeKey() string {
	return fmt.Sprintf("%s:%s:%d:%s", f.Source, f.File, f.Line, f.Category)
}

type ReportMeta struct {
	GeneratedAt     time.Time `json:"generated_at"`
	Scope           string    `json:"scope"`
	DurationSeconds int       `json:"duration_seconds"`
	GitCommit       string    `json:"git_commit"`
	GitBranch       string    `json:"git_branch"`
}

type SeverityCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
}

type ExecutiveSummary struct {
	HealthScore        int            `json:"health_score"`
	Status             string         `json:"status"`
	Narrative          string         `json:"narrative"`
	FindingsTotal      int            `json:"findings_total"`
	FindingsBySeverity SeverityCounts `json:"findings_by_severity"`
}

type SectionSummary struct {
	FilesScanned  int `json:"files_scanned"`
	FindingsCount int `json:"findings_count"`
}

type DriftSection struct {
	Missing int `json:"missing"`
	Drifted int `json:"drifted"`
	Orphans int `json:"orphans"`
}

type GatesSection struct {
	Implemented int      `json:"implemented"`
	Missing     int      `json:"missing"`
	CoveragePct int      `json:"coverage_pct"`
	Gaps        []string `json:"gaps"`
}

type ReportSections struct {
	Security *SectionSummary `json:"security,omitempty"`
	Drift    *DriftSection   `json:"drift,omitempty"`
	Gates    *GatesSection   `json:"gates,omitempty"`
	Quality  *SectionSummary `json:"quality,omitempty"`
}

type TrendComparison struct {
	PreviousDate     string `json:"previous_date"`
	PreviousScore    int    `json:"previous_score"`
	Delta            int    `json:"delta"`
	NewFindings      int    `json:"new_findings"`
	ResolvedFindings int    `json:"resolved_findings"`
}

type AuditReport struct {
	Meta             ReportMeta       `json:"meta"`
	ExecutiveSummary ExecutiveSummary `json:"executive_summary"`
	Sections         ReportSections   `json:"sections"`
	Findings         []AuditFinding   `json:"findings"`
	Recommendations  []string         `json:"recommendations"`
	Trend            *TrendComparison `json:"trend,omitempty"`
}

// =============================================================================
// Security Collector — native Go file scanner
// =============================================================================

type securityPattern struct {
	re             *regexp.Regexp
	severity       string
	category       string
	finding        string
	recommendation string
	fileMatch      func(string) bool
}

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "__pycache__": true,
	".venv": true, "venv": true, ".tox": true, "dist": true,
	"build": true, ".next": true, ".mypy_cache": true,
	".ruff_cache": true, ".pytest_cache": true, "htmlcov": true,
	"staticfiles": true, "media": true, "_archive": true,
}

func isPython(p string) bool  { return strings.HasSuffix(p, ".py") }
func isTS(p string) bool      { return strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".tsx") }
func isAnyCode(p string) bool { return isPython(p) || isTS(p) || strings.HasSuffix(p, ".go") }
func isSettings(p string) bool {
	return isPython(p) && (strings.Contains(p, "settings") || strings.Contains(p, "config"))
}
func notTestFile(p string) bool {
	base := filepath.Base(p)
	return !strings.HasPrefix(base, "test_") && !strings.HasSuffix(base, "_test.py") && !strings.HasSuffix(base, "_test.go") && !strings.HasSuffix(base, ".test.ts") && !strings.HasSuffix(base, ".test.tsx")
}

func initSecurityPatterns() []securityPattern {
	return []securityPattern{
		{
			re:             regexp.MustCompile(`(?i)DEBUG\s*=\s*True`),
			severity:       "medium",
			category:       "debug",
			finding:        "DEBUG mode enabled",
			recommendation: "Ensure DEBUG is False in production settings",
			fileMatch:      func(p string) bool { return isSettings(p) && notTestFile(p) },
		},
		{
			re:             regexp.MustCompile(`(?i)SESSION_COOKIE_SECURE\s*=\s*False`),
			severity:       "high",
			category:       "authentication",
			finding:        "Insecure session cookie",
			recommendation: "Set SESSION_COOKIE_SECURE = True in production",
			fileMatch:      isSettings,
		},
		{
			re:             regexp.MustCompile(`(?i)CSRF_COOKIE_SECURE\s*=\s*False`),
			severity:       "high",
			category:       "authentication",
			finding:        "Insecure CSRF cookie",
			recommendation: "Set CSRF_COOKIE_SECURE = True in production",
			fileMatch:      isSettings,
		},
		{
			re:             regexp.MustCompile(`(?i)SECURE_SSL_REDIRECT\s*=\s*False`),
			severity:       "high",
			category:       "authentication",
			finding:        "SSL redirect disabled",
			recommendation: "Set SECURE_SSL_REDIRECT = True in production",
			fileMatch:      isSettings,
		},
		{
			re:             regexp.MustCompile(`(?i)(password|passwd|secret_key|api_key|token)\s*=\s*["'][^"']{4,}["']`),
			severity:       "critical",
			category:       "credentials",
			finding:        "Possible hardcoded credential",
			recommendation: "Use SSM Parameter Store — never hardcode secrets",
			fileMatch:      func(p string) bool { return isAnyCode(p) && notTestFile(p) },
		},
		{
			re:             regexp.MustCompile(`(?i)ALLOWED_HOSTS\s*=\s*\[\s*["']\*["']\s*\]`),
			severity:       "high",
			category:       "authentication",
			finding:        "ALLOWED_HOSTS accepts all domains",
			recommendation: "Restrict ALLOWED_HOSTS to known domains",
			fileMatch:      isSettings,
		},
		{
			re:             regexp.MustCompile(`(?i)\.objects\.all\(\)`),
			severity:       "medium",
			category:       "authorization",
			finding:        "Unscoped .objects.all() query — potential tenant data leak",
			recommendation: "Verify tenant-scoping via schema_context() or use tenant-aware manager",
			fileMatch: func(p string) bool {
				return isPython(p) && notTestFile(p) && !strings.Contains(p, "migration")
			},
		},
		{
			re:             regexp.MustCompile(`\beval\s*\(`),
			severity:       "high",
			category:       "injection",
			finding:        "eval() call — code injection risk",
			recommendation: "Replace eval() with safe alternative",
			fileMatch:      func(p string) bool { return isAnyCode(p) && notTestFile(p) },
		},
		{
			re:             regexp.MustCompile(`os\.system\s*\(`),
			severity:       "high",
			category:       "injection",
			finding:        "os.system() call — command injection risk",
			recommendation: "Use subprocess.run() with shell=False",
			fileMatch:      func(p string) bool { return isPython(p) && notTestFile(p) },
		},
		{
			re:             regexp.MustCompile(`(?i)execute\s*\(\s*f["']|execute\s*\(\s*["'].*%s`),
			severity:       "critical",
			category:       "injection",
			finding:        "SQL string interpolation — injection risk",
			recommendation: "Use parameterized queries",
			fileMatch:      func(p string) bool { return isPython(p) && notTestFile(p) },
		},
	}
}

func collectSecurity(rootDir string) ([]AuditFinding, *SectionSummary, error) {
	patterns := initSecurityPatterns()
	var findings []AuditFinding
	filesScanned := 0

	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !isAnyCode(path) {
			return nil
		}
		filesScanned++

		relPath, _ := filepath.Rel(rootDir, path)
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			for _, p := range patterns {
				if p.fileMatch != nil && !p.fileMatch(path) {
					continue
				}
				if p.re.MatchString(line) {
					findings = append(findings, AuditFinding{
						Source:         "security",
						Severity:       p.severity,
						Category:       p.category,
						File:           relPath,
						Line:           lineNum,
						Finding:        p.finding,
						Detail:         strings.TrimSpace(line),
						Recommendation: p.recommendation,
						Gateable:       true,
					})
				}
			}
		}
		return nil
	})

	summary := &SectionSummary{FilesScanned: filesScanned, FindingsCount: len(findings)}
	return findings, summary, err
}

// =============================================================================
// Gates Collector — parse ~/.brain/governance/audit/gates.yaml
// =============================================================================

type gatesYAML struct {
	TierCommit struct {
		Existing []gateEntry `yaml:"existing"`
		Gaps     []gateEntry `yaml:"gaps"`
	} `yaml:"tier_commit"`
	TierPush struct {
		Existing []gateEntry `yaml:"existing"`
		Gaps     []gateEntry `yaml:"gaps"`
	} `yaml:"tier_push"`
	TierCI struct {
		Existing []gateEntry `yaml:"existing"`
		Gaps     []gateEntry `yaml:"gaps"`
	} `yaml:"tier_ci"`
}

type gateEntry struct {
	ID        string `yaml:"id"`
	Status    string `yaml:"status"`
	Rationale string `yaml:"rationale"`
}

func collectGates() ([]AuditFinding, *GatesSection, error) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".brain", "governance", "audit", "gates.yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read gates.yaml: %w", err)
	}

	var gates gatesYAML
	if err := yaml.Unmarshal(data, &gates); err != nil {
		return nil, nil, fmt.Errorf("cannot parse gates.yaml: %w", err)
	}

	var implemented, missing int
	var gaps []string
	var findings []AuditFinding

	countTier := func(existing, gapList []gateEntry) {
		for _, g := range existing {
			if g.Status == "implemented" {
				implemented++
			} else {
				missing++
				gaps = append(gaps, g.ID)
			}
		}
		for _, g := range gapList {
			if g.Status == "implemented" {
				implemented++
			} else {
				missing++
				gaps = append(gaps, g.ID)
				findings = append(findings, AuditFinding{
					Source:         "gates",
					Severity:       "medium",
					Category:       "gate_missing",
					Finding:        fmt.Sprintf("Gate %q not implemented", g.ID),
					Detail:         g.Rationale,
					Recommendation: fmt.Sprintf("Implement gate %s", g.ID),
					Gateable:       false,
				})
			}
		}
	}

	countTier(gates.TierCommit.Existing, gates.TierCommit.Gaps)
	countTier(gates.TierPush.Existing, gates.TierPush.Gaps)
	countTier(gates.TierCI.Existing, gates.TierCI.Gaps)

	total := implemented + missing
	pct := 0
	if total > 0 {
		pct = (implemented * 100) / total
	}

	section := &GatesSection{
		Implemented: implemented,
		Missing:     missing,
		CoveragePct: pct,
		Gaps:        gaps,
	}
	return findings, section, nil
}

// =============================================================================
// Quality Collector — lightweight code quality patterns
// =============================================================================

type qualityPattern struct {
	re             *regexp.Regexp
	severity       string
	category       string
	finding        string
	recommendation string
	fileMatch      func(string) bool
}

func initQualityPatterns() []qualityPattern {
	return []qualityPattern{
		{
			re:             regexp.MustCompile(`(?i)#\s*(TODO|FIXME|HACK|XXX)\b`),
			severity:       "low",
			category:       "dead_code",
			finding:        "TODO/FIXME/HACK comment",
			recommendation: "Resolve or create a task",
			fileMatch:      isPython,
		},
		{
			re:             regexp.MustCompile(`//\s*(TODO|FIXME|HACK|XXX)\b`),
			severity:       "low",
			category:       "dead_code",
			finding:        "TODO/FIXME/HACK comment",
			recommendation: "Resolve or create a task",
			fileMatch:      func(p string) bool { return isTS(p) || strings.HasSuffix(p, ".go") },
		},
		{
			re:             regexp.MustCompile(`^\s*except\s*:`),
			severity:       "medium",
			category:       "resource_leak",
			finding:        "Bare except clause — swallows all exceptions",
			recommendation: "Catch specific exceptions",
			fileMatch:      func(p string) bool { return isPython(p) && notTestFile(p) },
		},
		{
			re:             regexp.MustCompile(`(?m)^\s*print\s*\(`),
			severity:       "low",
			category:       "dead_code",
			finding:        "print() statement — likely debug leftover",
			recommendation: "Use logging module instead",
			fileMatch: func(p string) bool {
				return isPython(p) && notTestFile(p) && !strings.Contains(p, "manage") && !strings.Contains(p, "command")
			},
		},
		{
			re:             regexp.MustCompile(`console\.log\(`),
			severity:       "low",
			category:       "dead_code",
			finding:        "console.log() — debug leftover",
			recommendation: "Remove or use structured logging",
			fileMatch:      func(p string) bool { return isTS(p) && notTestFile(p) },
		},
	}
}

func collectQuality(rootDir string) ([]AuditFinding, *SectionSummary, error) {
	patterns := initQualityPatterns()
	var findings []AuditFinding
	filesScanned := 0

	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !isAnyCode(path) {
			return nil
		}
		filesScanned++

		relPath, _ := filepath.Rel(rootDir, path)
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			for _, p := range patterns {
				if p.fileMatch != nil && !p.fileMatch(path) {
					continue
				}
				if p.re.MatchString(line) {
					findings = append(findings, AuditFinding{
						Source:         "quality",
						Severity:       p.severity,
						Category:       p.category,
						File:           relPath,
						Line:           lineNum,
						Finding:        p.finding,
						Detail:         strings.TrimSpace(line),
						Recommendation: p.recommendation,
						Gateable:       true,
					})
				}
			}
		}
		return nil
	})

	summary := &SectionSummary{FilesScanned: filesScanned, FindingsCount: len(findings)}
	return findings, summary, err
}

// =============================================================================
// Drift Collector — delegates to lw drift report --json
// =============================================================================

func collectDrift(_ string) ([]AuditFinding, *DriftSection, error) {
	dir, err := resolveMakeDir("platform")
	if err != nil {
		return nil, nil, fmt.Errorf("cannot resolve platform dir: %w", err)
	}

	cmd := exec.Command("make", "dj-manage", "CMD=drift_report --json")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// Drift requires Django/Docker — gracefully skip
		fmt.Fprintf(os.Stderr, "%s drift collector skipped (Django stack not available)\n", color.YellowString("WARN"))
		return nil, nil, nil
	}

	// Parse the drift JSON output
	var driftOutput struct {
		Summary struct {
			Missing int `json:"missing"`
			Drifted int `json:"drifted"`
			Orphans int `json:"orphans"`
		} `json:"summary"`
		Items []struct {
			Schema string `json:"schema"`
			Key    string `json:"key"`
			Status string `json:"status"` // MISSING, DRIFT, ORPHAN
			Detail string `json:"detail"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &driftOutput); err != nil {
		// If JSON parse fails, try to extract just summary counts from whatever came back
		fmt.Fprintf(os.Stderr, "%s drift output not parseable as JSON, skipping\n", color.YellowString("WARN"))
		return nil, nil, nil
	}

	var findings []AuditFinding
	for _, item := range driftOutput.Items {
		sev := "medium"
		cat := "drifted"
		switch item.Status {
		case "MISSING":
			cat = "missing"
			sev = "high"
		case "ORPHAN":
			cat = "orphan"
			sev = "low"
		}
		findings = append(findings, AuditFinding{
			Source:         "drift",
			Severity:       sev,
			Category:       cat,
			Finding:        fmt.Sprintf("[%s] %s: %s", item.Status, item.Schema, item.Key),
			Detail:         item.Detail,
			Recommendation: "Run lw drift reconcile to sync",
			Gateable:       false,
		})
	}

	section := &DriftSection{
		Missing: driftOutput.Summary.Missing,
		Drifted: driftOutput.Summary.Drifted,
		Orphans: driftOutput.Summary.Orphans,
	}
	return findings, section, nil
}

// =============================================================================
// Report Builder
// =============================================================================

func calculateScore(c SeverityCounts) int {
	score := 100 - (c.Critical * 25) - (c.High * 10) - (c.Medium * 3) - (c.Low * 1)
	if score < 0 {
		return 0
	}
	return score
}

func statusFromScore(score int) string {
	if score >= 80 {
		return "on_track"
	}
	if score >= 50 {
		return "at_risk"
	}
	return "off_track"
}

func countSeverities(findings []AuditFinding) SeverityCounts {
	var c SeverityCounts
	for _, f := range findings {
		switch f.Severity {
		case "critical":
			c.Critical++
		case "high":
			c.High++
		case "medium":
			c.Medium++
		case "low":
			c.Low++
		}
	}
	return c
}

func dedup(findings []AuditFinding) []AuditFinding {
	seen := make(map[string]bool)
	var out []AuditFinding
	for _, f := range findings {
		key := f.CompositeKey()
		if !seen[key] {
			seen[key] = true
			out = append(out, f)
		}
	}
	return out
}

func buildNarrative(counts SeverityCounts, gates *GatesSection, drift *DriftSection) string {
	var parts []string
	if counts.Critical > 0 {
		parts = append(parts, fmt.Sprintf("%d critical", counts.Critical))
	}
	if counts.High > 0 {
		parts = append(parts, fmt.Sprintf("%d high", counts.High))
	}
	if counts.Medium > 0 {
		parts = append(parts, fmt.Sprintf("%d medium", counts.Medium))
	}
	if counts.Low > 0 {
		parts = append(parts, fmt.Sprintf("%d low", counts.Low))
	}
	if gates != nil {
		parts = append(parts, fmt.Sprintf("gate coverage %d%%", gates.CoveragePct))
	}
	if drift != nil {
		total := drift.Missing + drift.Drifted + drift.Orphans
		if total > 0 {
			parts = append(parts, fmt.Sprintf("%d drift items", total))
		}
	}
	if len(parts) == 0 {
		return "No findings"
	}
	return strings.Join(parts, ", ")
}

func buildRecommendations(findings []AuditFinding, gates *GatesSection) []string {
	var recs []string
	// Criticals first
	for _, f := range findings {
		if f.Severity == "critical" {
			recs = append(recs, fmt.Sprintf("Fix CRITICAL: %s in %s:%d", f.Finding, f.File, f.Line))
		}
	}
	// Gate gaps
	if gates != nil {
		for _, gap := range gates.Gaps {
			recs = append(recs, fmt.Sprintf("Implement gate: %s", gap))
		}
	}
	// High findings (limit to top 5)
	highCount := 0
	for _, f := range findings {
		if f.Severity == "high" && highCount < 5 {
			recs = append(recs, fmt.Sprintf("Fix HIGH: %s in %s:%d", f.Finding, f.File, f.Line))
			highCount++
		}
	}
	if len(recs) > 10 {
		recs = recs[:10]
	}
	return recs
}

func gitInfo() (string, string) {
	commit, _ := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	branch, _ := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	return strings.TrimSpace(string(commit)), strings.TrimSpace(string(branch))
}

// =============================================================================
// Trend Comparison
// =============================================================================

func computeTrend(current *AuditReport) *TrendComparison {
	reports, err := listReportFiles()
	if err != nil || len(reports) == 0 {
		return nil
	}

	// Load most recent previous report
	var prev *AuditReport
	for i := len(reports) - 1; i >= 0; i-- {
		dateStr := extractDate(reports[i])
		if dateStr == current.Meta.GeneratedAt.Format("2006-01-02") {
			continue // skip current
		}
		p, err := loadReportFromPath(reports[i])
		if err == nil {
			prev = p
			break
		}
	}
	if prev == nil {
		return nil
	}

	// Build key sets
	prevKeys := make(map[string]bool)
	for _, f := range prev.Findings {
		prevKeys[f.CompositeKey()] = true
	}
	currKeys := make(map[string]bool)
	for _, f := range current.Findings {
		currKeys[f.CompositeKey()] = true
	}

	newCount := 0
	for k := range currKeys {
		if !prevKeys[k] {
			newCount++
		}
	}
	resolvedCount := 0
	for k := range prevKeys {
		if !currKeys[k] {
			resolvedCount++
		}
	}

	return &TrendComparison{
		PreviousDate:     prev.Meta.GeneratedAt.Format("2006-01-02"),
		PreviousScore:    prev.ExecutiveSummary.HealthScore,
		Delta:            current.ExecutiveSummary.HealthScore - prev.ExecutiveSummary.HealthScore,
		NewFindings:      newCount,
		ResolvedFindings: resolvedCount,
	}
}

// =============================================================================
// Persistence
// =============================================================================

func auditDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".brain", "memory", "audits")
}

func saveReport(report *AuditReport) error {
	dir := auditDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	dateStr := report.Meta.GeneratedAt.Format("2006-01-02")

	// JSON
	jsonPath := filepath.Join(dir, dateStr+"_audit.json")
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return err
	}

	// Markdown
	mdPath := filepath.Join(dir, dateStr+"_adversarial_audit.md")
	md := renderMarkdown(report)
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		return err
	}

	return nil
}

func loadLatestReport() (*AuditReport, error) {
	reports, err := listReportFiles()
	if err != nil {
		return nil, err
	}
	if len(reports) == 0 {
		return nil, fmt.Errorf("no audit reports found in %s", auditDir())
	}
	return loadReportFromPath(reports[len(reports)-1])
}

func loadReportByDate(date string) (*AuditReport, error) {
	path := filepath.Join(auditDir(), date+"_audit.json")
	return loadReportFromPath(path)
}

func loadReportFromPath(path string) (*AuditReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var report AuditReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	return &report, nil
}

func listReportFiles() ([]string, error) {
	pattern := filepath.Join(auditDir(), "*_audit.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func extractDate(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, "_audit.json")
}

func pruneReports(retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	reports, err := listReportFiles()
	if err != nil {
		return err
	}
	for _, r := range reports {
		dateStr := extractDate(r)
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			os.Remove(r)
			// Also remove companion markdown
			mdPath := filepath.Join(auditDir(), dateStr+"_adversarial_audit.md")
			os.Remove(mdPath)
		}
	}
	return nil
}

// =============================================================================
// Markdown Renderer
// =============================================================================

func renderMarkdown(r *AuditReport) string {
	var b strings.Builder
	date := r.Meta.GeneratedAt.Format("2006-01-02")

	b.WriteString(fmt.Sprintf("# Adversarial Audit Report — %s\n\n", date))
	b.WriteString("## Executive Summary\n")
	b.WriteString(fmt.Sprintf("Health Score: **%d/100** | Status: **%s**\n\n", r.ExecutiveSummary.HealthScore, r.ExecutiveSummary.Status))
	b.WriteString(r.ExecutiveSummary.Narrative + "\n\n")

	// Findings tables by severity
	for _, sev := range []string{"critical", "high", "medium"} {
		var rows []AuditFinding
		for _, f := range r.Findings {
			if f.Severity == sev {
				rows = append(rows, f)
			}
		}
		if len(rows) == 0 {
			continue
		}
		sevTitle := strings.ToUpper(sev[:1]) + sev[1:]
		b.WriteString(fmt.Sprintf("## %s Findings (%d)\n\n", sevTitle, len(rows)))
		b.WriteString("| # | Source | Category | File | Finding | Recommendation |\n")
		b.WriteString("|---|--------|----------|------|---------|----------------|\n")
		for i, f := range rows {
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			b.WriteString(fmt.Sprintf("| %d | %s | %s | %s | %s | %s |\n",
				i+1, f.Source, f.Category, loc, f.Finding, f.Recommendation))
		}
		b.WriteString("\n")
	}

	// Low findings count only
	lowCount := 0
	for _, f := range r.Findings {
		if f.Severity == "low" {
			lowCount++
		}
	}
	if lowCount > 0 {
		b.WriteString(fmt.Sprintf("## Low Findings\n%d low-severity findings (use `--verbose` to expand)\n\n", lowCount))
	}

	// Gate coverage
	if r.Sections.Gates != nil {
		g := r.Sections.Gates
		total := g.Implemented + g.Missing
		b.WriteString("## Gate Coverage\n")
		b.WriteString(fmt.Sprintf("%d/%d gates active (%d%%)\n\n", g.Implemented, total, g.CoveragePct))
		if len(g.Gaps) > 0 {
			b.WriteString("Missing: " + strings.Join(g.Gaps, ", ") + "\n\n")
		}
	}

	// Drift
	if r.Sections.Drift != nil {
		d := r.Sections.Drift
		b.WriteString("## Schema Drift\n")
		b.WriteString(fmt.Sprintf("%d missing | %d drifted | %d orphans\n\n", d.Missing, d.Drifted, d.Orphans))
	}

	// Metrics
	b.WriteString("## Metrics\n")
	c := r.ExecutiveSummary.FindingsBySeverity
	b.WriteString(fmt.Sprintf("- Total findings: %d (critical: %d, high: %d, medium: %d, low: %d)\n",
		r.ExecutiveSummary.FindingsTotal, c.Critical, c.High, c.Medium, c.Low))
	if r.Sections.Security != nil {
		b.WriteString(fmt.Sprintf("- Files scanned (security): %d\n", r.Sections.Security.FilesScanned))
	}
	if r.Sections.Quality != nil {
		b.WriteString(fmt.Sprintf("- Files scanned (quality): %d\n", r.Sections.Quality.FilesScanned))
	}
	if r.Trend != nil {
		t := r.Trend
		sign := "+"
		if t.Delta < 0 {
			sign = ""
		}
		b.WriteString(fmt.Sprintf("- vs previous (%s): %s%d score, %d new, %d resolved\n",
			t.PreviousDate, sign, t.Delta, t.NewFindings, t.ResolvedFindings))
	}
	b.WriteString("\n")

	// Recommendations
	if len(r.Recommendations) > 0 {
		b.WriteString("## Recommendations\n")
		for i, rec := range r.Recommendations {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, rec))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// =============================================================================
// Terminal Renderer
// =============================================================================

func renderTerminal(r *AuditReport) {
	date := r.Meta.GeneratedAt.Format("2006-01-02")
	c := r.ExecutiveSummary.FindingsBySeverity

	fmt.Println()
	fmt.Println(color.CyanString("Adversarial Audit Report — %s", date))
	fmt.Println()

	// Score with color
	scoreColor := color.GreenString
	if r.ExecutiveSummary.HealthScore < 80 {
		scoreColor = color.YellowString
	}
	if r.ExecutiveSummary.HealthScore < 50 {
		scoreColor = color.RedString
	}
	fmt.Printf("  Health Score: %s  Status: %s\n", scoreColor("%d/100", r.ExecutiveSummary.HealthScore), r.ExecutiveSummary.Status)
	fmt.Printf("  %s\n\n", r.ExecutiveSummary.Narrative)

	// Severity summary
	fmt.Printf("  Findings: %s critical  %s high  %s medium  %s low\n\n",
		color.RedString("%d", c.Critical),
		color.YellowString("%d", c.High),
		color.CyanString("%d", c.Medium),
		fmt.Sprintf("%d", c.Low),
	)

	// Critical + High findings table
	var tableRows []AuditFinding
	for _, f := range r.Findings {
		if f.Severity == "critical" || f.Severity == "high" {
			tableRows = append(tableRows, f)
		}
	}

	if len(tableRows) > 0 {
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Sev", "Source", "Category", "File", "Finding"})
		table.SetAutoWrapText(false)
		table.SetBorder(false)
		table.SetColumnSeparator("")
		table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
		table.SetAlignment(tablewriter.ALIGN_LEFT)

		for _, f := range tableRows {
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			// Truncate long paths
			if len(loc) > 50 {
				loc = "..." + loc[len(loc)-47:]
			}
			sev := f.Severity
			if sev == "critical" {
				sev = color.RedString("CRIT")
			} else {
				sev = color.YellowString("HIGH")
			}
			table.Append([]string{sev, f.Source, f.Category, loc, f.Finding})
		}
		table.Render()
		fmt.Println()
	}

	// Gate coverage
	if r.Sections.Gates != nil {
		g := r.Sections.Gates
		total := g.Implemented + g.Missing
		fmt.Printf("  Gates: %d/%d (%d%%)  Missing: %s\n\n",
			g.Implemented, total, g.CoveragePct,
			color.YellowString(strings.Join(g.Gaps, ", ")))
	}

	// Trend
	if r.Trend != nil {
		t := r.Trend
		deltaStr := fmt.Sprintf("%+d", t.Delta)
		if t.Delta > 0 {
			deltaStr = color.GreenString(deltaStr)
		} else if t.Delta < 0 {
			deltaStr = color.RedString(deltaStr)
		}
		fmt.Printf("  Trend vs %s: %s score  %d new  %d resolved\n\n",
			t.PreviousDate, deltaStr, t.NewFindings, t.ResolvedFindings)
	}
}

// =============================================================================
// Diff Renderer
// =============================================================================

func renderDiff(current, previous *AuditReport) {
	currDate := current.Meta.GeneratedAt.Format("2006-01-02")
	prevDate := previous.Meta.GeneratedAt.Format("2006-01-02")

	fmt.Println()
	fmt.Println(color.CyanString("Audit Diff: %s vs %s", currDate, prevDate))
	fmt.Printf("  Score: %d → %d (%+d)\n\n",
		previous.ExecutiveSummary.HealthScore,
		current.ExecutiveSummary.HealthScore,
		current.ExecutiveSummary.HealthScore-previous.ExecutiveSummary.HealthScore,
	)

	prevKeys := make(map[string]AuditFinding)
	for _, f := range previous.Findings {
		prevKeys[f.CompositeKey()] = f
	}
	currKeys := make(map[string]AuditFinding)
	for _, f := range current.Findings {
		currKeys[f.CompositeKey()] = f
	}

	// Resolved
	var resolved []AuditFinding
	for k, f := range prevKeys {
		if _, ok := currKeys[k]; !ok {
			resolved = append(resolved, f)
		}
	}
	if len(resolved) > 0 {
		fmt.Printf("  %s (%d):\n", color.GreenString("Resolved"), len(resolved))
		for _, f := range resolved {
			fmt.Printf("    - [%s] %s", strings.ToUpper(f.Severity), f.Finding)
			if f.File != "" {
				fmt.Printf(" in %s:%d", f.File, f.Line)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	// New
	var newFindings []AuditFinding
	for k, f := range currKeys {
		if _, ok := prevKeys[k]; !ok {
			newFindings = append(newFindings, f)
		}
	}
	if len(newFindings) > 0 {
		fmt.Printf("  %s (%d):\n", color.RedString("New"), len(newFindings))
		for _, f := range newFindings {
			fmt.Printf("    - [%s] %s", strings.ToUpper(f.Severity), f.Finding)
			if f.File != "" {
				fmt.Printf(" in %s:%d", f.File, f.Line)
			}
			fmt.Println()
		}
		fmt.Println()
	}

	// Persistent
	var persistent []AuditFinding
	for k, f := range currKeys {
		if _, ok := prevKeys[k]; ok {
			persistent = append(persistent, f)
		}
	}
	if len(persistent) > 0 {
		fmt.Printf("  Persistent (%d):\n", len(persistent))
		// Only show critical/high persistent
		shown := 0
		for _, f := range persistent {
			if f.Severity == "critical" || f.Severity == "high" {
				fmt.Printf("    - [%s] %s", strings.ToUpper(f.Severity), f.Finding)
				if f.File != "" {
					fmt.Printf(" in %s:%d", f.File, f.Line)
				}
				fmt.Println()
				shown++
			}
		}
		if shown < len(persistent) {
			fmt.Printf("    ... and %d medium/low\n", len(persistent)-shown)
		}
		fmt.Println()
	}
}

// =============================================================================
// Cobra Commands
// =============================================================================

var (
	auditScope  string
	auditJSON   bool
	auditDate   string
	auditOutput string
	auditLast   int
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Adversarial audit — security, drift, gates, quality",
	Long: `Run native Go audit collectors against the monorepo.

Scans for security vulnerabilities, schema drift, gate coverage gaps,
and code quality issues. Produces a scored report with findings and
recommendations.

Examples:
  lw audit                            # Full audit (all scopes)
  lw audit run --scope security       # Security scan only
  lw audit run --scope gates          # Gate coverage analysis only
  lw audit report                     # Show latest report
  lw audit report --json              # Latest report as JSON
  lw audit diff                       # Compare latest vs previous
  lw audit list                       # List stored reports`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Default: run full audit
		return runAudit("all")
	},
}

var auditRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run audit collectors and produce a report",
	Long: `Run all or scoped audit collectors natively in Go.

Scopes:
  all       — Run everything (default)
  security  — File pattern scanner (credentials, injection, debug, auth)
  drift     — Schema drift via lw drift report (requires Django stack)
  gates     — Parse gates.yaml for coverage gaps
  quality   — Code quality patterns (TODO, bare except, debug prints)

Examples:
  lw audit run                    # All scopes
  lw audit run --scope security   # Security only
  lw audit run --json             # Output JSON to stdout`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAudit(auditScope)
	},
}

func runAudit(scope string) error {
	start := time.Now()
	cfg := config.Get()
	rootDir := cfg.Paths.LightwaveRoot

	fmt.Printf("%s Running audit (scope: %s) against %s\n\n",
		color.CyanString("▶"), scope, rootDir)

	var allFindings []AuditFinding
	var sections ReportSections

	runScope := func(name string) bool {
		return scope == "all" || scope == name
	}

	// Security
	if runScope("security") {
		fmt.Printf("  %s Security scan...", color.CyanString("●"))
		findings, summary, err := collectSecurity(rootDir)
		if err != nil {
			fmt.Printf(" %s\n", color.RedString("error: %v", err))
		} else {
			fmt.Printf(" %d findings (%d files)\n", len(findings), summary.FilesScanned)
			allFindings = append(allFindings, findings...)
			sections.Security = summary
		}
	}

	// Gates
	if runScope("gates") {
		fmt.Printf("  %s Gate analysis...", color.CyanString("●"))
		findings, gateSection, err := collectGates()
		if err != nil {
			fmt.Printf(" %s\n", color.RedString("error: %v", err))
		} else {
			fmt.Printf(" %d/%d implemented (%d%%)\n",
				gateSection.Implemented, gateSection.Implemented+gateSection.Missing, gateSection.CoveragePct)
			allFindings = append(allFindings, findings...)
			sections.Gates = gateSection
		}
	}

	// Quality
	if runScope("quality") {
		fmt.Printf("  %s Quality scan...", color.CyanString("●"))
		findings, summary, err := collectQuality(rootDir)
		if err != nil {
			fmt.Printf(" %s\n", color.RedString("error: %v", err))
		} else {
			fmt.Printf(" %d findings (%d files)\n", len(findings), summary.FilesScanned)
			allFindings = append(allFindings, findings...)
			sections.Quality = summary
		}
	}

	// Drift
	if runScope("drift") {
		fmt.Printf("  %s Drift check...", color.CyanString("●"))
		findings, driftSection, err := collectDrift(rootDir)
		if err != nil {
			fmt.Printf(" %s\n", color.RedString("error: %v", err))
		} else if driftSection != nil {
			total := driftSection.Missing + driftSection.Drifted + driftSection.Orphans
			fmt.Printf(" %d items\n", total)
			allFindings = append(allFindings, findings...)
			sections.Drift = driftSection
		} else {
			fmt.Printf(" skipped\n")
		}
	}

	// Dedup + build report
	allFindings = dedup(allFindings)
	counts := countSeverities(allFindings)
	score := calculateScore(counts)

	commit, branch := gitInfo()
	report := &AuditReport{
		Meta: ReportMeta{
			GeneratedAt:     time.Now().UTC(),
			Scope:           scope,
			DurationSeconds: int(time.Since(start).Seconds()),
			GitCommit:       commit,
			GitBranch:       branch,
		},
		ExecutiveSummary: ExecutiveSummary{
			HealthScore:        score,
			Status:             statusFromScore(score),
			Narrative:          buildNarrative(counts, sections.Gates, sections.Drift),
			FindingsTotal:      len(allFindings),
			FindingsBySeverity: counts,
		},
		Sections:        sections,
		Findings:        allFindings,
		Recommendations: buildRecommendations(allFindings, sections.Gates),
	}

	// Trend
	report.Trend = computeTrend(report)

	// Persist
	if err := saveReport(report); err != nil {
		fmt.Fprintf(os.Stderr, "%s Failed to save report: %v\n", color.RedString("ERR"), err)
	}

	// Prune old reports
	_ = pruneReports(90)

	// Output
	fmt.Println()
	if auditJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	renderTerminal(report)

	date := report.Meta.GeneratedAt.Format("2006-01-02")
	fmt.Printf("  Saved: %s\n", color.CyanString("%s/{%s_audit.json,%s_adversarial_audit.md}", auditDir(), date, date))
	return nil
}

var auditReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Show a stored audit report",
	Long: `Display a previously generated audit report.

Shows the latest report by default. Use --date to view a specific report.

Examples:
  lw audit report                     # Latest report (formatted)
  lw audit report --json              # Latest as JSON
  lw audit report --date 2026-03-20   # Specific date
  lw audit report -o report.md        # Save to file`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var report *AuditReport
		var err error

		if auditDate != "" {
			report, err = loadReportByDate(auditDate)
		} else {
			report, err = loadLatestReport()
		}
		if err != nil {
			return err
		}

		if auditOutput != "" {
			md := renderMarkdown(report)
			if err := os.WriteFile(auditOutput, []byte(md), 0644); err != nil {
				return err
			}
			fmt.Printf("Report saved to %s\n", auditOutput)
			return nil
		}

		if auditJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}

		renderTerminal(report)
		return nil
	},
}

var auditDiffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Compare two audit reports",
	Long: `Compare the latest report against a previous one.

Shows resolved findings, new findings, and persistent issues.

Examples:
  lw audit diff                       # Latest vs previous
  lw audit diff --date 2026-03-20     # Latest vs specific date`,
	RunE: func(cmd *cobra.Command, args []string) error {
		current, err := loadLatestReport()
		if err != nil {
			return fmt.Errorf("cannot load latest report: %w", err)
		}

		var previous *AuditReport
		if auditDate != "" {
			previous, err = loadReportByDate(auditDate)
			if err != nil {
				return fmt.Errorf("cannot load report for %s: %w", auditDate, err)
			}
		} else {
			// Load second-most-recent
			reports, err := listReportFiles()
			if err != nil || len(reports) < 2 {
				return fmt.Errorf("need at least 2 reports for diff (found %d)", len(reports))
			}
			previous, err = loadReportFromPath(reports[len(reports)-2])
			if err != nil {
				return fmt.Errorf("cannot load previous report: %w", err)
			}
		}

		renderDiff(current, previous)
		return nil
	},
}

var auditListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stored audit reports",
	Long: `Show all audit reports stored in ~/.brain/memory/audits/.

Examples:
  lw audit list             # All reports
  lw audit list --last 5    # Last 5 reports`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reports, err := listReportFiles()
		if err != nil {
			return err
		}
		if len(reports) == 0 {
			fmt.Println(color.YellowString("No audit reports found"))
			return nil
		}

		// Apply --last limit
		if auditLast > 0 && auditLast < len(reports) {
			reports = reports[len(reports)-auditLast:]
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Date", "Score", "Status", "Findings", "Scope"})
		table.SetBorder(false)
		table.SetColumnSeparator("")
		table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
		table.SetAlignment(tablewriter.ALIGN_LEFT)

		for _, r := range reports {
			report, err := loadReportFromPath(r)
			if err != nil {
				continue
			}
			table.Append([]string{
				extractDate(r),
				fmt.Sprintf("%d", report.ExecutiveSummary.HealthScore),
				report.ExecutiveSummary.Status,
				fmt.Sprintf("%d", report.ExecutiveSummary.FindingsTotal),
				report.Meta.Scope,
			})
		}
		table.Render()
		return nil
	},
}

func init() {
	// audit run flags
	auditRunCmd.Flags().StringVar(&auditScope, "scope", "all", "audit scope: all, security, drift, gates, quality")
	auditRunCmd.Flags().BoolVar(&auditJSON, "json", false, "output report as JSON")

	// audit report flags
	auditReportCmd.Flags().BoolVar(&auditJSON, "json", false, "output as JSON")
	auditReportCmd.Flags().StringVar(&auditDate, "date", "", "report date (YYYY-MM-DD)")
	auditReportCmd.Flags().StringVarP(&auditOutput, "output", "o", "", "save report to file")

	// audit diff flags
	auditDiffCmd.Flags().StringVar(&auditDate, "date", "", "compare against this date")

	// audit list flags
	auditListCmd.Flags().IntVar(&auditLast, "last", 0, "show last N reports")

	// Register subcommands
	auditCmd.AddCommand(auditRunCmd)
	auditCmd.AddCommand(auditReportCmd)
	auditCmd.AddCommand(auditDiffCmd)
	auditCmd.AddCommand(auditListCmd)
}
