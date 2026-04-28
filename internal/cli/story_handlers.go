package cli

import (
	"context"
	"fmt"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
)

// Schema-driven story handlers. commands.yaml v3.0.0 declares: list, show, link.
// Heavy interview / record / complete-round flows are out of scope for lw —
// they live in Python/Celery per the prune.

func init() {
	RegisterHandler("story.list", storyListHandler)
	RegisterHandler("story.show", storyShowHandler)
	RegisterHandler("story.link", storyLinkHandler)
}

func storyListHandler(ctx context.Context, _ []string, flags map[string]any) error {
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	stories, err := db.ListStories(ctx, pool, db.StoryListOptions{Limit: 50})
	if err != nil {
		return err
	}

	if asJSON(flags) {
		return emitJSON(stories)
	}
	if len(stories) == 0 {
		fmt.Println(color.YellowString("No stories found"))
		return nil
	}
	printStoryTable(stories)
	return nil
}

func storyShowHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("story id required")
	}
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	story, err := db.GetStory(ctx, pool, args[0])
	if err != nil {
		return err
	}

	if asJSON(flags) {
		return emitJSON(story)
	}

	fmt.Printf("Story %s: %s\n", color.YellowString(story.ShortID), story.Name)
	fmt.Printf("  Status:   %s\n", story.Status)
	fmt.Printf("  Priority: %s\n", story.Priority)
	if story.UserType != nil {
		fmt.Printf("  User:     %s\n", *story.UserType)
	}
	if story.Description != nil && *story.Description != "" {
		fmt.Printf("\n%s\n", *story.Description)
	}
	return nil
}

func storyLinkHandler(ctx context.Context, args []string, _ map[string]any) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: lw story link <story-id> <task-id>")
	}
	storyID, taskID := args[0], args[1]

	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	task, err := db.UpdateTask(ctx, pool, taskID, db.TaskUpdateOptions{StoryID: &storyID})
	if err != nil {
		return err
	}
	fmt.Printf("Linked task %s → story %s\n",
		color.CyanString(task.ShortID), color.YellowString(storyID))
	return nil
}
