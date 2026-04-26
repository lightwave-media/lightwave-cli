package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/paperclip"
	"github.com/lightwave-media/lightwave-cli/internal/version"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

// PaperclipAPIVersion is the contract version of the `lw paperclip` subtree.
// Bump on any breaking change: subcommand rename/removal, positional-arg
// reshuffle, --flag rename, or output-schema change. Consumers (plugins,
// scripts) pin a minimum value and refuse to run when below.
const PaperclipAPIVersion = 1

var (
	paperclipJSON      bool
	paperclipCompanyID string
	paperclipAPIBase   string
	paperclipProfile   string
)

var paperclipCmd = &cobra.Command{
	Use:   "paperclip",
	Short: "Manage paperclipai entities not covered by the upstream CLI",
	Long: `Wrappers around paperclipai HTTP endpoints (localhost:3100 by default)
that the official paperclipai CLI does not expose: labels, projects, goals.

Defaults --company-id from ~/.paperclip/context.json (current profile).

Note: a 'reviewer' subcommand is intentionally omitted. The /api/issues/{id}/reviewers
endpoint does not exist in the running paperclip server, and no reviewers_updated
event was found in the activity log. The closest concept is executionPolicy stages
of type "review" with participants — manage those via 'paperclipai issue update'.`,
}

func init() {
	version.RegisterAPI("paperclip", PaperclipAPIVersion)
	rootCmd.AddCommand(paperclipCmd)

	paperclipCmd.PersistentFlags().BoolVar(&paperclipJSON, "json", false, "output JSON")
	paperclipCmd.PersistentFlags().StringVar(&paperclipCompanyID, "company-id", "", "paperclipai company ID (default: current profile in ~/.paperclip/context.json)")
	paperclipCmd.PersistentFlags().StringVar(&paperclipAPIBase, "api-base", "", "paperclipai API base URL (default: $PAPERCLIP_URL or http://localhost:3100)")
	paperclipCmd.PersistentFlags().StringVar(&paperclipProfile, "profile", "", "paperclipai profile name (default: currentProfile in context.json)")

	paperclipLabelCmd.AddCommand(paperclipLabelAddCmd)
	paperclipLabelCmd.AddCommand(paperclipLabelRemoveCmd)
	paperclipLabelCmd.AddCommand(paperclipLabelListCmd)
	paperclipCmd.AddCommand(paperclipLabelCmd)

	paperclipProjectCmd.AddCommand(paperclipProjectCreateCmd)
	paperclipProjectCmd.AddCommand(paperclipProjectListCmd)
	paperclipCmd.AddCommand(paperclipProjectCmd)

	paperclipGoalCmd.AddCommand(paperclipGoalCreateCmd)
	paperclipGoalCmd.AddCommand(paperclipGoalListCmd)
	paperclipCmd.AddCommand(paperclipGoalCmd)

	paperclipCmd.AddCommand(paperclipCommentCmd)
	paperclipCmd.AddCommand(paperclipReassignCmd)
	paperclipCmd.AddCommand(paperclipCancelCmd)
}

// resolveCompanyID picks --company-id, otherwise reads the active profile binding.
func resolveCompanyID() (string, error) {
	if paperclipCompanyID != "" {
		return paperclipCompanyID, nil
	}
	ctx, err := paperclip.LoadContext()
	if err != nil {
		return "", err
	}
	id := ctx.ResolveCompanyID(paperclipProfile)
	if id == "" {
		profile := paperclipProfile
		if profile == "" {
			profile = ctx.CurrentProfile
		}
		return "", fmt.Errorf("no companyId bound to profile %q in ~/.paperclip/context.json — pass --company-id", profile)
	}
	return id, nil
}

func newPaperclipClient() *paperclip.Client {
	if paperclipAPIBase != "" {
		os.Setenv("PAPERCLIP_URL", paperclipAPIBase)
	}
	return paperclip.NewClient()
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// --- label ---

var paperclipLabelCmd = &cobra.Command{
	Use:   "label",
	Short: "Manage labels on paperclip issues",
}

var paperclipLabelAddCmd = &cobra.Command{
	Use:   "add <issueId|identifier> <labelName>",
	Short: "Add a label to an issue (label must already exist on the company)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		issueID, labelName := args[0], args[1]
		companyID, err := resolveCompanyID()
		if err != nil {
			return err
		}
		ctx := context.Background()
		client := newPaperclipClient()

		label, err := client.FindLabelByName(ctx, companyID, labelName)
		if err != nil {
			return err
		}
		ids, err := client.AddIssueLabel(ctx, issueID, label.ID)
		if err != nil {
			return err
		}
		if paperclipJSON {
			return emitJSON(map[string]any{"issueId": issueID, "labelIds": ids, "added": label.Name})
		}
		fmt.Printf("%s Added label %s to %s\n", color.GreenString("✓"), color.CyanString(label.Name), issueID)
		return nil
	},
}

var paperclipLabelRemoveCmd = &cobra.Command{
	Use:   "remove <issueId|identifier> <labelName>",
	Short: "Remove a label from an issue",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		issueID, labelName := args[0], args[1]
		companyID, err := resolveCompanyID()
		if err != nil {
			return err
		}
		ctx := context.Background()
		client := newPaperclipClient()

		label, err := client.FindLabelByName(ctx, companyID, labelName)
		if err != nil {
			return err
		}
		ids, err := client.RemoveIssueLabel(ctx, issueID, label.ID)
		if err != nil {
			return err
		}
		if paperclipJSON {
			return emitJSON(map[string]any{"issueId": issueID, "labelIds": ids, "removed": label.Name})
		}
		fmt.Printf("%s Removed label %s from %s\n", color.YellowString("✓"), color.CyanString(label.Name), issueID)
		return nil
	},
}

var paperclipLabelListCmd = &cobra.Command{
	Use:   "list",
	Short: "List labels defined on the active company",
	RunE: func(cmd *cobra.Command, args []string) error {
		companyID, err := resolveCompanyID()
		if err != nil {
			return err
		}
		ctx := context.Background()
		client := newPaperclipClient()

		labels, err := client.ListLabels(ctx, companyID)
		if err != nil {
			return err
		}
		sort.Slice(labels, func(i, j int) bool {
			return strings.ToLower(labels[i].Name) < strings.ToLower(labels[j].Name)
		})
		if paperclipJSON {
			return emitJSON(labels)
		}
		tw := tablewriter.NewWriter(os.Stdout)
		tw.SetHeader([]string{"NAME", "COLOR", "ID"})
		for _, l := range labels {
			tw.Append([]string{l.Name, l.Color, l.ID})
		}
		tw.Render()
		return nil
	},
}

// --- project ---

var paperclipProjectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage paperclip projects",
}

var paperclipProjectCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a project on the active company",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		desc, _ := cmd.Flags().GetString("description")
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("--name is required")
		}
		companyID, err := resolveCompanyID()
		if err != nil {
			return err
		}
		ctx := context.Background()
		client := newPaperclipClient()

		project, err := client.CreateProject(ctx, companyID, name, desc)
		if err != nil {
			return err
		}
		if paperclipJSON {
			return emitJSON(project)
		}
		fmt.Printf("%s Created project %s (%s)\n", color.GreenString("✓"), color.CyanString(project.Name), project.ID)
		return nil
	},
}

var paperclipProjectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects on the active company",
	RunE: func(cmd *cobra.Command, args []string) error {
		companyID, err := resolveCompanyID()
		if err != nil {
			return err
		}
		ctx := context.Background()
		client := newPaperclipClient()

		projects, err := client.ListProjects(ctx, companyID)
		if err != nil {
			return err
		}
		sort.Slice(projects, func(i, j int) bool {
			return strings.ToLower(projects[i].Name) < strings.ToLower(projects[j].Name)
		})
		if paperclipJSON {
			return emitJSON(projects)
		}
		tw := tablewriter.NewWriter(os.Stdout)
		tw.SetHeader([]string{"NAME", "STATUS", "ID"})
		for _, p := range projects {
			tw.Append([]string{p.Name, p.Status, p.ID})
		}
		tw.Render()
		return nil
	},
}

// --- goal ---

var paperclipGoalCmd = &cobra.Command{
	Use:   "goal",
	Short: "Manage paperclip goals",
}

var paperclipGoalCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a goal on the active company",
	RunE: func(cmd *cobra.Command, args []string) error {
		title, _ := cmd.Flags().GetString("title")
		desc, _ := cmd.Flags().GetString("description")
		if strings.TrimSpace(title) == "" {
			return fmt.Errorf("--title is required")
		}
		companyID, err := resolveCompanyID()
		if err != nil {
			return err
		}
		ctx := context.Background()
		client := newPaperclipClient()

		goal, err := client.CreateGoal(ctx, companyID, title, desc)
		if err != nil {
			return err
		}
		if paperclipJSON {
			return emitJSON(goal)
		}
		fmt.Printf("%s Created goal %s (%s)\n", color.GreenString("✓"), color.CyanString(goal.Title), goal.ID)
		return nil
	},
}

var paperclipGoalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List goals on the active company",
	RunE: func(cmd *cobra.Command, args []string) error {
		companyID, err := resolveCompanyID()
		if err != nil {
			return err
		}
		ctx := context.Background()
		client := newPaperclipClient()

		goals, err := client.ListGoals(ctx, companyID)
		if err != nil {
			return err
		}
		sort.Slice(goals, func(i, j int) bool {
			return strings.ToLower(goals[i].Title) < strings.ToLower(goals[j].Title)
		})
		if paperclipJSON {
			return emitJSON(goals)
		}
		tw := tablewriter.NewWriter(os.Stdout)
		tw.SetHeader([]string{"TITLE", "STATUS", "ID"})
		for _, g := range goals {
			tw.Append([]string{g.Title, g.Status, g.ID})
		}
		tw.Render()
		return nil
	},
}

func init() {
	paperclipProjectCreateCmd.Flags().String("name", "", "project name (required)")
	paperclipProjectCreateCmd.Flags().String("description", "", "project description")
	_ = paperclipProjectCreateCmd.MarkFlagRequired("name")

	paperclipGoalCreateCmd.Flags().String("title", "", "goal title (required)")
	paperclipGoalCreateCmd.Flags().String("description", "", "goal description")
	_ = paperclipGoalCreateCmd.MarkFlagRequired("title")

	paperclipCommentCmd.Flags().StringVar(&paperclipCommentAs, "as", "", "agent role posting the comment, e.g. frontend-engineer (required)")
	_ = paperclipCommentCmd.MarkFlagRequired("as")

	paperclipReassignCmd.Flags().StringVar(&paperclipReassignComment, "comment", "", "post a comment alongside the reassignment")
	paperclipCancelCmd.Flags().StringVar(&paperclipCancelComment, "comment", "", "post a comment explaining the cancellation")
	paperclipCancelCmd.Flags().BoolVar(&paperclipCancelYes, "yes", false, "skip confirmation prompt (CI/agent use)")
	paperclipCancelCmd.Flags().BoolVar(&paperclipCancelDryRun, "dry-run", false, "print intended change without calling the API")
}

// --- comment / reassign / cancel ---

var paperclipCommentAs string

var paperclipCommentCmd = &cobra.Command{
	Use:   "comment <issueId|identifier> <body>",
	Short: "Post a comment on an issue with agent attribution",
	Long: `Post a comment to a paperclip issue.

The --as flag is required and identifies the agent role posting the comment.
The body is prefixed with [from: <role>] so cross-team comments are visibly
attributed in the issue thread without grepping the activity log.

Examples:
  lw paperclip comment LIGA-43 "Misassigned — this is marketing, not frontend" --as frontend-engineer
  lw paperclip comment <uuid> "Updated status, see PR #123" --as backend-engineer`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		issueID, body := args[0], args[1]
		if strings.TrimSpace(paperclipCommentAs) == "" {
			return fmt.Errorf("--as <agent-role> is required for attribution")
		}
		attributedBody := fmt.Sprintf("[from: %s]\n\n%s", paperclipCommentAs, body)

		ctx := context.Background()
		client := newPaperclipClient()
		if err := client.AddIssueComment(ctx, issueID, attributedBody); err != nil {
			return err
		}
		if paperclipJSON {
			return emitJSON(map[string]any{"issueId": issueID, "posted": true, "as": paperclipCommentAs})
		}
		fmt.Printf("%s Comment posted to %s as %s\n", color.GreenString("✓"), color.CyanString(issueID), color.YellowString(paperclipCommentAs))
		return nil
	},
}

var paperclipReassignComment string

var paperclipReassignCmd = &cobra.Command{
	Use:   "reassign <issueId|identifier> <agent-name>",
	Short: "Reassign an issue to another agent",
	Long: `Reassign a paperclip issue by changing its assigneeAgentId.
Resolves agent name (kebab-case or display name) to an ID across all companies.

Examples:
  lw paperclip reassign LIGA-43 marketing-content-lead
  lw paperclip reassign LIGA-43 backend-engineer --comment "FE work blocked on this BE migration"`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		issueID, agentName := args[0], args[1]
		ctx := context.Background()
		client := newPaperclipClient()
		agent, err := resolveAgent(ctx, client, agentName)
		if err != nil {
			return err
		}
		fields := map[string]any{"assigneeAgentId": agent.ID}
		if paperclipReassignComment != "" {
			fields["comment"] = paperclipReassignComment
		}
		if err := client.UpdateIssue(ctx, issueID, fields); err != nil {
			return err
		}
		if paperclipJSON {
			return emitJSON(map[string]any{
				"issueId":         issueID,
				"assigneeAgentId": agent.ID,
				"agentName":       agent.Name,
			})
		}
		fmt.Printf("%s Reassigned %s to %s\n", color.GreenString("✓"), color.CyanString(issueID), color.CyanString(agent.Name))
		return nil
	},
}

var (
	paperclipCancelComment string
	paperclipCancelYes     bool
	paperclipCancelDryRun  bool
)

var paperclipCancelCmd = &cobra.Command{
	Use:   "cancel <issueId|identifier>",
	Short: "Cancel an issue (status=cancelled)",
	Long: `Cancel a paperclip issue. Sets status=cancelled.

Standard destructive-command pattern: --dry-run previews, --yes skips the prompt.

Examples:
  lw paperclip cancel LIGA-43 --dry-run
  lw paperclip cancel LIGA-43 --comment "Superseded by LIGA-91"
  lw paperclip cancel LIGA-43 --yes  # CI/agent — no prompt`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		issueID := args[0]
		fields := map[string]any{"status": "cancelled"}
		if paperclipCancelComment != "" {
			fields["comment"] = paperclipCancelComment
		}

		if paperclipCancelDryRun {
			if paperclipJSON {
				return emitJSON(map[string]any{"issueId": issueID, "dryRun": true, "fields": fields})
			}
			fmt.Printf("%s Would cancel %s (status=cancelled)", color.YellowString("dry-run:"), color.CyanString(issueID))
			if paperclipCancelComment != "" {
				fmt.Printf(" with comment: %q", paperclipCancelComment)
			}
			fmt.Println()
			return nil
		}

		if !paperclipCancelYes {
			fmt.Fprintf(os.Stderr, "Cancel issue %s? [y/N]: ", issueID)
			var resp string
			_, _ = fmt.Scanln(&resp)
			if !strings.EqualFold(strings.TrimSpace(resp), "y") {
				return fmt.Errorf("aborted")
			}
		}

		ctx := context.Background()
		client := newPaperclipClient()
		if err := client.UpdateIssue(ctx, issueID, fields); err != nil {
			return err
		}
		if paperclipJSON {
			return emitJSON(map[string]any{"issueId": issueID, "status": "cancelled"})
		}
		fmt.Printf("%s Cancelled %s\n", color.YellowString("✓"), color.CyanString(issueID))
		return nil
	},
}
