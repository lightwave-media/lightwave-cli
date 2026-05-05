package cli

import (
	"context"
	"fmt"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
)

// Schema-driven sprint handlers. Wired via dispatcher (commands.yaml v3.0.0).
// Schema declares: list, current, tasks. Heavier capabilities (start, complete,
// plan, auto-plan, check) live in legacy sprint.go and are no longer exposed
// via the cobra root — kept temporarily for orchestrator.go callers awaiting
// Phase 5 sweep.

func init() {
	RegisterHandler("sprint.list", sprintListHandler)
	RegisterHandler("sprint.current", sprintCurrentHandler)
	RegisterHandler("sprint.tasks", sprintTasksHandler)
}

func sprintListHandler(ctx context.Context, _ []string, flags map[string]any) error {
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	sprints, err := db.ListSprints(ctx, pool, db.SprintListOptions{Limit: 50})
	if err != nil {
		return err
	}

	if asJSON(flags) {
		return emitJSON(sprints)
	}

	if len(sprints) == 0 {
		fmt.Println(color.YellowString("No sprints found"))
		return nil
	}
	printSprintTable(sprints)
	return nil
}

func sprintCurrentHandler(ctx context.Context, _ []string, flags map[string]any) error {
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	sprints, err := db.ListSprints(ctx, pool, db.SprintListOptions{Status: "active", Limit: 1})
	if err != nil {
		return err
	}

	if asJSON(flags) {
		if len(sprints) == 0 {
			return emitJSON(nil)
		}
		return emitJSON(sprints[0])
	}

	if len(sprints) == 0 {
		fmt.Println(color.YellowString("No active sprint"))
		return nil
	}
	printSprintTable(sprints[:1])
	return nil
}

func sprintTasksHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("sprint id required")
	}
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	sprint, err := db.GetSprint(ctx, pool, args[0])
	if err != nil {
		return err
	}

	tasks, err := db.ListTasks(ctx, pool, db.TaskListOptions{SprintID: sprint.ShortID, Limit: 100})
	if err != nil {
		return err
	}

	if asJSON(flags) {
		return emitJSON(tasks)
	}

	if len(tasks) == 0 {
		fmt.Printf("Sprint %s has no tasks\n", color.YellowString(sprint.ShortID))
		return nil
	}
	fmt.Printf("Sprint %s: %s (%d tasks)\n", color.YellowString(sprint.ShortID), sprint.Name, len(tasks))
	for _, t := range tasks {
		fmt.Printf("  %s [%s] %s\n", color.CyanString(t.ShortID), t.Status, t.Title)
	}
	return nil
}

// asJSON returns true when --json was passed (dispatcher only inserts the key
// if cobra saw the flag changed).
func asJSON(flags map[string]any) bool {
	v, ok := flags["json"]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}
