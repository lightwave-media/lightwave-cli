package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/paperclip"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

// --- shared helpers ---

// resolveAgent finds an agent by name (kebab-case, display name, or lowercase).
func resolveAgent(ctx context.Context, client *paperclip.Client, name string) (*paperclip.Agent, error) {
	agents, err := client.ListAllAgents(ctx)
	if err != nil {
		return nil, err
	}
	normalized := normalizeAgentName(name)
	for i, a := range agents {
		if normalizeAgentName(a.Name) == normalized {
			return &agents[i], nil
		}
	}
	return nil, fmt.Errorf("agent %q not found", name)
}

// --- lw agent pause <agent-name> ---

var agentPauseCmd = &cobra.Command{
	Use:   "pause <agent-name>",
	Short: "Pause an agent's heartbeat",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := paperclip.NewClient()

		agent, err := resolveAgent(ctx, client, args[0])
		if err != nil {
			return err
		}

		resp, err := client.PauseAgent(ctx, agent.ID)
		if err != nil {
			return err
		}

		if agentJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		}

		fmt.Printf("%s Paused %s\n", color.YellowString("⏸"), color.CyanString(agent.Name))
		return nil
	},
}

// --- lw agent resume <agent-name> ---

var agentResumeCmd = &cobra.Command{
	Use:   "resume <agent-name>",
	Short: "Resume an agent's heartbeat",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := paperclip.NewClient()

		agent, err := resolveAgent(ctx, client, args[0])
		if err != nil {
			return err
		}

		resp, err := client.ResumeAgent(ctx, agent.ID)
		if err != nil {
			return err
		}

		if agentJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		}

		fmt.Printf("%s Resumed %s\n", color.GreenString("▶"), color.CyanString(agent.Name))
		return nil
	},
}

// --- lw agent invoke <agent-name> ---

var agentInvokeCmd = &cobra.Command{
	Use:   "invoke <agent-name>",
	Short: "Manually trigger an agent's heartbeat",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := paperclip.NewClient()

		agent, err := resolveAgent(ctx, client, args[0])
		if err != nil {
			return err
		}

		resp, err := client.InvokeHeartbeat(ctx, agent.ID)
		if err != nil {
			return err
		}

		if agentJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		}

		fmt.Printf("%s Heartbeat invoked for %s\n", color.GreenString("✓"), color.CyanString(agent.Name))
		return nil
	},
}

// --- lw agent logs [agent-name] ---

var (
	agentLogsLimit int
	agentLogsType  string
)

var agentLogsCmd = &cobra.Command{
	Use:   "logs [agent-name]",
	Short: "Show recent activity from Paperclip audit trail",
	Long: `Show recent activity entries from the Paperclip audit trail.
Without an agent name, shows all activity across agents.

Examples:
  lw agent logs
  lw agent logs backend-engineer
  lw agent logs --limit 50
  lw agent logs --type issue --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := paperclip.NewClient()

		filter := paperclip.ActivityFilter{
			Limit:      agentLogsLimit,
			EntityType: agentLogsType,
		}

		// If agent name given, resolve and filter by agent ID
		if len(args) > 0 {
			agent, err := resolveAgent(ctx, client, args[0])
			if err != nil {
				return err
			}
			filter.AgentID = agent.ID
		}

		activities, err := client.ListAllActivity(ctx, filter)
		if err != nil {
			return err
		}

		if agentJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(activities)
		}

		if len(activities) == 0 {
			fmt.Println("No activity found.")
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Time", "Agent", "Action", "Entity", "Detail"})
		table.SetBorder(false)
		table.SetColumnSeparator(" ")
		table.SetColWidth(40)

		for _, a := range activities {
			agentName := a.AgentName
			if agentName == "" {
				agentName = "-"
			}
			entity := a.EntityType
			if a.EntityID != "" {
				entity += "/" + truncateStr(a.EntityID, 8)
			}
			table.Append([]string{
				a.CreatedAt.Local().Format("01-02 15:04"),
				agentName,
				a.Action,
				entity,
				truncateStr(a.Detail, 40),
			})
		}
		table.Render()
		return nil
	},
}

// --- lw agent runs [agent-name] ---

var (
	agentRunsLimit    int
	agentRunsFailOnly bool
)

var agentRunsCmd = &cobra.Command{
	Use:   "runs [agent-name]",
	Short: "Show heartbeat execution history",
	Long: `Show heartbeat run history with success/failure metrics.
Without an agent name, shows runs across all agents.

Examples:
  lw agent runs
  lw agent runs backend-engineer
  lw agent runs --failures-only
  lw agent runs --limit 50 --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := paperclip.NewClient()

		filter := paperclip.ActivityFilter{
			Limit:      agentRunsLimit,
			EntityType: "heartbeat",
		}

		if len(args) > 0 {
			agent, err := resolveAgent(ctx, client, args[0])
			if err != nil {
				return err
			}
			filter.AgentID = agent.ID
		}

		activities, err := client.ListAllActivity(ctx, filter)
		if err != nil {
			return err
		}

		// Filter to failures only if requested
		if agentRunsFailOnly {
			var filtered []paperclip.Activity
			for _, a := range activities {
				if strings.Contains(strings.ToLower(a.Action), "fail") ||
					strings.Contains(strings.ToLower(a.Action), "error") {
					filtered = append(filtered, a)
				}
			}
			activities = filtered
		}

		if agentJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(activities)
		}

		// Summary stats
		total := len(activities)
		failures := 0
		for _, a := range activities {
			if strings.Contains(strings.ToLower(a.Action), "fail") ||
				strings.Contains(strings.ToLower(a.Action), "error") {
				failures++
			}
		}
		successRate := 0.0
		if total > 0 {
			successRate = float64(total-failures) / float64(total) * 100
		}

		rateColor := color.GreenString
		if successRate < 50 {
			rateColor = color.RedString
		} else if successRate < 80 {
			rateColor = color.YellowString
		}

		fmt.Printf("%s  Runs: %d  Failures: %s  Success Rate: %s\n\n",
			color.CyanString("Heartbeat Summary"),
			total,
			color.RedString("%d", failures),
			rateColor("%.0f%%", successRate),
		)

		if total == 0 {
			fmt.Println("No heartbeat runs found.")
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Time", "Agent", "Action", "Detail"})
		table.SetBorder(false)
		table.SetColumnSeparator(" ")

		for _, a := range activities {
			agentName := a.AgentName
			if agentName == "" {
				agentName = "-"
			}
			action := a.Action
			if strings.Contains(strings.ToLower(action), "fail") ||
				strings.Contains(strings.ToLower(action), "error") {
				action = color.RedString(action)
			} else {
				action = color.GreenString(action)
			}
			table.Append([]string{
				a.CreatedAt.Local().Format("01-02 15:04"),
				agentName,
				action,
				truncateStr(a.Detail, 50),
			})
		}
		table.Render()
		return nil
	},
}

// --- lw agent ci-report ---

var (
	ciReportWindow string
	ciReportRepo   string
)

// monitoredRepos are the GitHub repos checked by ci-report.
// Matches the routing table in scripts/ci/ci-monitor.sh.
var monitoredRepos = []string{
	"lightwave-media/lightwave-platform",
	"lightwave-media/lightwave-sys",
	"lightwave-media/lightwave-cli",
	"lightwave-media/lightwave-core",
	"lightwave-media/lightwave-ui",
}

// ghRun represents a GitHub Actions workflow run from `gh run list`.
type ghRun struct {
	DatabaseID  int       `json:"databaseId"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	HeadBranch  string    `json:"headBranch"`
	HeadSha     string    `json:"headSha"`
	CreatedAt   time.Time `json:"createdAt"`
	DisplayName string    `json:"displayTitle"`
}

// ciAgentStats aggregates CI runs per agent per repo.
type ciAgentStats struct {
	Agent   string `json:"agent"`
	Repo    string `json:"repo"`
	Runs    int    `json:"runs"`
	Fail    int    `json:"failures"`
	Rate    string `json:"failure_rate"`
	Retries int    `json:"retry_loops"`
	EstMin  int    `json:"est_minutes_burned"`
}

// ciReport is the full CI report structure for JSON output.
type ciReport struct {
	Window     string         `json:"window"`
	Stats      []ciAgentStats `json:"stats"`
	TotalRuns  int            `json:"total_runs"`
	TotalFails int            `json:"total_failures"`
	FailRate   string         `json:"failure_rate"`
	EstMinutes int            `json:"est_minutes_burned"`
	RetryLoops []retryLoop    `json:"retry_loops,omitempty"`
}

type retryLoop struct {
	Agent    string `json:"agent"`
	Repo     string `json:"repo"`
	Workflow string `json:"workflow"`
	Runs     int    `json:"runs"`
	Window   string `json:"window"`
}

var agentCIReportCmd = &cobra.Command{
	Use:   "ci-report",
	Short: "Cross-reference Paperclip activity with GitHub CI failures",
	Long: `Correlate agent activity with GitHub Actions failures to identify
which agents are burning CI minutes and detect retry loops.

Examples:
  lw agent ci-report
  lw agent ci-report --window 48h
  lw agent ci-report --repo lightwave-media/lightwave-sys
  lw agent ci-report --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		window, err := time.ParseDuration(ciReportWindow)
		if err != nil {
			return fmt.Errorf("invalid --window: %w", err)
		}
		cutoff := time.Now().Add(-window)

		repos := monitoredRepos
		if ciReportRepo != "" {
			repos = []string{ciReportRepo}
		}

		// Fetch GitHub runs for each repo
		var allRuns []struct {
			Repo string
			Run  ghRun
		}
		for _, repo := range repos {
			runs, err := fetchGHRuns(repo, 50)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not fetch runs for %s: %v\n", repo, err)
				continue
			}
			for _, r := range runs {
				if r.CreatedAt.After(cutoff) {
					allRuns = append(allRuns, struct {
						Repo string
						Run  ghRun
					}{Repo: repo, Run: r})
				}
			}
		}

		// Build stats grouped by agent+repo
		type key struct{ agent, repo string }
		statsMap := map[key]*ciAgentStats{}

		for _, entry := range allRuns {
			agent := branchToAgent(entry.Run.HeadBranch)
			k := key{agent: agent, repo: entry.Repo}
			st, ok := statsMap[k]
			if !ok {
				st = &ciAgentStats{Agent: agent, Repo: entry.Repo}
				statsMap[k] = st
			}
			st.Runs++
			if entry.Run.Conclusion == "failure" {
				st.Fail++
			}
		}

		// Finalize stats
		var stats []ciAgentStats
		totalRuns, totalFails, totalMinutes := 0, 0, 0
		for _, st := range statsMap {
			if st.Runs > 0 {
				st.Rate = fmt.Sprintf("%.0f%%", float64(st.Fail)/float64(st.Runs)*100)
			}
			st.EstMin = st.Runs * 2 // ~2 min per run estimate
			st.Retries = detectRetryCount(allRuns, st.Agent, st.Repo)
			totalRuns += st.Runs
			totalFails += st.Fail
			totalMinutes += st.EstMin
			stats = append(stats, *st)
		}

		sort.Slice(stats, func(i, j int) bool { return stats[i].Fail > stats[j].Fail })

		// Detect retry loops
		loops := detectRetryLoops(allRuns, cutoff)

		totalRate := "0%"
		if totalRuns > 0 {
			totalRate = fmt.Sprintf("%.0f%%", float64(totalFails)/float64(totalRuns)*100)
		}

		report := ciReport{
			Window:     ciReportWindow,
			Stats:      stats,
			TotalRuns:  totalRuns,
			TotalFails: totalFails,
			FailRate:   totalRate,
			EstMinutes: totalMinutes,
			RetryLoops: loops,
		}

		if agentJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}

		// Render table
		fmt.Printf("%s (last %s)\n\n", color.CyanString("CI Report"), ciReportWindow)

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Agent", "Repo", "Runs", "Fail", "Rate", "Retries", "Est Min"})
		table.SetBorder(false)
		table.SetColumnSeparator(" ")

		for _, st := range stats {
			rate := st.Rate
			if st.Fail > 0 {
				rate = color.RedString(st.Rate)
			}
			// Shorten repo name for display
			repoShort := strings.TrimPrefix(st.Repo, "lightwave-media/")
			table.Append([]string{
				st.Agent,
				repoShort,
				fmt.Sprintf("%d", st.Runs),
				fmt.Sprintf("%d", st.Fail),
				rate,
				fmt.Sprintf("%d", st.Retries),
				fmt.Sprintf("%d", st.EstMin),
			})
		}
		table.Render()

		fmt.Printf("\nTotal: %d runs, %d failures (%s), ~%d minutes burned\n",
			totalRuns, totalFails, totalRate, totalMinutes)

		if len(loops) > 0 {
			fmt.Printf("\n%s\n", color.RedString("Retry Loops Detected:"))
			for _, l := range loops {
				fmt.Printf("  %s on %s/%s: %d runs in %s\n",
					color.CyanString(l.Agent),
					l.Repo, l.Workflow,
					l.Runs, l.Window)
			}
		}

		return nil
	},
}

// fetchGHRuns shells out to `gh run list` for a repo.
func fetchGHRuns(repo string, limit int) ([]ghRun, error) {
	cmd := exec.Command("gh", "run", "list",
		"--repo", repo,
		"--limit", fmt.Sprintf("%d", limit),
		"--json", "databaseId,name,status,conclusion,headBranch,headSha,createdAt,displayTitle",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh run list for %s: %w", repo, err)
	}
	var runs []ghRun
	if err := json.Unmarshal(out, &runs); err != nil {
		return nil, fmt.Errorf("parse gh output for %s: %w", repo, err)
	}
	return runs, nil
}

// branchToAgent maps a branch name to an agent display name.
// Agents use branches like "lw/backend-engineer" or "feature/backend-engineer-xyz".
func branchToAgent(branch string) string {
	branch = strings.ToLower(branch)

	// Direct mapping: lw/{agent-name} branches
	if name, ok := strings.CutPrefix(branch, "lw/"); ok {
		return kebabToDisplay(name)
	}

	// feature/{agent-name}-{description} or fix/{agent-name}-{description}
	for _, prefix := range []string{"feature/", "fix/", "hotfix/"} {
		if rest, ok := strings.CutPrefix(branch, prefix); ok {
			// Try to match known agent patterns
			knownAgents := []string{
				"backend-engineer", "frontend-engineer", "infrastructure-engineer",
				"release-engineer", "qa-engineer", "cto", "general-manager",
			}
			for _, agent := range knownAgents {
				if strings.HasPrefix(rest, agent) {
					return kebabToDisplay(agent)
				}
			}
		}
	}

	if branch == "main" || branch == "master" {
		return "(direct push)"
	}

	return "(unattributed)"
}

// kebabToDisplay converts "backend-engineer" to "Backend Engineer".
func kebabToDisplay(s string) string {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// detectRetryCount counts how many times the same agent pushed to the same repo
// with failures in rapid succession (within 1 hour windows).
func detectRetryCount(allRuns []struct {
	Repo string
	Run  ghRun
}, agent, repo string) int {
	retries := 0
	var failTimes []time.Time
	for _, entry := range allRuns {
		if entry.Repo == repo && branchToAgent(entry.Run.HeadBranch) == agent &&
			entry.Run.Conclusion == "failure" {
			failTimes = append(failTimes, entry.Run.CreatedAt)
		}
	}
	sort.Slice(failTimes, func(i, j int) bool { return failTimes[i].Before(failTimes[j]) })

	// Count consecutive failures within 1-hour windows
	for i := 1; i < len(failTimes); i++ {
		if failTimes[i].Sub(failTimes[i-1]) < time.Hour {
			retries++
		}
	}
	return retries
}

// detectRetryLoops finds cases where the same agent+repo+workflow has 3+ runs
// within a 1-hour window, all failed.
func detectRetryLoops(allRuns []struct {
	Repo string
	Run  ghRun
}, cutoff time.Time) []retryLoop {
	type groupKey struct{ agent, repo, workflow string }
	groups := map[groupKey][]ghRun{}

	for _, entry := range allRuns {
		if entry.Run.CreatedAt.Before(cutoff) {
			continue
		}
		agent := branchToAgent(entry.Run.HeadBranch)
		k := groupKey{agent: agent, repo: entry.Repo, workflow: entry.Run.Name}
		groups[k] = append(groups[k], entry.Run)
	}

	var loops []retryLoop
	for k, runs := range groups {
		if len(runs) < 3 {
			continue
		}
		// Check if there's a 1-hour window with 3+ failures
		sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })

		failures := 0
		for _, r := range runs {
			if r.Conclusion == "failure" {
				failures++
			}
		}
		if failures < 3 {
			continue
		}

		span := runs[len(runs)-1].CreatedAt.Sub(runs[0].CreatedAt)
		loops = append(loops, retryLoop{
			Agent:    k.agent,
			Repo:     k.repo,
			Workflow: k.workflow,
			Runs:     len(runs),
			Window:   span.Truncate(time.Minute).String(),
		})
	}

	sort.Slice(loops, func(i, j int) bool { return loops[i].Runs > loops[j].Runs })
	return loops
}

func init() {
	// logs flags
	agentLogsCmd.Flags().IntVar(&agentLogsLimit, "limit", 25, "Maximum entries to show")
	agentLogsCmd.Flags().StringVar(&agentLogsType, "type", "", "Filter by entity type")

	// runs flags
	agentRunsCmd.Flags().IntVar(&agentRunsLimit, "limit", 20, "Maximum entries to show")
	agentRunsCmd.Flags().BoolVar(&agentRunsFailOnly, "failures-only", false, "Show only failed runs")

	// ci-report flags
	agentCIReportCmd.Flags().StringVar(&ciReportWindow, "window", "24h", "Time window to analyze")
	agentCIReportCmd.Flags().StringVar(&ciReportRepo, "repo", "", "Filter to a single repo")

	// Register with parent agent command
	agentCmd.AddCommand(agentPauseCmd)
	agentCmd.AddCommand(agentResumeCmd)
	agentCmd.AddCommand(agentInvokeCmd)
	agentCmd.AddCommand(agentLogsCmd)
	agentCmd.AddCommand(agentRunsCmd)
	agentCmd.AddCommand(agentCIReportCmd)
}
