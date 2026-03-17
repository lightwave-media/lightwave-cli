package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

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
	Long: `Walk the Task → Story → Epic chain and report missing or draft upstream documents.

Reports gaps where required documents (PRD, SAD, NFR) are missing or still in
draft for an epic. Requirements are loaded from SST config.yaml, not hardcoded.

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

var lineageFixCmd = &cobra.Command{
	Use:   "fix [epic-id]",
	Short: "Auto-create missing documents for an epic",
	Long: `Create draft documents for all missing lineage gaps in one shot.

Only creates documents for "missing" gaps — existing drafts are left alone.
If no epic-id is given, fixes all active epics.

Examples:
  lw lineage fix b902c1b4
  lw lineage fix`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		if len(args) > 0 {
			return fixEpicLineage(ctx, pool, args[0])
		}
		return fixAllEpicsLineage(ctx, pool)
	},
}

var lineageConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Show loaded lineage validation rules",
	Long:  `Display the lineage validation rules loaded from SST config.yaml.`,
	Run: func(cmd *cobra.Command, args []string) {
		lc := db.LoadLineageConfig()
		fmt.Println(color.CyanString("Lineage validation rules (from SST):"))
		fmt.Printf("  Epic requires:      %s\n", strings.Join(lc.EpicRequires, ", "))
		fmt.Printf("  Epic recommended:   %s\n", strings.Join(lc.EpicRecommended, ", "))
		fmt.Printf("  DDD task threshold: %d tasks\n", lc.TaskThreshold)
		fmt.Printf("  Sprint blockers:    %s\n", strings.Join(lc.SprintBlockers, ", "))
	},
}

func init() {
	lineageCmd.AddCommand(lineageCheckCmd)
	lineageCmd.AddCommand(lineageFixCmd)
	lineageCmd.AddCommand(lineageConfigCmd)
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
		fmt.Println(color.GreenString("  Complete lineage — all documents present and beyond draft"))
		return nil
	}

	printLineageReport(*epic, gaps)
	return nil
}

func fixEpicLineage(ctx context.Context, pool *pgxpool.Pool, epicID string) error {
	epic, err := db.GetEpic(ctx, pool, epicID)
	if err != nil {
		return err
	}

	created, err := db.FixLineage(ctx, pool, epic.ID)
	if err != nil {
		return fmt.Errorf("failed to fix lineage: %w", err)
	}

	if len(created) == 0 {
		fmt.Printf("Epic %s: %s\n", color.YellowString(epic.ShortID), epic.Name)
		fmt.Println(color.GreenString("  No missing documents — nothing to create"))
		return nil
	}

	fmt.Printf("Epic %s: %s\n", color.YellowString(epic.ShortID), epic.Name)
	for _, doc := range created {
		fmt.Printf("  %s Created %s: %s\n",
			color.GreenString("+"),
			color.CyanString(strings.ToUpper(doc.Category)),
			doc.Title)
	}
	fmt.Printf("\n%s documents created\n", color.GreenString("%d", len(created)))
	return nil
}

func fixAllEpicsLineage(ctx context.Context, pool *pgxpool.Pool) error {
	epics, err := db.ListEpics(ctx, pool, db.EpicListOptions{Status: "active"})
	if err != nil {
		return err
	}

	if len(epics) == 0 {
		fmt.Println(color.YellowString("No active epics found"))
		return nil
	}

	totalCreated := 0
	for _, epic := range epics {
		created, err := db.FixLineage(ctx, pool, epic.ID)
		if err != nil {
			return fmt.Errorf("failed to fix lineage for epic %s: %w", epic.ShortID, err)
		}
		if len(created) > 0 {
			fmt.Printf("Epic %s: %s\n", color.YellowString(epic.ShortID), epic.Name)
			for _, doc := range created {
				fmt.Printf("  %s Created %s: %s\n",
					color.GreenString("+"),
					color.CyanString(strings.ToUpper(doc.Category)),
					doc.Title)
			}
			totalCreated += len(created)
		}
	}

	if totalCreated == 0 {
		fmt.Println(color.GreenString("All active epics have complete lineage — nothing to create"))
	} else {
		fmt.Printf("\n%s documents created across all epics\n", color.GreenString("%d", totalCreated))
	}
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
		switch {
		case gap.Status == "out_of_order":
			statusColor = tablewriter.Colors{tablewriter.FgMagentaColor, tablewriter.Bold}
		case gap.Severity == "required" && gap.Status == "missing":
			statusColor = tablewriter.Colors{tablewriter.FgRedColor, tablewriter.Bold}
		case gap.Severity == "required" && gap.Status == "draft":
			statusColor = tablewriter.Colors{tablewriter.FgYellowColor, tablewriter.Bold}
		case gap.Status == "missing":
			statusColor = tablewriter.Colors{tablewriter.FgYellowColor}
		case gap.Status == "draft":
			statusColor = tablewriter.Colors{tablewriter.FgHiBlackColor}
		}

		entity := gap.EntityType + " " + gap.EntityShortID
		if gap.EntityName != "" {
			name := gap.EntityName
			if len(name) > 30 {
				name = name[:27] + "..."
			}
			entity = fmt.Sprintf("%s %s (%s)", gap.EntityType, gap.EntityShortID, name)
		}
		if gap.TaskCount > 0 {
			entity += fmt.Sprintf(" [%d tasks]", gap.TaskCount)
		}

		table.Rich([]string{
			gap.DocumentType,
			gap.Status,
			gap.Severity,
			entity,
		}, []tablewriter.Colors{
			{},
			statusColor,
			statusColor,
			{tablewriter.FgCyanColor},
		})
	}

	table.Render()

	missing := 0
	draft := 0
	outOfOrder := 0
	for _, g := range gaps {
		switch g.Status {
		case "missing":
			missing++
		case "draft":
			draft++
		case "out_of_order":
			outOfOrder++
		}
	}
	parts := []string{}
	if missing > 0 {
		parts = append(parts, color.RedString("%d missing", missing))
	}
	if draft > 0 {
		parts = append(parts, color.YellowString("%d draft", draft))
	}
	if outOfOrder > 0 {
		parts = append(parts, color.MagentaString("%d out of order", outOfOrder))
	}
	fmt.Printf("\n%s\n", strings.Join(parts, ", "))
}
