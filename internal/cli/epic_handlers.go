package cli

import (
	"context"
	"fmt"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
)

// Schema-driven epic handlers. commands.yaml v3.0.0 declares: list, info, tasks.

func init() {
	RegisterHandler("epic.list", epicListHandler)
	RegisterHandler("epic.info", epicInfoHandler)
	RegisterHandler("epic.tasks", epicTasksHandler)
}

func epicListHandler(ctx context.Context, _ []string, flags map[string]any) error {
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	epics, err := db.ListEpics(ctx, pool, db.EpicListOptions{Limit: 50})
	if err != nil {
		return err
	}

	if asJSON(flags) {
		return emitJSON(epics)
	}
	if len(epics) == 0 {
		fmt.Println(color.YellowString("No epics found"))
		return nil
	}
	printEpicTable(epics)
	return nil
}

func epicInfoHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("epic id required")
	}
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	epic, err := db.GetEpic(ctx, pool, args[0])
	if err != nil {
		return err
	}

	if asJSON(flags) {
		return emitJSON(epic)
	}

	fmt.Printf("Epic %s: %s\n", color.YellowString(epic.ShortID), epic.Name)
	fmt.Printf("  Status:   %s\n", epic.Status)
	if epic.Priority != nil {
		fmt.Printf("  Priority: %s\n", *epic.Priority)
	}
	if epic.GithubRepo != nil {
		fmt.Printf("  Repo:     %s\n", *epic.GithubRepo)
	}
	fmt.Printf("  Tasks:    %d\n", epic.TaskCount)
	return nil
}

func epicTasksHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("epic id required")
	}
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	epic, err := db.GetEpic(ctx, pool, args[0])
	if err != nil {
		return err
	}

	tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{EpicID: epic.ID, Limit: 100})
	if err != nil {
		return err
	}

	if asJSON(flags) {
		return emitJSON(tasks)
	}

	if len(tasks) == 0 {
		fmt.Printf("Epic %s has no tasks\n", color.YellowString(epic.ShortID))
		return nil
	}
	fmt.Printf("Epic %s: %s (%d tasks)\n", color.YellowString(epic.ShortID), epic.Name, len(tasks))
	for _, t := range tasks {
		fmt.Printf("  %s [%s] %s\n", color.CyanString(t.ShortID), t.Status, t.Title)
	}
	return nil
}
