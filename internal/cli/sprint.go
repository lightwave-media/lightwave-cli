package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/lightwave-media/lightwave-cli/internal/github"
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

// Flags for sprint start
var (
	sprintStartNoGithub bool
	sprintStartProject  int
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

var sprintStartCmd = &cobra.Command{
	Use:   "start [sprint-id]",
	Short: "Start a sprint: check lineage, generate spec, spawn Claude Code",
	Long: `Start a sprint with full automation:
1. Load sprint spec from .claude/queue/{draft,pending}/
2. Check lineage gate — refuse if required docs (from SST sprint_blockers) are missing
3. Mark sprint as active with today's start date
4. Generate spec prompt from YAML
5. Spawn claude -p with the spec (or print prompt with --dry-run)
6. Move spec file from draft/ → active/
7. Sync tasks to GitHub (unless --no-github)

If no sprint ID is given, starts the first planned sprint.

Examples:
  lw sprint start cde4d931
  lw sprint start --dry-run
  lw sprint start --no-github`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		skipGate, _ := cmd.Flags().GetBool("skip-gate")

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		// Resolve sprint
		var sprintID string
		if len(args) == 1 {
			sprintID = args[0]
		} else {
			sprints, err := db.ListSprints(ctx, pool, db.SprintListOptions{Status: "planned", Limit: 1})
			if err != nil {
				return err
			}
			if len(sprints) == 0 {
				return fmt.Errorf("no planned sprints found")
			}
			sprintID = sprints[0].ShortID
		}

		sprint, err := db.GetSprint(ctx, pool, sprintID)
		if err != nil {
			return err
		}

		fmt.Printf("Sprint %s: %s\n", color.YellowString(sprint.ShortID), sprint.Name)

		// 1. Find sprint spec YAML
		specPath, spec, err := FindSprintSpec(sprint.ShortID)
		if err != nil {
			fmt.Printf("%s No spec file found — generating prompt from database\n", color.YellowString("!"))
			spec = nil
		} else {
			fmt.Printf("Spec: %s\n", color.CyanString(specPath))
		}

		// 2. Lineage gate
		if !skipGate && sprint.EpicID != nil {
			lc := db.LoadLineageConfig()
			gaps, err := db.CheckLineage(ctx, pool, *sprint.EpicID)
			if err != nil {
				return fmt.Errorf("lineage check failed: %w", err)
			}

			// Check for blockers (required docs that are missing)
			blockerSet := make(map[string]bool)
			for _, b := range lc.SprintBlockers {
				blockerSet[b] = true
			}

			var blockers []db.LineageGap
			for _, gap := range gaps {
				if gap.Status == "missing" && blockerSet[gap.DocumentType] {
					blockers = append(blockers, gap)
				}
			}

			if len(blockers) > 0 {
				fmt.Printf("\n%s Sprint blocked by missing required documents:\n", color.RedString("BLOCKED"))
				for _, b := range blockers {
					fmt.Printf("  %s %s for %s %s\n",
						color.RedString("✗"),
						color.CyanString(strings.ToUpper(b.DocumentType)),
						b.EntityType, b.EntityShortID)
				}
				fmt.Printf("\nRun %s to create them, or %s to bypass\n",
					color.CyanString("lw lineage fix %s", sprint.ShortID),
					color.YellowString("--skip-gate"))
				return fmt.Errorf("lineage gate failed: %d blocking documents missing", len(blockers))
			}

			// Show warnings for non-blocking gaps
			if len(gaps) > 0 {
				fmt.Printf("%s %d lineage gaps (non-blocking)\n", color.YellowString("!"), len(gaps))
			}
		}

		// 3. Generate prompt
		var prompt string
		if spec != nil {
			prompt = GeneratePrompt(spec)
		} else {
			prompt = generatePromptFromDB(ctx, pool, sprint)
		}

		if dryRun {
			fmt.Printf("\n%s\n", color.CyanString("--- Generated Prompt ---"))
			fmt.Println(prompt)
			fmt.Printf("%s\n", color.CyanString("--- End Prompt ---"))
			return nil
		}

		// 4. Mark sprint as active
		today := time.Now().Format("2006-01-02")
		active := "active"
		_, err = db.UpdateSprint(ctx, pool, sprint.ShortID, db.SprintUpdateOptions{
			Status:    &active,
			StartDate: &today,
		})
		if err != nil {
			return fmt.Errorf("failed to activate sprint: %w", err)
		}
		fmt.Printf("Status: %s  Start: %s\n", color.GreenString("active"), color.CyanString(today))

		// 5. Move spec file to active/
		if specPath != "" {
			newPath, err := MoveSpec(specPath, "active")
			if err != nil {
				fmt.Printf("%s Failed to move spec: %v\n", color.YellowString("Warning:"), err)
			} else {
				fmt.Printf("Spec moved → %s\n", color.CyanString(newPath))
			}
		}

		// 6. GitHub sync
		if !sprintStartNoGithub {
			if err := syncSprintToGitHub(ctx, pool, sprint); err != nil {
				fmt.Printf("%s GitHub sync: %v\n", color.YellowString("Warning:"), err)
			}
		}

		// 7. Spawn Claude Code
		fmt.Printf("\n%s Spawning Claude Code session...\n", color.GreenString("→"))
		return spawnClaudeSession(prompt)
	},
}

var sprintStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show active sprint status",
	Long:  `Show the currently active sprint with task counts.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		sprints, err := db.ListSprints(ctx, pool, db.SprintListOptions{Status: "active", Limit: 1})
		if err != nil {
			return err
		}
		if len(sprints) == 0 {
			fmt.Println(color.YellowString("No active sprint"))
			return nil
		}

		s := sprints[0]
		fmt.Printf("Sprint: %s (%s)\n", color.CyanString(s.Name), color.YellowString(s.ShortID))
		fmt.Printf("Status: %s\n", color.GreenString(s.Status))
		if s.StartDate != nil {
			fmt.Printf("Started: %s\n", s.StartDate.Format("2006-01-02"))
		}
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

	// sprint start flags
	sprintStartCmd.Flags().BoolVar(&sprintStartNoGithub, "no-github", false, "Skip GitHub issue creation")
	sprintStartCmd.Flags().IntVar(&sprintStartProject, "project", 0, "GitHub org project number to add issues to")
	sprintStartCmd.Flags().Bool("dry-run", false, "Print generated prompt without spawning Claude Code")
	sprintStartCmd.Flags().Bool("skip-gate", false, "Bypass lineage gate check")

	// Add subcommands
	sprintCmd.AddCommand(sprintListCmd)
	sprintCmd.AddCommand(sprintCreateCmd)
	sprintCmd.AddCommand(sprintUpdateCmd)
	sprintCmd.AddCommand(sprintStartCmd)
	sprintCmd.AddCommand(sprintStatusCmd)
}

func syncSprintToGitHub(ctx context.Context, pool interface{}, sprint *db.Sprint) error {
	// Re-connect to get the typed pool for task queries
	dbPool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection: %w", err)
	}

	tasks, err := db.ListTasks(ctx, dbPool, db.TaskListOptions{SprintID: sprint.ShortID, Limit: 100})
	if err != nil {
		return fmt.Errorf("listing sprint tasks: %w", err)
	}

	if len(tasks) == 0 {
		fmt.Println(color.YellowString("No tasks in sprint — skipping GitHub sync"))
		return nil
	}

	fmt.Printf("\nCreating GitHub issues for %d tasks...\n", len(tasks))

	// Convert to github.TaskInfo
	taskInfos := make([]github.TaskInfo, len(tasks))
	for i, t := range tasks {
		desc := ""
		if t.Description != nil {
			desc = *t.Description
		}
		taskInfos[i] = github.TaskInfo{
			ShortID:      t.ShortID,
			Title:        t.Title,
			Description:  desc,
			Priority:     t.Priority,
			TaskType:     t.TaskType,
			TaskCategory: t.TaskCategory,
		}
	}

	results, err := github.SyncSprintTasks(
		github.DefaultRepo,
		github.DefaultOrg,
		sprintStartProject,
		taskInfos,
	)

	for shortID, url := range results {
		fmt.Printf("  %s %s → %s\n", color.GreenString("✓"), shortID, color.BlueString(url))
	}

	if err != nil {
		return err
	}

	fmt.Printf("%s Created %d GitHub issues\n", color.GreenString("Done!"), len(results))
	return nil
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

// generatePromptFromDB builds a prompt from database data when no spec YAML exists
func generatePromptFromDB(ctx context.Context, pool *pgxpool.Pool, sprint *db.Sprint) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# Sprint: %s\n\n", sprint.Name))
	b.WriteString(fmt.Sprintf("**Sprint ID:** %s\n\n", sprint.ShortID))

	if sprint.Objectives != nil && *sprint.Objectives != "" {
		b.WriteString("## Objective\n\n")
		b.WriteString(*sprint.Objectives + "\n\n")
	}

	// Load tasks from DB
	tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{
		SprintID: sprint.ShortID,
		Limit:    100,
	})
	if err == nil && len(tasks) > 0 {
		b.WriteString("## Tasks\n\n")
		b.WriteString("| # | Task | Type | Priority | Status |\n")
		b.WriteString("|---|------|------|----------|--------|\n")
		for i, t := range tasks {
			if t.Status == "done" {
				continue
			}
			b.WriteString(fmt.Sprintf("| %d | %s (`%s`) | %s | %s | %s |\n",
				i+1, t.Title, t.ShortID, t.TaskType, t.Priority, t.Status))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Read relevant files before making changes\n")
	b.WriteString("2. Work through tasks in priority order (P1 first)\n")
	b.WriteString("3. After each task, run relevant tests to verify\n")
	b.WriteString("4. Mark tasks as done via `lw task update <id> --status=done` when complete\n")

	return b.String()
}

// spawnClaudeSession launches claude -p with the generated prompt
func spawnClaudeSession(prompt string) error {
	// Write prompt to temp file for claude -p
	tmpFile, err := os.CreateTemp("", "lw-sprint-*.md")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(prompt); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write prompt: %w", err)
	}
	tmpFile.Close()

	// Check if claude is available
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Println(color.YellowString("claude not found in PATH — printing prompt instead:"))
		fmt.Println(prompt)
		return nil
	}

	// Spawn claude with the prompt file
	cmd := exec.Command(claudePath, "-p", prompt)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
