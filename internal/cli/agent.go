package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/paperclip"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var agentJSON bool

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage Paperclip AI agents",
	Long: `Manage agents across Paperclip companies (lightwave-engineering, lightwave-operations).

Paperclip calls work items "issues" — this CLI uses "task" for consistency with lw task.`,
}

// --- lw agent list ---

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all agents across companies",
	Long: `List all Paperclip agents grouped by company.

Examples:
  lw agent list
  lw agent list --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := paperclip.NewClient()

		agents, err := client.ListAllAgents(ctx)
		if err != nil {
			return err
		}

		if agentJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(agents)
		}

		if len(agents) == 0 {
			fmt.Println("No agents found. Is Paperclip running?")
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Company", "Agent", "Status", "Current Task"})
		table.SetBorder(false)
		table.SetColumnSeparator(" ")

		for _, a := range agents {
			status := agentStatusColor(a.Status)
			currentTask := a.CurrentIssue
			if currentTask == "" {
				currentTask = "-"
			}
			table.Append([]string{a.CompanyName, a.Name, status, truncateStr(currentTask, 50)})
		}
		table.Render()
		return nil
	},
}

// --- lw agent status [agent-name] ---

var agentStatusCmd = &cobra.Command{
	Use:   "status [agent-name]",
	Short: "Show agent status and current task",
	Long: `Show detailed status for one agent, or a summary of all agents.

Examples:
  lw agent status
  lw agent status backend-engineer
  lw agent status backend-engineer --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := paperclip.NewClient()

		agents, err := client.ListAllAgents(ctx)
		if err != nil {
			return err
		}

		if len(args) > 0 {
			name := args[0]
			for _, a := range agents {
				if a.Name == name {
					return printAgentDetail(a)
				}
			}
			return fmt.Errorf("agent %q not found", name)
		}

		// No name given — show summary of all agents
		if agentJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(agents)
		}

		active := 0
		idle := 0
		errored := 0
		for _, a := range agents {
			switch a.Status {
			case "active", "working", "running":
				active++
			case "idle":
				idle++
			default:
				errored++
			}
		}
		fmt.Printf("%s  Active: %s  Idle: %s  Error: %s  Total: %d\n",
			color.CyanString("Agent Summary"),
			color.GreenString("%d", active),
			color.YellowString("%d", idle),
			color.RedString("%d", errored),
			len(agents),
		)
		return nil
	},
}

func printAgentDetail(a paperclip.Agent) error {
	if agentJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(a)
	}

	fmt.Printf("%s  %s\n", color.CyanString("Agent:"), a.Name)
	fmt.Printf("%s %s\n", color.CyanString("Company:"), a.CompanyName)
	fmt.Printf("%s  %s\n", color.CyanString("Status:"), agentStatusColor(a.Status))

	if a.CurrentIssue != "" {
		fmt.Printf("%s    %s\n", color.CyanString("Task:"), a.CurrentIssue)
	}
	if !a.LastHeartbeat.IsZero() {
		ago := time.Since(a.LastHeartbeat).Truncate(time.Second)
		fmt.Printf("%s %s (%s ago)\n", color.CyanString("Heartbeat:"), a.LastHeartbeat.Format(time.RFC3339), ago)
	}
	return nil
}

// --- lw agent assign <agent> <description> ---

var agentAssignCmd = &cobra.Command{
	Use:   "assign <agent-name> <description>",
	Short: "Assign a task to an agent",
	Long: `Create a Paperclip issue (work item) assigned to a specific agent.

The CLI uses "task" but creates a Paperclip "issue" under the hood.

Examples:
  lw agent assign backend-engineer "Add pagination to /v1/users endpoint"
  lw agent assign frontend-engineer "Fix dashboard layout on mobile" --json`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := paperclip.NewClient()
		agentName := args[0]
		description := args[1]

		// Find the agent to get the company ID
		agents, err := client.ListAllAgents(ctx)
		if err != nil {
			return err
		}

		var target *paperclip.Agent
		for _, a := range agents {
			if a.Name == agentName {
				target = &a
				break
			}
		}
		if target == nil {
			return fmt.Errorf("agent %q not found", agentName)
		}

		issue := paperclip.Issue{
			Title:     description,
			AgentName: agentName,
			AgentID:   target.ID,
		}

		created, err := client.CreateIssue(ctx, target.CompanyID, issue)
		if err != nil {
			return err
		}

		if agentJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(created)
		}

		fmt.Printf("%s Task assigned to %s\n", color.GreenString("✓"), color.CyanString(agentName))
		fmt.Printf("  ID: %s\n", created.ID)
		fmt.Printf("  Title: %s\n", created.Title)
		return nil
	},
}

// --- lw agent sync ---

var agentSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Trigger manual status sync from Paperclip",
	Long: `Fetch latest issue statuses from Paperclip and display a summary.
This is the CLI equivalent of the Celery Beat polling task.

Examples:
  lw agent sync
  lw agent sync --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := paperclip.NewClient()

		companies, err := client.ListCompanies(ctx)
		if err != nil {
			return err
		}

		type syncResult struct {
			Company string            `json:"company"`
			Issues  []paperclip.Issue `json:"issues"`
		}
		var results []syncResult

		for _, co := range companies {
			issues, err := client.ListIssues(ctx, co.ID)
			if err != nil {
				return fmt.Errorf("sync %s: %w", co.Name, err)
			}
			results = append(results, syncResult{Company: co.Name, Issues: issues})
		}

		if agentJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(results)
		}

		for _, r := range results {
			fmt.Printf("%s  %d issues\n", color.CyanString(r.Company+":"), len(r.Issues))

			// Count by status
			statusCounts := map[string]int{}
			for _, iss := range r.Issues {
				statusCounts[iss.Status]++
			}
			for status, count := range statusCounts {
				fmt.Printf("  %s: %d\n", status, count)
			}
		}
		return nil
	},
}

// --- lw agent cost [agent-name] ---

var agentCostCmd = &cobra.Command{
	Use:   "cost [agent-name]",
	Short: "Show per-agent cost breakdown",
	Long: `Display cost tracking data from Paperclip per agent.

Examples:
  lw agent cost
  lw agent cost backend-engineer
  lw agent cost --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		client := paperclip.NewClient()

		companies, err := client.ListCompanies(ctx)
		if err != nil {
			return err
		}

		// Aggregate costs from all issues per agent
		type agentCost struct {
			AgentName string  `json:"agent_name"`
			Company   string  `json:"company"`
			Tasks     int     `json:"tasks"`
			TotalCost float64 `json:"total_cost"`
		}
		costMap := map[string]*agentCost{}

		for _, co := range companies {
			issues, err := client.ListIssues(ctx, co.ID)
			if err != nil {
				return fmt.Errorf("cost %s: %w", co.Name, err)
			}
			for _, iss := range issues {
				name := iss.AgentName
				if name == "" {
					name = "(unassigned)"
				}
				key := co.Name + "/" + name
				ac, ok := costMap[key]
				if !ok {
					ac = &agentCost{AgentName: name, Company: co.Name}
					costMap[key] = ac
				}
				ac.Tasks++
				ac.TotalCost += iss.Cost
			}
		}

		// Filter if agent name specified
		var costs []*agentCost
		for _, ac := range costMap {
			if len(args) > 0 && ac.AgentName != args[0] {
				continue
			}
			costs = append(costs, ac)
		}

		if agentJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(costs)
		}

		if len(costs) == 0 {
			if len(args) > 0 {
				fmt.Printf("No cost data for agent %q\n", args[0])
			} else {
				fmt.Println("No cost data available")
			}
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Company", "Agent", "Tasks", "Total Cost"})
		table.SetBorder(false)
		table.SetColumnSeparator(" ")

		var totalCost float64
		for _, ac := range costs {
			table.Append([]string{
				ac.Company,
				ac.AgentName,
				fmt.Sprintf("%d", ac.Tasks),
				fmt.Sprintf("$%.2f", ac.TotalCost),
			})
			totalCost += ac.TotalCost
		}
		table.Render()
		fmt.Printf("\n%s $%.2f\n", color.CyanString("Total:"), totalCost)
		return nil
	},
}

// --- helpers ---

func agentStatusColor(status string) string {
	switch strings.ToLower(status) {
	case "active", "working", "running":
		return color.GreenString(status)
	case "idle":
		return color.YellowString(status)
	case "error", "failed":
		return color.RedString(status)
	default:
		return status
	}
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func init() {
	// Global --json flag for agent subcommands
	agentCmd.PersistentFlags().BoolVar(&agentJSON, "json", false, "Output in JSON format")

	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentStatusCmd)
	agentCmd.AddCommand(agentAssignCmd)
	agentCmd.AddCommand(agentSyncCmd)
	agentCmd.AddCommand(agentCostCmd)
}
