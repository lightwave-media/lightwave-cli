package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
)

// Schema-driven task handlers. commands.yaml v3.0.0 declares 7 commands:
// list, create, start, status, pr, done, info.
//
// `create` preserves the atomic createOS + Paperclip + GitHub fan-out from
// the legacy taskCreateCmd (per ~/.brain/memory/feedback/2026-04-27-one-
// command-issue-creation.yaml — single command surface, no parallel
// `lw paperclip issue create` etc.). The handler unpacks the dispatcher
// flag map into the legacy globals and invokes runTaskCreate.

func init() {
	RegisterHandler("task.list", taskListHandler)
	RegisterHandler("task.info", taskInfoHandler)
	RegisterHandler("task.create", taskCreateHandler)
	RegisterHandler("task.start", taskStartHandler)
	RegisterHandler("task.status", taskStatusHandler)
	RegisterHandler("task.pr", taskPRHandler)
	RegisterHandler("task.done", taskDoneHandler)
}

func taskListHandler(ctx context.Context, _ []string, flags map[string]any) error {
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	opts := db.TaskListOptions{
		Status:   flagStr(flags, "status"),
		Priority: flagStr(flags, "priority"),
		TaskType: flagStr(flags, "type"),
		EpicID:   flagStr(flags, "epic"),
		SprintID: flagStr(flags, "sprint"),
		Limit:    50,
	}
	if v, ok := flags["limit"]; ok {
		if s, ok := v.(string); ok && s != "" {
			var n int
			if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n > 0 {
				opts.Limit = n
			}
		}
	}
	if flagBool(flags, "all") {
		opts.Limit = 0
	}

	tasks, err := db.ListTasks(ctx, pool, opts)
	if err != nil {
		return err
	}

	if asJSON(flags) {
		return emitJSON(tasks)
	}
	if len(tasks) == 0 {
		fmt.Println(color.YellowString("No tasks found"))
		return nil
	}
	printTaskTable(tasks)
	return nil
}

func taskInfoHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("task id required")
	}
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	task, err := db.GetTask(ctx, pool, args[0])
	if err != nil {
		return err
	}
	if asJSON(flags) {
		return emitJSON(task)
	}
	printTaskDetails(task)
	return nil
}

// taskCreateHandler bridges the dispatcher flag map into the legacy
// taskCreate* globals so runTaskCreate (the proven atomic fan-out) works
// unmodified. Title is the positional arg.
func taskCreateHandler(_ context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("title argument required: lw task create <title>")
	}

	taskCreateTitle = args[0]
	taskCreateDescription = flagStr(flags, "description")
	taskCreateDescriptionFile = flagStr(flags, "description-file")
	taskCreatePriority = "p3_medium"
	if v := flagStr(flags, "priority"); v != "" {
		taskCreatePriority = v
	}
	taskCreateType = "feature"
	if v := flagStr(flags, "type"); v != "" {
		taskCreateType = v
	}
	taskCreateCategory = flagStr(flags, "category")
	taskCreateEpic = flagStr(flags, "epic")
	taskCreateSprint = flagStr(flags, "sprint")
	taskCreateStory = flagStr(flags, "story")
	taskCreatePRD = flagStr(flags, "prd")
	taskCreatePlan = flagStr(flags, "plan")
	taskCreateDocs = flagSlice(flags, "doc")
	taskCreateAttach = flagSlice(flags, "attach")
	taskCreateLabels = flagSlice(flags, "label")
	taskCreateParent = flagStr(flags, "parent")
	taskCreateAssign = flagStr(flags, "assign")
	taskCreateBlocks = flagSlice(flags, "blocks")
	taskCreateBlockedBy = flagSlice(flags, "blocked-by")
	taskCreateBillingCode = flagStr(flags, "billing-code")
	taskCreateProject = flagStr(flags, "project")
	taskCreateProjectWS = flagStr(flags, "project-workspace")
	taskCreateJSON = flagBool(flags, "json")
	taskCreateDryRun = flagBool(flags, "dry-run")

	return runTaskCreate(nil, args)
}

// taskStartHandler moves a task to in_progress and creates a feature branch
// named after the task's short id and a slugified title.
func taskStartHandler(ctx context.Context, args []string, _ map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("task id required")
	}
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	task, err := db.GetTask(ctx, pool, args[0])
	if err != nil {
		return err
	}

	branch := fmt.Sprintf("feature/%s-%s", task.ShortID, slugify(task.Title))
	status := "in_progress"
	updated, err := db.UpdateTask(ctx, pool, task.ID, db.TaskUpdateOptions{
		Status: &status,
		Branch: &branch,
	})
	if err != nil {
		return err
	}

	// Create + checkout branch (best-effort; surface error but don't roll back DB).
	if out, err := exec.Command("git", "checkout", "-b", branch).CombinedOutput(); err != nil {
		fmt.Printf("%s git checkout: %v\n%s", color.YellowString("Warning:"), err, string(out))
	}

	fmt.Printf("Started task %s — branch %s\n",
		color.CyanString(updated.ShortID), color.YellowString(branch))
	return nil
}

// taskStatusHandler updates a task's status. Args: id, status.
func taskStatusHandler(ctx context.Context, args []string, _ map[string]any) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: lw task status <id> <status>")
	}
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	status := args[1]
	updated, err := db.UpdateTask(ctx, pool, args[0], db.TaskUpdateOptions{Status: &status})
	if err != nil {
		return err
	}
	fmt.Printf("Task %s → %s\n",
		color.CyanString(updated.ShortID), colorStatus(status, status))
	return nil
}

// taskPRHandler opens a PR for the current branch and persists the URL on
// the task. Delegates branch + base resolution to gh.
func taskPRHandler(ctx context.Context, args []string, _ map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("task id required")
	}
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	task, err := db.GetTask(ctx, pool, args[0])
	if err != nil {
		return err
	}

	title := fmt.Sprintf("[%s] %s", task.ShortID, task.Title)
	body := fmt.Sprintf("Task: %s\nID: %s\n", task.ShortID, task.ID)
	if task.Description != nil && *task.Description != "" {
		body += "\n" + *task.Description + "\n"
	}

	out, err := exec.Command("gh", "pr", "create",
		"--repo", defaultGHRepo,
		"--title", title,
		"--body", body,
		"--fill-first",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr create: %w\n%s", err, string(out))
	}

	url := strings.TrimSpace(string(out))
	updated, err := db.UpdateTask(ctx, pool, task.ID, db.TaskUpdateOptions{
		PRUrl:  &url,
		Status: ptrStr("in_review"),
	})
	if err != nil {
		return fmt.Errorf("recording PR url: %w", err)
	}

	fmt.Printf("PR opened for %s: %s\n",
		color.CyanString(updated.ShortID), color.BlueString(url))
	return nil
}

func taskDoneHandler(ctx context.Context, args []string, _ map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("task id required")
	}
	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	status := "done"
	updated, err := db.UpdateTask(ctx, pool, args[0], db.TaskUpdateOptions{Status: &status})
	if err != nil {
		return err
	}
	fmt.Printf("Task %s marked %s\n",
		color.CyanString(updated.ShortID), color.GreenString("done"))
	return nil
}

// flagSlice extracts a repeatable string flag from the dispatcher flag map.
// Dispatcher inserts the slice value only when the flag was changed.
func flagSlice(flags map[string]any, name string) []string {
	v, ok := flags[name]
	if !ok {
		return nil
	}
	s, _ := v.([]string)
	return s
}

func ptrStr(s string) *string { return &s }

// slugify produces a kebab-case slug truncated to 40 chars for branch naming.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 40 {
		out = strings.TrimRight(out[:40], "-")
	}
	if out == "" {
		out = "task"
	}
	return out
}
