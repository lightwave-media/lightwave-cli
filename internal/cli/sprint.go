package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var sprintCmd = &cobra.Command{
	Use:   "sprint",
	Short: "Sprint management commands",
	Long:  `Manage createOS sprints - list and create sprints.`,
}

// Flags for sprint list
var (
	sprintListStatus string
	sprintListEpic   string
	sprintListLimit  int
)

// Flags for sprint create
var (
	sprintCreateName       string
	sprintCreateObjectives string
	sprintCreateEpic       string
	sprintCreateStartDate  string
	sprintCreateEndDate    string
	sprintCreateStatus     string
)

// Flags for sprint update
var (
	sprintUpdateStatus     string
	sprintUpdateName       string
	sprintUpdateObjectives string
	sprintUpdateStartDate  string
	sprintUpdateEndDate    string
)

var sprintListCmd = &cobra.Command{
	Use:   "list",
	Short: "List sprints",
	Long: `List sprints with optional filters.

Examples:
  lw sprint list
  lw sprint list --status=active
  lw sprint list --epic=abc123 --limit=10`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		opts := db.SprintListOptions{
			Status: sprintListStatus,
			EpicID: sprintListEpic,
			Limit:  sprintListLimit,
		}

		sprints, err := db.ListSprints(ctx, pool, opts)
		if err != nil {
			return err
		}

		if len(sprints) == 0 {
			fmt.Println(color.YellowString("No sprints found matching filters"))
			return nil
		}

		printSprintTable(sprints)
		return nil
	},
}

var sprintCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new sprint",
	Long: `Create a new sprint in createOS.

Examples:
  lw sprint create --name="Sprint 5"
  lw sprint create --name="Sprint 5" --start-date=2026-03-10 --end-date=2026-03-24
  lw sprint create --name="Sprint 5" --epic=abc123 --status=active`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if sprintCreateName == "" {
			return fmt.Errorf("--name is required")
		}

		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		opts := db.SprintCreateOptions{
			Name:       sprintCreateName,
			Objectives: sprintCreateObjectives,
			EpicID:     sprintCreateEpic,
			StartDate:  sprintCreateStartDate,
			EndDate:    sprintCreateEndDate,
			Status:     sprintCreateStatus,
		}

		sprint, err := db.CreateSprint(ctx, pool, opts)
		if err != nil {
			return err
		}

		fmt.Printf("Created sprint %s: %s\n", color.YellowString(sprint.ShortID), sprint.Name)
		return nil
	},
}

var sprintUpdateCmd = &cobra.Command{
	Use:   "update [sprint-id]",
	Short: "Update a sprint",
	Long: `Update sprint fields by short ID prefix.

Examples:
  lw sprint update 74ce --status=completed
  lw sprint update 74ce --name="Sprint 2 (Final)" --objectives="Shipped"
  lw sprint update 74ce --start-date=2026-03-14 --end-date=2026-03-21`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		opts := db.SprintUpdateOptions{}
		if cmd.Flags().Changed("status") {
			opts.Status = &sprintUpdateStatus
		}
		if cmd.Flags().Changed("name") {
			opts.Name = &sprintUpdateName
		}
		if cmd.Flags().Changed("objectives") {
			opts.Objectives = &sprintUpdateObjectives
		}
		if cmd.Flags().Changed("start-date") {
			opts.StartDate = &sprintUpdateStartDate
		}
		if cmd.Flags().Changed("end-date") {
			opts.EndDate = &sprintUpdateEndDate
		}

		sprint, err := db.UpdateSprint(ctx, pool, args[0], opts)
		if err != nil {
			return err
		}

		fmt.Printf("Updated sprint %s\n", color.YellowString(sprint.ShortID))
		return nil
	},
}

func init() {
	// sprint list flags
	sprintListCmd.Flags().StringVarP(&sprintListStatus, "status", "s", "", "Filter by status (active, completed, planned)")
	sprintListCmd.Flags().StringVar(&sprintListEpic, "epic", "", "Filter by epic ID")
	sprintListCmd.Flags().IntVarP(&sprintListLimit, "limit", "n", 50, "Limit number of results")

	// sprint create flags
	sprintCreateCmd.Flags().StringVar(&sprintCreateName, "name", "", "Sprint name (required)")
	sprintCreateCmd.Flags().StringVar(&sprintCreateObjectives, "objectives", "", "Sprint objectives")
	sprintCreateCmd.Flags().StringVar(&sprintCreateEpic, "epic", "", "Epic ID")
	sprintCreateCmd.Flags().StringVar(&sprintCreateStartDate, "start-date", "", "Start date (YYYY-MM-DD)")
	sprintCreateCmd.Flags().StringVar(&sprintCreateEndDate, "end-date", "", "End date (YYYY-MM-DD)")
	sprintCreateCmd.Flags().StringVar(&sprintCreateStatus, "status", "planned", "Status (active, completed, planned)")

	// sprint update flags
	sprintUpdateCmd.Flags().StringVar(&sprintUpdateStatus, "status", "", "Status (active, completed, planned)")
	sprintUpdateCmd.Flags().StringVar(&sprintUpdateName, "name", "", "Sprint name")
	sprintUpdateCmd.Flags().StringVar(&sprintUpdateObjectives, "objectives", "", "Sprint objectives")
	sprintUpdateCmd.Flags().StringVar(&sprintUpdateStartDate, "start-date", "", "Start date (YYYY-MM-DD)")
	sprintUpdateCmd.Flags().StringVar(&sprintUpdateEndDate, "end-date", "", "End date (YYYY-MM-DD)")

	// Add subcommands
	sprintCmd.AddCommand(sprintListCmd)
	sprintCmd.AddCommand(sprintCreateCmd)
	sprintCmd.AddCommand(sprintUpdateCmd)
}

func printSprintTable(sprints []db.Sprint) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ID", "Name", "Status", "Start", "End"})
	table.SetBorder(false)
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
	)

	for _, s := range sprints {
		startDate := "-"
		if s.StartDate != nil {
			startDate = s.StartDate.Format("2006-01-02")
		}
		endDate := "-"
		if s.EndDate != nil {
			endDate = s.EndDate.Format("2006-01-02")
		}

		statusColor := getSprintStatusColor(s.Status)

		table.Rich([]string{
			s.ShortID,
			s.Name,
			s.Status,
			startDate,
			endDate,
		}, []tablewriter.Colors{
			{tablewriter.FgYellowColor},
			{},
			statusColor,
			{},
			{},
		})
	}

	table.Render()
	fmt.Printf("\n%s sprints\n", color.CyanString("%d", len(sprints)))
}

func getSprintStatusColor(status string) tablewriter.Colors {
	switch status {
	case "active":
		return tablewriter.Colors{tablewriter.FgGreenColor}
	case "planned":
		return tablewriter.Colors{tablewriter.FgYellowColor}
	case "completed":
		return tablewriter.Colors{tablewriter.FgHiBlackColor}
	default:
		return tablewriter.Colors{}
	}
}
