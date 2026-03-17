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

var epicCmd = &cobra.Command{
	Use:   "epic",
	Short: "Epic management commands",
	Long:  `Manage createOS epics - list epics with task counts.`,
}

// Flags for epic list
var (
	epicListStatus string
	epicListLimit  int
	epicListFormat string
)

// Flags for epic create
var (
	epicCreateName     string
	epicCreateStatus   string
	epicCreatePriority string
)

// Flags for epic update
var (
	epicUpdateStatus   string
	epicUpdateName     string
	epicUpdatePriority string
)

var epicCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new epic",
	Long: `Create a new epic in createOS.

Examples:
  lw epic create --name="LightWave Platform Q1"
  lw epic create --name="LightWave Platform Q1" --status=active --priority=p1_urgent`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if epicCreateName == "" {
			return fmt.Errorf("--name is required")
		}

		ctx := context.Background()
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		status := epicCreateStatus
		if status == "" {
			status = "active"
		}

		opts := db.EpicCreateOptions{
			Name:     epicCreateName,
			Status:   status,
			Priority: epicCreatePriority,
		}

		epic, err := db.CreateEpic(ctx, pool, opts)
		if err != nil {
			return err
		}

		fmt.Printf("Created epic %s: %s\n", color.YellowString(epic.ShortID), epic.Name)
		return nil
	},
}

var epicListCmd = &cobra.Command{
	Use:   "list",
	Short: "List epics",
	Long: `List epics with optional filters.

Examples:
  lw epic list
  lw epic list --status=active
  lw epic list --limit=10
  lw epic list --format=ids`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		opts := db.EpicListOptions{
			Status: epicListStatus,
			Limit:  epicListLimit,
		}

		epics, err := db.ListEpics(ctx, pool, opts)
		if err != nil {
			return err
		}

		if len(epics) == 0 {
			if epicListFormat == "ids" {
				return nil // Silent for scripting
			}
			fmt.Println(color.YellowString("No epics found matching filters"))
			return nil
		}

		if epicListFormat == "ids" {
			for _, e := range epics {
				fmt.Println(e.ID)
			}
			return nil
		}

		printEpicTable(epics)
		return nil
	},
}

var epicUpdateCmd = &cobra.Command{
	Use:   "update [epic-id]",
	Short: "Update an epic",
	Long: `Update epic fields by short ID prefix.

Examples:
  lw epic update a1b2 --status=completed
  lw epic update a1b2 --name="Updated epic" --priority=p2_high`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		opts := db.EpicUpdateOptions{}
		if cmd.Flags().Changed("status") {
			opts.Status = &epicUpdateStatus
		}
		if cmd.Flags().Changed("name") {
			opts.Name = &epicUpdateName
		}
		if cmd.Flags().Changed("priority") {
			opts.Priority = &epicUpdatePriority
		}

		epic, err := db.UpdateEpic(ctx, pool, args[0], opts)
		if err != nil {
			return err
		}

		fmt.Printf("Updated epic %s\n", color.YellowString(epic.ShortID))
		return nil
	},
}

func init() {
	// epic create flags
	epicCreateCmd.Flags().StringVar(&epicCreateName, "name", "", "Epic name (required)")
	epicCreateCmd.Flags().StringVar(&epicCreateStatus, "status", "active", "Status (active, completed, planned)")
	epicCreateCmd.Flags().StringVar(&epicCreatePriority, "priority", "", "Priority (p1_urgent, p2_high, p3_medium, p4_low)")

	// epic list flags
	epicListCmd.Flags().StringVarP(&epicListStatus, "status", "s", "", "Filter by status (active, completed, planned)")
	epicListCmd.Flags().IntVarP(&epicListLimit, "limit", "n", 50, "Limit number of results")
	epicListCmd.Flags().StringVar(&epicListFormat, "format", "table", "Output format (table, ids)")

	// epic update flags
	epicUpdateCmd.Flags().StringVar(&epicUpdateStatus, "status", "", "Status (active, completed, planned)")
	epicUpdateCmd.Flags().StringVar(&epicUpdateName, "name", "", "Epic name")
	epicUpdateCmd.Flags().StringVar(&epicUpdatePriority, "priority", "", "Priority (p1_urgent, p2_high, p3_medium, p4_low)")

	// Add subcommands
	epicCmd.AddCommand(epicCreateCmd)
	epicCmd.AddCommand(epicListCmd)
	epicCmd.AddCommand(epicUpdateCmd)
}

func printEpicTable(epics []db.Epic) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ID", "Name", "Status", "Priority", "Repo", "Tasks"})
	table.SetBorder(false)
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
	)

	for _, e := range epics {
		name := e.Name
		if len(name) > 40 {
			name = name[:37] + "..."
		}

		repo := "-"
		if e.GithubRepo != nil && *e.GithubRepo != "" {
			repo = *e.GithubRepo
			if len(repo) > 30 {
				repo = repo[:27] + "..."
			}
		}

		priority := "-"
		if e.Priority != nil {
			priority = *e.Priority
		}

		table.Rich([]string{
			e.ShortID,
			name,
			e.Status,
			priority,
			repo,
			fmt.Sprintf("%d", e.TaskCount),
		}, []tablewriter.Colors{
			{tablewriter.FgYellowColor},
			{},
			getSprintStatusColor(e.Status),
			getPriorityColor(priority),
			{},
			{tablewriter.FgCyanColor},
		})
	}

	table.Render()
	fmt.Printf("\n%s epics\n", color.CyanString("%d", len(epics)))
}
