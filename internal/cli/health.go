package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var healthJSON bool

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check CLI dependencies and service connectivity",
	Long: `Verify all tools, paths, and services the CLI depends on.

Checks:
  - Required binaries (git, gh, docker, make, go)
  - Optional tools (ffmpeg, whisper-cli, psql)
  - Config loaded and paths exist
  - PostgreSQL connectivity (if configured)
  - Paperclip API reachability (if running)

Examples:
  lw health
  lw health --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runHealth(healthJSON)
	},
}

func init() {
	healthCmd.Flags().BoolVar(&healthJSON, "json", false, "Output as JSON")
}

// =============================================================================
// Types
// =============================================================================

type healthCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "warn", "fail"
	Detail  string `json:"detail,omitempty"`
	Elapsed string `json:"elapsed,omitempty"`
}

type healthReport struct {
	Timestamp string        `json:"timestamp"`
	Overall   string        `json:"overall"` // "ok", "warn", "fail"
	Checks    []healthCheck `json:"checks"`
}

// =============================================================================
// Runner
// =============================================================================

func runHealth(asJSON bool) error {
	cfg := config.Get()
	start := time.Now()

	checks := []healthCheck{}

	// ── Required binaries ────────────────────────────────────────────────────
	required := []struct{ name, bin string }{
		{"git", "git"},
		{"gh (GitHub CLI)", "gh"},
		{"docker", "docker"},
		{"make", "make"},
		{"go", "go"},
		{"node", "node"},
		{"jq", "jq"},
	}
	for _, r := range required {
		checks = append(checks, checkBinary(r.name, r.bin, false))
	}

	// ── Optional binaries ────────────────────────────────────────────────────
	optional := []struct{ name, bin string }{
		{"ffmpeg (lw ux)", "ffmpeg"},
		{"whisper-cli (lw ux)", "whisper-cli"},
		{"psql", "psql"},
	}
	for _, o := range optional {
		checks = append(checks, checkBinary(o.name, o.bin, true))
	}

	// ── Config paths ─────────────────────────────────────────────────────────
	checks = append(checks, checkPath("lightwave_root", cfg.Paths.LightwaveRoot))
	checks = append(checks, checkPath("platform dir", cfg.Paths.Platform))
	checks = append(checks, checkPath("cli dir", filepath.Join(cfg.Paths.LightwaveRoot, "packages/lightwave-cli")))

	// ── Environment ──────────────────────────────────────────────────────────
	checks = append(checks, checkEnvVar("LW_ENV", false))
	checks = append(checks, checkEnvVar("LW_AGENT_KEY", true))
	checks = append(checks, checkEnvVar("PAPERCLIP_API_KEY", true))

	// ── PostgreSQL ───────────────────────────────────────────────────────────
	checks = append(checks, checkPostgres(cfg))

	// ── Paperclip API ────────────────────────────────────────────────────────
	checks = append(checks, checkHTTP("Paperclip API", cfg.GetPaperclipURL()+"/api/health", true))

	// ── Orchestrator ─────────────────────────────────────────────────────────
	checks = append(checks, checkHTTP("Orchestrator", cfg.GetOrchestratorURL()+"/health", true))

	// ── Determine overall ────────────────────────────────────────────────────
	overall := "ok"
	for _, c := range checks {
		if c.Status == "fail" {
			overall = "fail"
			break
		}
		if c.Status == "warn" && overall == "ok" {
			overall = "warn"
		}
	}

	report := healthReport{
		Timestamp: start.UTC().Format(time.RFC3339),
		Overall:   overall,
		Checks:    checks,
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	return printHealthTable(report)
}

// =============================================================================
// Check helpers
// =============================================================================

func checkBinary(name, bin string, optional bool) healthCheck {
	t := time.Now()
	path, err := exec.LookPath(bin)
	elapsed := fmt.Sprintf("%dms", time.Since(t).Milliseconds())
	if err != nil {
		status := "fail"
		if optional {
			status = "warn"
		}
		return healthCheck{Name: name, Status: status, Detail: "not found in PATH", Elapsed: elapsed}
	}
	return healthCheck{Name: name, Status: "ok", Detail: path, Elapsed: elapsed}
}

func checkPath(name, path string) healthCheck {
	if path == "" {
		return healthCheck{Name: name, Status: "warn", Detail: "not configured"}
	}
	if _, err := os.Stat(path); err != nil {
		return healthCheck{Name: name, Status: "fail", Detail: "path does not exist: " + path}
	}
	return healthCheck{Name: name, Status: "ok", Detail: path}
}

func checkEnvVar(name string, optional bool) healthCheck {
	val := os.Getenv(name)
	if val == "" {
		status := "warn"
		if !optional {
			status = "fail"
		}
		return healthCheck{Name: name, Status: status, Detail: "not set"}
	}
	// Show first 4 chars only to confirm presence without exposing secrets
	masked := val[:min(4, len(val))] + "****"
	return healthCheck{Name: name, Status: "ok", Detail: masked}
}

func checkPostgres(cfg *config.Config) healthCheck {
	t := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx)
	elapsed := fmt.Sprintf("%dms", time.Since(t).Milliseconds())
	if err != nil {
		return healthCheck{Name: "PostgreSQL", Status: "warn", Detail: "cannot connect: " + err.Error(), Elapsed: elapsed}
	}

	var result int
	err = pool.QueryRow(ctx, "SELECT 1").Scan(&result)
	elapsed = fmt.Sprintf("%dms", time.Since(t).Milliseconds())
	if err != nil {
		return healthCheck{Name: "PostgreSQL", Status: "warn", Detail: "ping failed: " + err.Error(), Elapsed: elapsed}
	}
	return healthCheck{
		Name:    "PostgreSQL",
		Status:  "ok",
		Detail:  fmt.Sprintf("%s:%d/%s", cfg.Database.Host, cfg.Database.Port, cfg.Database.Name),
		Elapsed: elapsed,
	}
}

func checkHTTP(name, url string, optional bool) healthCheck {
	t := time.Now()
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url) //nolint:noctx
	elapsed := fmt.Sprintf("%dms", time.Since(t).Milliseconds())
	if err != nil {
		status := "warn"
		if !optional {
			status = "fail"
		}
		return healthCheck{Name: name, Status: status, Detail: "unreachable: " + url, Elapsed: elapsed}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return healthCheck{Name: name, Status: "warn", Detail: fmt.Sprintf("HTTP %d at %s", resp.StatusCode, url), Elapsed: elapsed}
	}
	return healthCheck{Name: name, Status: "ok", Detail: fmt.Sprintf("HTTP %d at %s", resp.StatusCode, url), Elapsed: elapsed}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// =============================================================================
// Output
// =============================================================================

func printHealthTable(r healthReport) error {
	okStr := color.GreenString("ok")
	warnStr := color.YellowString("warn")
	failStr := color.RedString("fail")

	statusLabel := map[string]string{
		"ok":   okStr,
		"warn": warnStr,
		"fail": failStr,
	}

	overallColor := map[string]func(string, ...interface{}) string{
		"ok":   color.GreenString,
		"warn": color.YellowString,
		"fail": color.RedString,
	}[r.Overall]

	fmt.Printf("\nlw health  %s  (%s)\n\n", overallColor(r.Overall), r.Timestamp)

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Check", "Status", "Detail", "Elapsed"})
	table.SetBorder(false)
	table.SetColumnSeparator("  ")
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)

	for _, c := range r.Checks {
		detail := c.Detail
		if len(detail) > 60 {
			detail = "..." + detail[len(detail)-57:]
		}
		table.Append([]string{c.Name, statusLabel[c.Status], detail, c.Elapsed})
	}
	table.Render()
	fmt.Println()
	return nil
}
