package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var lineageCmd = &cobra.Command{
	Use:   "lineage",
	Short: "Document lineage traceability",
	Long:  `Check and manage document lineage — trace tasks back through stories, epics, and upstream artifacts (PRD, SAD, NFR).`,
}

var lineageCheckCmd = &cobra.Command{
	Use:   "check [epic-id]",
	Short: "Check lineage completeness for an epic",
	Long: `Walk the Task → Story → Epic chain and report missing upstream documents.

Reports gaps where required documents (PRD, SAD, NFR) are missing for an epic.
If no epic-id is given, checks all active epics.

Examples:
  lw lineage check
  lw lineage check b902c1b4`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		if len(args) > 0 {
			return checkEpicLineage(ctx, pool, args[0])
		}
		return checkAllEpicsLineage(ctx, pool)
	},
}

func init() {
	lineageCmd.AddCommand(lineageCheckCmd)
}

func checkAllEpicsLineage(ctx context.Context, pool *pgxpool.Pool) error {
	epics, err := db.ListEpics(ctx, pool, db.EpicListOptions{Status: "active"})
	if err != nil {
		return err
	}

	if len(epics) == 0 {
		fmt.Println(color.YellowString("No active epics found"))
		return nil
	}

	allClean := true
	for _, epic := range epics {
		gaps, err := db.CheckLineage(ctx, pool, epic.ID)
		if err != nil {
			return fmt.Errorf("failed to check lineage for epic %s: %w", epic.ShortID, err)
		}
		if len(gaps) > 0 {
			allClean = false
			printLineageReport(epic, gaps)
		}
	}

	if allClean {
		fmt.Println(color.GreenString("All active epics have complete lineage"))
	}
	return nil
}

func checkEpicLineage(ctx context.Context, pool *pgxpool.Pool, epicID string) error {
	epic, err := db.GetEpic(ctx, pool, epicID)
	if err != nil {
		return err
	}

	gaps, err := db.CheckLineage(ctx, pool, epic.ID)
	if err != nil {
		return fmt.Errorf("failed to check lineage: %w", err)
	}

	if len(gaps) == 0 {
		fmt.Printf("Epic %s: %s\n", color.YellowString(epic.ShortID), epic.Name)
		fmt.Println(color.GreenString("  Complete lineage — all required documents present"))
		return nil
	}

	printLineageReport(*epic, gaps)
	return nil
}

func printLineageReport(epic db.Epic, gaps []db.LineageGap) {
	fmt.Printf("\nEpic %s: %s\n", color.YellowString(epic.ShortID), epic.Name)

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Document Type", "Status", "Severity", "Entity"})
	table.SetBorder(false)
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
	)

	for _, gap := range gaps {
		statusColor := tablewriter.Colors{}
		switch gap.Severity {
		case "required":
			statusColor = tablewriter.Colors{tablewriter.FgRedColor, tablewriter.Bold}
		case "recommended":
			statusColor = tablewriter.Colors{tablewriter.FgYellowColor}
		}

		table.Rich([]string{
			gap.DocumentType,
			gap.Status,
			gap.Severity,
			gap.EntityType + " " + gap.EntityShortID,
		}, []tablewriter.Colors{
			{},
			statusColor,
			statusColor,
			{tablewriter.FgCyanColor},
		})
	}

	table.Render()
	fmt.Printf("\n%s gaps found\n", color.RedString("%d", len(gaps)))
}
