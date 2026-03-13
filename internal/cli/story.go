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

var storyCmd = &cobra.Command{
	Use:   "story",
	Short: "User story management commands",
	Long:  `Manage createOS user stories - list and create stories.`,
}

// Flags for story list
var (
	storyListStatus string
	storyListEpic   string
	storyListSprint string
	storyListLimit  int
)

// Flags for story create
var (
	storyCreateName        string
	storyCreateDescription string
	storyCreatePriority    string
	storyCreateEpic        string
	storyCreateSprint      string
	storyCreateUserType    string
)

// Flags for story update
var (
	storyUpdateStatus   string
	storyUpdateName     string
	storyUpdatePriority string
)

var storyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List user stories",
	Long: `List user stories with optional filters.

Examples:
  lw story list
  lw story list --status=active
  lw story list --epic=abc123 --sprint=def456`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		opts := db.StoryListOptions{
			Status:   storyListStatus,
			EpicID:   storyListEpic,
			SprintID: storyListSprint,
			Limit:    storyListLimit,
		}

		stories, err := db.ListStories(ctx, pool, opts)
		if err != nil {
			return err
		}

		if len(stories) == 0 {
			fmt.Println(color.YellowString("No stories found matching filters"))
			return nil
		}

		printStoryTable(stories)
		return nil
	},
}

var storyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new user story",
	Long: `Create a new user story in createOS.

Examples:
  lw story create --name="As a user, I want to login"
  lw story create --name="Payment flow" --priority=p2_high --epic=abc123
  lw story create --name="Dashboard view" --user-type=admin --sprint=def456`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if storyCreateName == "" {
			return fmt.Errorf("--name is required")
		}

		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		opts := db.StoryCreateOptions{
			Name:        storyCreateName,
			Description: storyCreateDescription,
			Priority:    storyCreatePriority,
			EpicID:      storyCreateEpic,
			SprintID:    storyCreateSprint,
			UserType:    storyCreateUserType,
		}

		story, err := db.CreateStory(ctx, pool, opts)
		if err != nil {
			return err
		}

		fmt.Printf("Created story %s: %s\n", color.YellowString(story.ShortID), story.Name)
		return nil
	},
}

var storyUpdateCmd = &cobra.Command{
	Use:   "update [story-id]",
	Short: "Update a user story",
	Long: `Update story fields by short ID prefix.

Examples:
  lw story update a1b2 --status=active
  lw story update a1b2 --name="Updated story name" --priority=p2_high`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		pool, err := db.Connect(ctx)
		if err != nil {
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer db.Close()

		opts := db.StoryUpdateOptions{}
		if cmd.Flags().Changed("status") {
			opts.Status = &storyUpdateStatus
		}
		if cmd.Flags().Changed("name") {
			opts.Name = &storyUpdateName
		}
		if cmd.Flags().Changed("priority") {
			opts.Priority = &storyUpdatePriority
		}

		story, err := db.UpdateStory(ctx, pool, args[0], opts)
		if err != nil {
			return err
		}

		fmt.Printf("Updated story %s\n", color.YellowString(story.ShortID))
		return nil
	},
}

func init() {
	// story list flags
	storyListCmd.Flags().StringVarP(&storyListStatus, "status", "s", "", "Filter by status")
	storyListCmd.Flags().StringVar(&storyListEpic, "epic", "", "Filter by epic ID")
	storyListCmd.Flags().StringVar(&storyListSprint, "sprint", "", "Filter by sprint ID")
	storyListCmd.Flags().IntVarP(&storyListLimit, "limit", "n", 50, "Limit number of results")

	// story create flags
	storyCreateCmd.Flags().StringVar(&storyCreateName, "name", "", "Story name (required)")
	storyCreateCmd.Flags().StringVar(&storyCreateDescription, "description", "", "Story description")
	storyCreateCmd.Flags().StringVar(&storyCreatePriority, "priority", "p3_medium", "Priority (p1_urgent, p2_high, p3_medium, p4_low)")
	storyCreateCmd.Flags().StringVar(&storyCreateEpic, "epic", "", "Epic ID")
	storyCreateCmd.Flags().StringVar(&storyCreateSprint, "sprint", "", "Sprint ID")
	storyCreateCmd.Flags().StringVar(&storyCreateUserType, "user-type", "", "User type")

	// story update flags
	storyUpdateCmd.Flags().StringVar(&storyUpdateStatus, "status", "", "Status")
	storyUpdateCmd.Flags().StringVar(&storyUpdateName, "name", "", "Story name")
	storyUpdateCmd.Flags().StringVar(&storyUpdatePriority, "priority", "", "Priority (p1_urgent, p2_high, p3_medium, p4_low)")

	// Add subcommands
	storyCmd.AddCommand(storyListCmd)
	storyCmd.AddCommand(storyCreateCmd)
	storyCmd.AddCommand(storyUpdateCmd)
}

func printStoryTable(stories []db.Story) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ID", "Name", "Status", "Priority", "Points"})
	table.SetBorder(false)
	table.SetHeaderColor(
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
		tablewriter.Colors{tablewriter.Bold, tablewriter.FgCyanColor},
	)

	for _, s := range stories {
		name := s.Name
		if len(name) > 50 {
			name = name[:47] + "..."
		}

		points := "-"
		if s.StoryPoints != nil {
			points = fmt.Sprintf("%d", *s.StoryPoints)
		}

		table.Rich([]string{
			s.ShortID,
			name,
			s.Status,
			s.Priority,
			points,
		}, []tablewriter.Colors{
			{tablewriter.FgYellowColor},
			{},
			{},
			getPriorityColor(s.Priority),
			{},
		})
	}

	table.Render()
	fmt.Printf("\n%s stories\n", color.CyanString("%d", len(stories)))
}
