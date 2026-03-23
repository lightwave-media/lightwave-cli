package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Task management commands",
	Long:  `Manage createOS tasks - list, view, create, and update tasks.`,
}

// Flags for task list
var (
	taskStatus      string
	taskPriority    string
	taskType        string
	taskCategory    string
	taskAgentStatus string
	taskEpicID      string
	taskSprintID    string
	taskLimit       int
)

// Flags for task create
var (
	taskCreateTitle       string
	taskCreateDescription string
	taskCreatePriority    string
	taskCreateType        string
	taskCreateCategory    string
	taskCreateEpic        string
	taskCreateSprint      string
	taskCreateStory       string
)

// Flags for task update
var (
	taskUpdateStatus      string
	taskUpdatePriority    string
	taskUpdateAgent       string
	taskUpdateBranch      string
	taskUpdatePRUrl       string
	taskUpdateTitle       string
	taskUpdateDescription string
	taskUpdateEpic        string
	taskUpdateSprint      string
)

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks (Tier 2: direct PostgreSQL)",
	Long: `List tasks with optional filters.

Examples:
  lw task list --status=approved
  lw task list --status=approved,next_up
  lw task list --priority=p1_urgent
  lw task list --status=in_progress --limit=10`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		// Connect to database (Tier 2)
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		// Build options
		opts := db.TaskListOptions{
			Status:      taskStatus,
			Priority:    taskPriority,
			TaskType:    taskType,
			Category:    taskCategory,
			AgentStatus: taskAgentStatus,
			EpicID:      taskEpicID,
			SprintID:    taskSprintID,
			Limit:       taskLimit,
		}

		// Query tasks
		tasks, err := db.ListTasks(ctx, pool, opts)
		if err != nil {
			return err
		}

		if len(tasks) == 0 {
			fmt.Println(color.YellowString("No tasks found matching filters"))
			return nil
		}

		// Display as table
		printTaskTable(tasks)

		return nil
	},
}

var taskInfoCmd = &cobra.Command{
	Use:   "info <task-id>",
	Short: "Show task details (Tier 2: direct PostgreSQL)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]

		// Connect to database
		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		// Get task
		task, err := db.GetTask(ctx, pool, taskID)
		if err != nil {
			return err
		}

		// Display task details
		printTaskDetails(task)

		return nil
	},
}

var taskContextCmd = &cobra.Command{
	Use:   "context <task-id>",
	Short: "Show full task context (task + epic + sprint + story)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		tc, err := db.GetTaskContext(ctx, pool, taskID)
		if err != nil {
			return err
		}

		printTaskContext(tc)
		return nil
	},
}

var taskCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new task",
	Long: `Create a new task in createOS.

Examples:
  lw task create --title="Fix login bug"
  lw task create --title="Add dark mode" --priority=p2_high --type=feature
  lw task create --title="Update docs" --epic=<epic-id> --sprint=<sprint-id>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if taskCreateTitle == "" {
			return fmt.Errorf("--title is required")
		}

		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		// Interpret escape sequences in description (CLI doesn't expand \n)
		desc := strings.ReplaceAll(taskCreateDescription, `\n`, "\n")

		opts := db.TaskCreateOptions{
			Title:       taskCreateTitle,
			Description: desc,
			Priority:    taskCreatePriority,
			TaskType:    taskCreateType,
			Category:    taskCreateCategory,
			EpicID:      taskCreateEpic,
			SprintID:    taskCreateSprint,
			StoryID:     taskCreateStory,
		}

		task, err := db.CreateTask(ctx, pool, opts)
		if err != nil {
			return err
		}

		fmt.Printf("Created task %s: %s\n", color.YellowString(task.ShortID), task.Title)

		// Auto-create GitHub Issue
		issueNum, issueErr := createGitHubIssueForTask(task)
		if issueErr != nil {
			fmt.Printf("  %s GitHub issue: %v\n", color.YellowString("Warning:"), issueErr)
		} else if issueNum > 0 {
			// Store cross-reference
			notionID := fmt.Sprintf("gh-%d", issueNum)
			if _, err := db.UpdateTaskNotionID(ctx, pool, task.ID, notionID); err != nil {
				fmt.Printf("  %s store issue ref: %v\n", color.YellowString("Warning:"), err)
			}
		}

		return nil
	},
}

var taskUpdateCmd = &cobra.Command{
	Use:   "update <task-id>",
	Short: "Update an existing task",
	Long: `Update task fields by ID (supports short ID prefix).

Examples:
  lw task update abc123 --status=in_progress
  lw task update abc123 --priority=p1_urgent --agent=claude
  lw task update abc123 --branch=feat/login --pr-url=https://github.com/...`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		opts := db.TaskUpdateOptions{}
		if cmd.Flags().Changed("status") {
			opts.Status = &taskUpdateStatus
		}
		if cmd.Flags().Changed("priority") {
			opts.Priority = &taskUpdatePriority
		}
		if cmd.Flags().Changed("agent") {
			opts.Agent = &taskUpdateAgent
		}
		if cmd.Flags().Changed("branch") {
			opts.Branch = &taskUpdateBranch
		}
		if cmd.Flags().Changed("pr-url") {
			opts.PRUrl = &taskUpdatePRUrl
		}
		if cmd.Flags().Changed("title") {
			opts.Title = &taskUpdateTitle
		}
		if cmd.Flags().Changed("description") {
			desc := strings.ReplaceAll(taskUpdateDescription, `\n`, "\n")
			opts.Description = &desc
		}
		if cmd.Flags().Changed("epic") {
			opts.EpicID = &taskUpdateEpic
		}
		if cmd.Flags().Changed("sprint") {
			opts.SprintID = &taskUpdateSprint
		}

		task, err := db.UpdateTask(ctx, pool, taskID, opts)
		if err != nil {
			return err
		}

		fmt.Printf("Updated task %s\n", color.YellowString(task.ShortID))
		return nil
	},
}

var taskNextApprovedCmd = &cobra.Command{
	Use:   "next-approved",
	Short: "Get next approved task from active sprint",
	Long: `Return the next approved task ready for work in the active sprint.
Priority: approved status > next_up > then by priority and created date.
Used by scrum manager orchestrator.

Examples:
  lw task next-approved
  lw task next-approved --sprint=<sprint-id>  # specific sprint`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println(color.HiBlackString("Hint: use `lw github pick` for GitHub-native task selection"))
		fmt.Println()

		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		// Get active sprint (or specified sprint)
		sprintID := taskSprintID
		if sprintID == "" {
			// Find active sprint
			sprints, err := db.ListSprints(ctx, pool, db.SprintListOptions{
				Status: "active",
				Limit:  1,
			})
			if err != nil {
				return err
			}
			if len(sprints) == 0 {
				fmt.Println(color.YellowString("No active sprint found"))
				return nil
			}
			sprintID = sprints[0].ID
		}

		// Get approved and next_up tasks, ordered by priority
		opts := db.TaskListOptions{
			SprintID: sprintID,
			Status:   "approved,next_up",
			Limit:    1,
		}

		tasks, err := db.ListTasks(ctx, pool, opts)
		if err != nil {
			return err
		}

		if len(tasks) == 0 {
			fmt.Println(color.YellowString("No approved tasks in active sprint"))
			return nil
		}

		task := tasks[0]
		printTaskDetails(&task)
		return nil
	},
}

// createGitHubIssueForTask creates a GitHub Issue for a newly created task.
// Returns the issue number (0 if creation failed).
func createGitHubIssueForTask(task *db.Task) (int, error) {
	// Build issue body with task metadata
	var body strings.Builder
	body.WriteString(fmt.Sprintf("**Task ID:** %s\n", task.ShortID))
	body.WriteString(fmt.Sprintf("**Priority:** %s\n", task.Priority))
	body.WriteString(fmt.Sprintf("**Type:** %s\n", task.TaskType))
	if task.Description != nil && *task.Description != "" {
		body.WriteString(fmt.Sprintf("\n%s\n", *task.Description))
	}

	// Map priority to label
	priorityLabel := "p3"
	switch {
	case strings.Contains(task.Priority, "p1"):
		priorityLabel = "p1"
	case strings.Contains(task.Priority, "p2"):
		priorityLabel = "p2"
	case strings.Contains(task.Priority, "p4"):
		priorityLabel = "p4"
	}

	// Map task type to label
	typeLabel := "enhancement"
	if task.TaskType == "fix" || task.TaskType == "hotfix" {
		typeLabel = "bug"
	}

	ghArgs := []string{
		"issue", "create",
		"--repo", defaultGHRepo,
		"--title", task.Title,
		"--body", body.String(),
		"--label", fmt.Sprintf("%s,%s", priorityLabel, typeLabel),
	}

	cmd := exec.Command("gh", ghArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("gh issue create: %w\n%s", err, string(out))
	}

	// Parse issue URL from output to get number
	outStr := strings.TrimSpace(string(out))
	parts := strings.Split(outStr, "/")
	if len(parts) > 0 {
		var num int
		if _, err := fmt.Sscanf(parts[len(parts)-1], "%d", &num); err == nil {
			fmt.Printf("  GitHub issue #%d created: %s\n", num, color.BlueString(outStr))

			// Add to Projects board
			addIssueToProject(num)

			return num, nil
		}
	}

	return 0, nil
}

// addIssueToProject adds an issue to the GitHub Projects board.
func addIssueToProject(issueNumber int) {
	// First get the node ID of the issue
	cmd := exec.Command("gh", "issue", "view",
		fmt.Sprintf("%d", issueNumber),
		"--repo", defaultGHRepo,
		"--json", "id",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return
	}

	var resp struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(out, &resp) != nil || resp.ID == "" {
		return
	}

	// Add to project
	addCmd := exec.Command("gh", "project", "item-add", "2",
		"--owner", "lightwave-media",
		"--url", fmt.Sprintf("https://github.com/%s/issues/%d", defaultGHRepo, issueNumber),
	)
	if addOut, addErr := addCmd.CombinedOutput(); addErr != nil {
		fmt.Printf("  %s add to projects: %v\n%s", color.YellowString("Warning:"), addErr, string(addOut))
	} else {
		fmt.Println("  Added to Projects board")
	}
}

func init() {
	// task list flags
	taskListCmd.Flags().StringVarP(&taskStatus, "status", "s", "", "Filter by status (comma-separated: approved,next_up,in_progress)")
	taskListCmd.Flags().StringVarP(&taskPriority, "priority", "p", "", "Filter by priority (p1_urgent, p2_high, p3_medium, p4_low)")
	taskListCmd.Flags().StringVarP(&taskType, "type", "t", "", "Filter by type (feature, fix, hotfix, chore, docs)")
	taskListCmd.Flags().StringVarP(&taskCategory, "category", "c", "", "Filter by category")
	taskListCmd.Flags().StringVar(&taskAgentStatus, "agent-status", "", "Filter by agent status")
	taskListCmd.Flags().StringVar(&taskEpicID, "epic", "", "Filter by epic ID")
	taskListCmd.Flags().StringVar(&taskSprintID, "sprint", "", "Filter by sprint ID")
	taskListCmd.Flags().IntVarP(&taskLimit, "limit", "n", 50, "Limit number of results")

	// task create flags
	taskCreateCmd.Flags().StringVar(&taskCreateTitle, "title", "", "Task title (required)")
	taskCreateCmd.Flags().StringVar(&taskCreateDescription, "description", "", "Task description")
	taskCreateCmd.Flags().StringVar(&taskCreatePriority, "priority", "p3_medium", "Priority (p1_urgent, p2_high, p3_medium, p4_low)")
	taskCreateCmd.Flags().StringVar(&taskCreateType, "type", "feature", "Type (feature, fix, hotfix, chore, docs)")
	taskCreateCmd.Flags().StringVar(&taskCreateCategory, "category", "", "Task category")
	taskCreateCmd.Flags().StringVar(&taskCreateEpic, "epic", "", "Epic ID")
	taskCreateCmd.Flags().StringVar(&taskCreateSprint, "sprint", "", "Sprint ID")
	taskCreateCmd.Flags().StringVar(&taskCreateStory, "story", "", "User story ID")

	// task update flags
	taskUpdateCmd.Flags().StringVar(&taskUpdateStatus, "status", "", "New status")
	taskUpdateCmd.Flags().StringVar(&taskUpdatePriority, "priority", "", "New priority")
	taskUpdateCmd.Flags().StringVar(&taskUpdateAgent, "agent", "", "Assigned agent")
	taskUpdateCmd.Flags().StringVar(&taskUpdateBranch, "branch", "", "Branch name")
	taskUpdateCmd.Flags().StringVar(&taskUpdatePRUrl, "pr-url", "", "Pull request URL")
	taskUpdateCmd.Flags().StringVar(&taskUpdateTitle, "title", "", "New title")
	taskUpdateCmd.Flags().StringVar(&taskUpdateDescription, "description", "", "New description")
	taskUpdateCmd.Flags().StringVar(&taskUpdateEpic, "epic", "", "Epic ID (short ID, empty to unset)")
	taskUpdateCmd.Flags().StringVar(&taskUpdateSprint, "sprint", "", "Sprint ID (short ID, empty to unset)")

	// Add subcommands
	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskInfoCmd)
	taskCmd.AddCommand(taskContextCmd)
	taskCmd.AddCommand(taskCreateCmd)
	taskCmd.AddCommand(taskUpdateCmd)
	taskCmd.AddCommand(taskNextApprovedCmd)

	// next-approved flags
	taskNextApprovedCmd.Flags().StringVar(&taskSprintID, "sprint", "", "Sprint ID (defaults to active sprint)")
}

func printTaskTable(tasks []db.Task) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ID", "Title", "Status", "Priority", "Type"})
	table.SetBorder(false)
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
	)

	for _, t := range tasks {
		// Truncate title if too long
		title := t.Title
		if len(title) > 50 {
			title = title[:47] + "..."
		}

		// Color status
		statusColor := getStatusColor(t.Status)
		priorityColor := getPriorityColor(t.Priority)

		table.Rich([]string{
			t.ShortID,
			title,
			t.StatusDisplay(),
			t.PriorityDisplay(),
			t.TaskType,
		}, []tablewriter.Colors{
			{tablewriter.FgYellowColor},
			{},
			statusColor,
			priorityColor,
			{},
		})
	}

	table.Render()
	fmt.Printf("\n%s tasks\n", color.CyanString("%d", len(tasks)))
}

func printTaskDetails(t *db.Task) {
	fmt.Println()
	fmt.Printf("%s %s\n", color.CyanString("Task:"), color.YellowString(t.ShortID))
	fmt.Printf("%s %s\n", color.CyanString("Title:"), t.Title)
	fmt.Println()

	fmt.Printf("%s %s\n", color.CyanString("Status:"), colorStatus(t.Status, t.StatusDisplay()))
	fmt.Printf("%s %s\n", color.CyanString("Priority:"), colorPriority(t.Priority, t.PriorityDisplay()))
	fmt.Printf("%s %s\n", color.CyanString("Type:"), t.TaskType)
	fmt.Printf("%s %s\n", color.CyanString("Category:"), t.TaskCategory)
	fmt.Println()

	if t.Description != nil && *t.Description != "" {
		fmt.Println(color.CyanString("Description:"))
		fmt.Println(*t.Description)
		fmt.Println()
	}

	if t.BranchName != nil && *t.BranchName != "" {
		fmt.Printf("%s %s\n", color.CyanString("Branch:"), *t.BranchName)
	}
	if t.PRUrl != nil && *t.PRUrl != "" {
		fmt.Printf("%s %s\n", color.CyanString("PR:"), *t.PRUrl)
	}

	if t.AssignedAgent != nil && *t.AssignedAgent != "" {
		fmt.Printf("%s %s\n", color.CyanString("Agent:"), *t.AssignedAgent)
	}

	fmt.Println()
	fmt.Printf("%s %s\n", color.CyanString("Full ID:"), t.ID)
	fmt.Printf("%s %s\n", color.CyanString("Updated:"), t.UpdatedAt.Format("2006-01-02 15:04:05"))
}

func getStatusColor(status string) tablewriter.Colors {
	switch status {
	case "in_progress":
		return tablewriter.Colors{tablewriter.FgGreenColor}
	case "approved", "next_up":
		return tablewriter.Colors{tablewriter.FgYellowColor}
	case "in_review":
		return tablewriter.Colors{tablewriter.FgBlueColor}
	case "archived":
		return tablewriter.Colors{tablewriter.FgHiBlackColor}
	case "on_hold":
		return tablewriter.Colors{tablewriter.FgRedColor}
	default:
		return tablewriter.Colors{}
	}
}

func getPriorityColor(priority string) tablewriter.Colors {
	switch priority {
	case "p1_urgent":
		return tablewriter.Colors{tablewriter.FgRedColor, tablewriter.Bold}
	case "p2_high":
		return tablewriter.Colors{tablewriter.FgYellowColor}
	case "p3_medium":
		return tablewriter.Colors{}
	case "p4_low":
		return tablewriter.Colors{tablewriter.FgHiBlackColor}
	default:
		return tablewriter.Colors{}
	}
}

func colorStatus(status, display string) string {
	switch status {
	case "in_progress":
		return color.GreenString(display)
	case "approved", "next_up":
		return color.YellowString(display)
	case "in_review":
		return color.BlueString(display)
	case "archived":
		return color.HiBlackString(display)
	case "on_hold":
		return color.RedString(display)
	default:
		return display
	}
}

func colorPriority(priority, display string) string {
	switch priority {
	case "p1_urgent":
		return color.RedString(display)
	case "p2_high":
		return color.YellowString(display)
	case "p4_low":
		return color.HiBlackString(display)
	default:
		return display
	}
}

func printTaskContext(tc *db.TaskContext) {
	fmt.Println()
	fmt.Printf("%s %s\n", color.CyanString("Task:"), color.YellowString(tc.ShortID))
	fmt.Printf("%s %s\n", color.CyanString("Title:"), tc.Title)
	fmt.Printf("%s %s\n", color.CyanString("Status:"), colorStatus(tc.Status, tc.StatusDisplay()))
	fmt.Printf("%s %s\n", color.CyanString("Priority:"), colorPriority(tc.Priority, tc.PriorityDisplay()))
	fmt.Printf("%s %s\n", color.CyanString("Type:"), tc.TaskType)
	if tc.TaskCategory != "" {
		fmt.Printf("%s %s\n", color.CyanString("Category:"), tc.TaskCategory)
	}
	fmt.Println()

	// Epic
	if tc.EpicName != nil {
		fmt.Println(color.CyanString("Epic:"))
		fmt.Printf("  Name:   %s\n", *tc.EpicName)
		if tc.EpicStatus != nil {
			fmt.Printf("  Status: %s\n", *tc.EpicStatus)
		}
		if tc.EpicID != nil {
			fmt.Printf("  ID:     %s\n", *tc.EpicID)
		}
		fmt.Println()
	}

	// Sprint
	if tc.SprintName != nil {
		fmt.Println(color.CyanString("Sprint:"))
		fmt.Printf("  Name:   %s\n", *tc.SprintName)
		if tc.SprintStatus != nil {
			fmt.Printf("  Status: %s\n", *tc.SprintStatus)
		}
		if tc.SprintStart != nil && tc.SprintEnd != nil {
			fmt.Printf("  Dates:  %s - %s\n", tc.SprintStart.Format("2006-01-02"), tc.SprintEnd.Format("2006-01-02"))
		}
		if tc.SprintID != nil {
			fmt.Printf("  ID:     %s\n", *tc.SprintID)
		}
		fmt.Println()
	}

	// Story
	if tc.StoryName != nil {
		fmt.Printf("%s %s\n", color.CyanString("Story:"), *tc.StoryName)
		fmt.Println()
	}

	// Description
	if tc.Description != nil && *tc.Description != "" {
		fmt.Println(color.CyanString("Description:"))
		fmt.Println(*tc.Description)
		fmt.Println()
	}

	// Dev context
	if (tc.BranchName != nil && *tc.BranchName != "") || (tc.PRUrl != nil && *tc.PRUrl != "") || (tc.AssignedAgent != nil && *tc.AssignedAgent != "") {
		fmt.Println(color.CyanString("Development:"))
		if tc.BranchName != nil && *tc.BranchName != "" {
			fmt.Printf("  Branch: %s\n", *tc.BranchName)
		}
		if tc.PRUrl != nil && *tc.PRUrl != "" {
			fmt.Printf("  PR:     %s\n", *tc.PRUrl)
		}
		if tc.AssignedAgent != nil && *tc.AssignedAgent != "" {
			fmt.Printf("  Agent:  %s\n", *tc.AssignedAgent)
		}
		fmt.Println()
	}

	fmt.Printf("%s %s\n", color.CyanString("Full ID:"), tc.ID)
	fmt.Printf("%s %s\n", color.CyanString("Updated:"), tc.UpdatedAt.Format("2006-01-02 15:04:05"))
}

// Suppress unused warning
var _ = strings.TrimSpace
