package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/lightwave-media/lightwave-cli/internal/github"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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

		// Guard: refuse if another sprint is already active
		activeSprints, err := db.ListSprints(ctx, pool, db.SprintListOptions{Status: "active", Limit: 1})
		if err != nil {
			return fmt.Errorf("checking active sprints: %w", err)
		}
		if len(activeSprints) > 0 && activeSprints[0].ID != sprint.ID {
			return fmt.Errorf("sprint %s is already active — complete it first", activeSprints[0].ShortID)
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
				epicShort := *sprint.EpicID
				if len(epicShort) > 8 {
					epicShort = epicShort[:8]
				}
				fmt.Printf("\n%s Sprint blocked by missing required documents:\n", color.RedString("BLOCKED"))
				for _, b := range blockers {
					fmt.Printf("  %s %s for %s %s\n",
						color.RedString("✗"),
						color.CyanString(strings.ToUpper(b.DocumentType)),
						b.EntityType, b.EntityShortID)
				}
				fmt.Printf("\nRun %s to create them, or %s to bypass\n",
					color.CyanString("lw lineage fix %s", epicShort),
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
	Short: "Show active sprint status with task progress",
	Long:  `Show the currently active sprint with task completion stats.`,
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
			days := int(time.Since(*s.StartDate).Hours() / 24)
			fmt.Printf("Started: %s (%d days ago)\n", s.StartDate.Format("2006-01-02"), days)
		}

		// Task progress
		tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{SprintID: s.ShortID, Limit: 100})
		if err != nil {
			return err
		}

		done, inProgress, total := 0, 0, len(tasks)
		for _, t := range tasks {
			switch t.Status {
			case "done":
				done++
			case "in_progress":
				inProgress++
			}
		}

		if total > 0 {
			pct := done * 100 / total
			fmt.Printf("Tasks:   %s/%d done (%d%%)", color.GreenString("%d", done), total, pct)
			if inProgress > 0 {
				fmt.Printf(", %s in progress", color.YellowString("%d", inProgress))
			}
			fmt.Println()

			// Show remaining tasks
			remaining := total - done
			if remaining > 0 {
				fmt.Println()
				for _, t := range tasks {
					switch t.Status {
					case "done":
						fmt.Printf("  %s %s\n", color.GreenString("✓"), color.HiBlackString(t.Title))
					case "in_progress":
						fmt.Printf("  %s %s (%s)\n", color.YellowString("◐"), t.Title, t.ShortID)
					default:
						fmt.Printf("  %s %s (%s)\n", color.RedString("○"), t.Title, t.ShortID)
					}
				}
			}
		}

		// Lineage status
		if s.EpicID != nil {
			gaps, err := db.CheckLineage(ctx, pool, *s.EpicID)
			if err == nil && len(gaps) > 0 {
				missing := 0
				for _, g := range gaps {
					if g.Status == "missing" {
						missing++
					}
				}
				if missing > 0 {
					fmt.Printf("\nLineage: %s\n", color.RedString("%d missing docs", missing))
				} else {
					fmt.Printf("\nLineage: %s\n", color.YellowString("%d draft docs", len(gaps)))
				}
			} else if err == nil {
				fmt.Printf("\nLineage: %s\n", color.GreenString("complete"))
			}
		}

		return nil
	},
}

var sprintCompleteCmd = &cobra.Command{
	Use:   "complete [sprint-id]",
	Short: "Complete a sprint: mark done, move spec, report results",
	Long: `Complete a sprint:
1. Mark sprint as completed with today's end date
2. Move spec from active/ → done/
3. Report task completion stats

If no sprint ID is given, completes the active sprint.

Examples:
  lw sprint complete
  lw sprint complete cde4d931`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		var sprintID string
		if len(args) == 1 {
			sprintID = args[0]
		} else {
			sprints, err := db.ListSprints(ctx, pool, db.SprintListOptions{Status: "active", Limit: 1})
			if err != nil {
				return err
			}
			if len(sprints) == 0 {
				return fmt.Errorf("no active sprint found")
			}
			sprintID = sprints[0].ShortID
		}

		sprint, err := db.GetSprint(ctx, pool, sprintID)
		if err != nil {
			return err
		}

		// Report task stats
		tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{SprintID: sprint.ShortID, Limit: 100})
		if err != nil {
			return fmt.Errorf("failed to list tasks: %w", err)
		}

		done, total := 0, 0
		for _, t := range tasks {
			total++
			if t.Status == "done" {
				done++
			}
		}

		// Zero-task guard
		force, _ := cmd.Flags().GetBool("force")
		if total == 0 && !force {
			return fmt.Errorf("sprint %s has no tasks", sprint.ShortID)
		}

		fmt.Printf("Sprint %s: %s\n", color.YellowString(sprint.ShortID), sprint.Name)
		fmt.Printf("Tasks: %s/%d completed\n", color.GreenString("%d", done), total)

		if done < total {
			remaining := total - done
			fmt.Printf("%s %d tasks still open:\n", color.YellowString("!"), remaining)
			for _, t := range tasks {
				if t.Status != "done" {
					fmt.Printf("  %s %s (%s) — %s\n",
						color.RedString("○"),
						t.Title, t.ShortID,
						color.YellowString(t.Status))
				}
			}
		}

		// Gate: refuse to complete with open tasks unless --force
		if done < total && !force {
			fmt.Printf("\nUse %s to complete anyway\n", color.YellowString("--force"))
			return fmt.Errorf("sprint has %d open tasks", total-done)
		}

		// Show GitHub cleanup summary
		var issuestoClose []int
		for _, t := range tasks {
			if t.Status == "done" {
				if issueNum := taskIssueNumber(ctx, &t); issueNum > 0 {
					issuestoClose = append(issuestoClose, issueNum)
				}
			}
		}

		if dryRun {
			fmt.Printf("\n%s\n", color.YellowString("[DRY RUN] Would perform:"))
			fmt.Printf("  Mark sprint %s as completed\n", sprint.ShortID)
			if len(issuestoClose) > 0 {
				fmt.Printf("  Close %d GitHub Issues: %v\n", len(issuestoClose), issuestoClose)
				fmt.Printf("  Move %d Projects cards to Done\n", len(issuestoClose))
			}
			return nil
		}

		// Mark sprint completed
		today := time.Now().Format("2006-01-02")
		completed := "completed"
		_, err = db.UpdateSprint(ctx, pool, sprint.ShortID, db.SprintUpdateOptions{
			Status:  &completed,
			EndDate: &today,
		})
		if err != nil {
			return fmt.Errorf("failed to complete sprint: %w", err)
		}
		fmt.Printf("Status: %s  End: %s\n", color.HiBlackString("completed"), color.CyanString(today))

		// Close linked GitHub Issues and sync Projects board
		for _, issueNum := range issuestoClose {
			closeLinkedIssue(issueNum)
			syncProjectStatus(issueNum, "done")
		}

		// Move spec from active/ → done/
		specPath, _, err := FindSprintSpec(sprint.ShortID)
		if err == nil {
			newPath, err := MoveSpec(specPath, "done")
			if err != nil {
				fmt.Printf("%s Failed to move spec: %v\n", color.YellowString("Warning:"), err)
			} else {
				fmt.Printf("Spec moved → %s\n", color.CyanString(newPath))
			}
		}

		return nil
	},
}

var sprintPlanCmd = &cobra.Command{
	Use:   "plan [sprint-id]",
	Short: "Generate a sprint spec YAML from database records",
	Long: `Read sprint, stories, and tasks from the database and write a spec YAML
file to .claude/queue/draft/ for use with lw sprint start.

Examples:
  lw sprint plan a1b2c3d4`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		sprint, err := db.GetSprint(ctx, pool, args[0])
		if err != nil {
			return err
		}

		// Get epic
		var epicName, epicID string
		if sprint.EpicID != nil {
			epic, err := db.GetEpic(ctx, pool, *sprint.EpicID)
			if err == nil {
				epicName = epic.Name
				epicID = epic.ID
			}
		}

		// Get stories for this sprint
		stories, err := db.ListStories(ctx, pool, db.StoryListOptions{SprintID: sprint.ID})
		if err != nil {
			return fmt.Errorf("failed to list stories: %w", err)
		}

		// Get tasks for this sprint
		tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{SprintID: sprint.ID, Limit: 100})
		if err != nil {
			return fmt.Errorf("failed to list tasks: %w", err)
		}

		// Build spec structure
		spec := SprintSpec{}
		spec.Sprint.ID = sprint.ID
		spec.Sprint.Name = sprint.Name
		spec.Sprint.Status = sprint.Status
		spec.Epic.ID = epicID
		spec.Epic.Name = epicName

		if sprint.Objectives != nil {
			spec.Objective = *sprint.Objectives
		}

		for _, s := range stories {
			spec.Stories = append(spec.Stories, struct {
				ID   string `yaml:"id"`
				Name string `yaml:"name"`
			}{ID: s.ID, Name: s.Name})
		}

		for _, t := range tasks {
			spec.Tasks = append(spec.Tasks, struct {
				ID       string   `yaml:"id"`
				Name     string   `yaml:"name"`
				Type     string   `yaml:"type"`
				Priority string   `yaml:"priority"`
				Status   string   `yaml:"status"`
				Story    string   `yaml:"story"`
				Files    []string `yaml:"files"`
				AC       string   `yaml:"ac"`
			}{
				ID:       t.ID,
				Name:     t.Title,
				Type:     t.TaskType,
				Priority: t.Priority,
				Status:   t.Status,
			})
		}

		// Marshal to YAML
		data, err := yaml.Marshal(&spec)
		if err != nil {
			return fmt.Errorf("failed to marshal spec: %w", err)
		}

		// Write to .claude/queue/draft/
		cfg := config.Get()
		draftDir := filepath.Join(cfg.Paths.LightwaveRoot, ".claude", "queue", "draft")
		if err := os.MkdirAll(draftDir, 0o755); err != nil {
			return fmt.Errorf("failed to create draft directory: %w", err)
		}

		// Sanitize sprint name for filename
		safeName := strings.ReplaceAll(strings.ToLower(sprint.Name), " ", "-")
		safeName = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				return r
			}
			return -1
		}, safeName)
		filename := fmt.Sprintf("sprint-%s-%s.yaml", sprint.ShortID, safeName)
		outPath := filepath.Join(draftDir, filename)

		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			return fmt.Errorf("failed to write spec: %w", err)
		}

		fmt.Printf("%s Sprint spec written to %s\n",
			color.GreenString("✓"),
			color.CyanString(outPath))
		fmt.Printf("  Sprint: %s (%s)\n", sprint.Name, sprint.ShortID)
		fmt.Printf("  Stories: %d  Tasks: %d\n", len(stories), len(tasks))
		fmt.Printf("\nNext: edit the spec, then run %s\n",
			color.CyanString("lw sprint start %s", sprint.ShortID))
		return nil
	},
}

var sprintAutoPlanCmd = &cobra.Command{
	Use:   "auto-plan [epic-id]",
	Short: "Create a sprint from approved tasks in an epic",
	Long: `Pulls all approved tasks from an epic that aren't already in a sprint,
creates a new sprint, and assigns tasks to it.

Examples:
  lw sprint auto-plan abc123
  lw sprint auto-plan abc123 --dry-run
  lw sprint auto-plan abc123 --name="Sprint 5"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		dryRun, _ := cmd.Flags().GetBool("dry-run")
		name, _ := cmd.Flags().GetString("name")

		_, _, err = autoPlanSprint(ctx, pool, args[0], name, dryRun)
		return err
	},
}

var sprintCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check if active sprint is complete (exit 0 if done, 1 if not)",
	Long: `Checks if all tasks in the active sprint are done.
Returns exit code 0 if complete, 1 if not. Designed for scripting.

Examples:
  lw sprint check`,
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
			return fmt.Errorf("no active sprint")
		}

		s := sprints[0]
		tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{SprintID: s.ShortID, Limit: 100})
		if err != nil {
			return err
		}

		if len(tasks) == 0 {
			return fmt.Errorf("sprint %s has no tasks", s.ShortID)
		}

		done := 0
		for _, t := range tasks {
			if t.Status == "done" {
				done++
			}
		}

		if done == len(tasks) {
			fmt.Printf("Sprint complete — all %d tasks done\n", done)
			return nil
		}

		fmt.Printf("Sprint in progress: %d/%d tasks done\n", done, len(tasks))
		return fmt.Errorf("sprint incomplete: %d/%d done", done, len(tasks))
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

	// sprint complete flags
	sprintCompleteCmd.Flags().Bool("force", false, "Complete sprint even with open tasks")
	sprintCompleteCmd.Flags().Bool("dry-run", false, "Show what would happen without making changes")

	// sprint auto-plan flags
	sprintAutoPlanCmd.Flags().Bool("dry-run", false, "Show what would be planned without creating")
	sprintAutoPlanCmd.Flags().String("name", "", "Override auto-generated sprint name")

	// Add subcommands
	sprintCmd.AddCommand(sprintListCmd)
	sprintCmd.AddCommand(sprintCreateCmd)
	sprintCmd.AddCommand(sprintUpdateCmd)
	sprintCmd.AddCommand(sprintStartCmd)
	sprintCmd.AddCommand(sprintStatusCmd)
	sprintCmd.AddCommand(sprintCompleteCmd)
	sprintCmd.AddCommand(sprintPlanCmd)
	sprintCmd.AddCommand(sprintAutoPlanCmd)
	sprintCmd.AddCommand(sprintCheckCmd)
}

func syncSprintToGitHub(ctx context.Context, pool *pgxpool.Pool, sprint *db.Sprint) error {
	tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{SprintID: sprint.ShortID, Limit: 100})
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

	for shortID, ref := range results {
		fmt.Printf("  %s %s → %s\n", color.GreenString("✓"), shortID, color.BlueString(ref.URL))

		// Store issue linkage: set notion_id to gh-N so taskIssueNumber() can resolve it
		if ref.Number > 0 {
			ghRef := fmt.Sprintf("gh-%d", ref.Number)
			if _, linkErr := db.UpdateTaskNotionID(ctx, pool, shortID, ghRef); linkErr != nil {
				fmt.Printf("    %s link task %s → %s: %v\n", color.YellowString("Warning:"), shortID, ghRef, linkErr)
			}
		}
	}

	if err != nil {
		return err
	}

	fmt.Printf("%s Synced %d GitHub issues\n", color.GreenString("Done!"), len(results))
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

// spawnClaudeSession launches a NEW claude session with the generated prompt.
// If already inside Claude Code (CLAUDECODE env var set), writes the prompt to a
// file and prints instructions instead of nesting sessions.
func spawnClaudeSession(prompt string) error {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Println(color.YellowString("claude not found in PATH — printing prompt instead:"))
		fmt.Println(prompt)
		return nil
	}

	// Detect nesting: CLAUDECODE is set when running inside a Claude Code session
	if os.Getenv("CLAUDECODE") != "" {
		// Write prompt to temp file so a new session can pick it up
		promptFile := filepath.Join(os.TempDir(), "lw-sprint-prompt.md")
		if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
			return fmt.Errorf("writing prompt file: %w", err)
		}
		fmt.Printf("%s Sprint activated. Prompt written to %s\n", color.GreenString("✓"), color.CyanString(promptFile))
		fmt.Printf("Start a new session: %s\n", color.CyanString("cat %s | claude -p", promptFile))
		return nil
	}

	// Not inside Claude Code — spawn directly
	cmd := exec.Command(claudePath, "-p")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// autoPlanSprint creates a sprint from approved, unsprinted tasks in an epic.
// Returns the created sprint (nil if no tasks or dry run), task count, and error.
func autoPlanSprint(ctx context.Context, pool *pgxpool.Pool, epicID, nameOverride string, dryRun bool) (*db.Sprint, int, error) {
	epic, err := db.GetEpic(ctx, pool, epicID)
	if err != nil {
		return nil, 0, fmt.Errorf("resolving epic: %w", err)
	}

	tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{
		Status: "approved",
		EpicID: epic.ID,
		Limit:  100,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("listing tasks: %w", err)
	}

	// Filter for tasks not already in a sprint
	var ready []db.Task
	for _, t := range tasks {
		if t.SprintID == nil {
			ready = append(ready, t)
		}
	}

	if len(ready) == 0 {
		fmt.Println(color.YellowString("No approved tasks ready for sprint"))
		return nil, 0, nil
	}

	// Auto-name: "Sprint N" from last sprint count in epic
	sprintName := nameOverride
	if sprintName == "" {
		existing, _ := db.ListSprints(ctx, pool, db.SprintListOptions{EpicID: epic.ID, Limit: 100})
		sprintName = fmt.Sprintf("Sprint %d", len(existing)+1)
	}

	if dryRun {
		fmt.Printf("[DRY RUN] Would create %s with %d tasks:\n", color.CyanString(sprintName), len(ready))
		for _, t := range ready {
			fmt.Printf("  %s %s (%s)\n", color.YellowString("→"), t.Title, t.ShortID)
		}
		return nil, len(ready), nil
	}

	sprint, err := db.CreateSprint(ctx, pool, db.SprintCreateOptions{
		Name:   sprintName,
		Status: "planned",
		EpicID: epic.ID,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("creating sprint: %w", err)
	}

	for _, t := range ready {
		sid := sprint.ID
		_, err := db.UpdateTask(ctx, pool, t.ShortID, db.TaskUpdateOptions{
			SprintID: &sid,
		})
		if err != nil {
			fmt.Printf("  %s Failed to assign %s: %v\n", color.YellowString("!"), t.ShortID, err)
		}
	}

	fmt.Printf("Sprint %s: %s\n", color.YellowString(sprint.ShortID), sprint.Name)
	for _, t := range ready {
		fmt.Printf("  %s %s (%s)\n", color.GreenString("→"), t.Title, t.ShortID)
	}

	return sprint, len(ready), nil
}
