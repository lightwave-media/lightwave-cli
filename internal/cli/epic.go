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
)

var epicListCmd = &cobra.Command{
	Use:   "list",
	Short: "List epics",
	Long: `List epics with optional filters.

Examples:
  lw epic list
  lw epic list --status=active
  lw epic list --limit=10`,
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
			fmt.Println(color.YellowString("No epics found matching filters"))
			return nil
		}

		printEpicTable(epics)
		return nil
	},
}

func init() {
	// epic list flags
	epicListCmd.Flags().StringVarP(&epicListStatus, "status", "s", "", "Filter by status (active, completed, planned)")
	epicListCmd.Flags().IntVarP(&epicListLimit, "limit", "n", 50, "Limit number of results")

	// Add subcommands
	epicCmd.AddCommand(epicListCmd)
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
