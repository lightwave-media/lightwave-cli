package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/mddocs"
	"github.com/lightwave-media/lightwave-cli/internal/mdindex"
	"github.com/lightwave-media/lightwave-cli/internal/mdtasks"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

// `lw task new|edit|close` + `lw task index` — Phase 3 / EB-001 plan §3.
//
// Markdown-canonical CRUD for tasks under `lightwave-media/docs/<domain>/
// tasks/`. The legacy Postgres `lw task create|update|done` surface
// remains for the Paperclip + GitHub fan-out flow; these new verbs are
// the v_core / markdown-canonical path.
//
// All four attach via AttachOrphanTaskCommands (the same bridge as
// US-003's `lw task fetch-context`) until the lightwave-core schema
// catches up.

// Re-exported from task_fetch_context.go's AttachOrphanTaskCommands
// hook — this file just adds more commands to the same bridge.
func init() {
	registerOrphanTaskCommand(newTaskMdNewCmd())
	registerOrphanTaskCommand(newTaskMdEditCmd())
	registerOrphanTaskCommand(newTaskMdCloseCmd())
	registerOrphanTaskCommand(newTaskMdIndexCmd())
}

// orphanTaskCommandsExtra is appended-to by init()s in this file; the
// existing AttachOrphanTaskCommands (in task_fetch_context.go) is
// extended below to consume it.
var orphanTaskCommandsExtra []*cobra.Command

func registerOrphanTaskCommand(c *cobra.Command) {
	orphanTaskCommandsExtra = append(orphanTaskCommandsExtra, c)
}

// (The slice is read inside task_fetch_context.go's AttachOrphanTaskCommands.)

// ----------------------------------------------------------------------
// lw task new
// ----------------------------------------------------------------------

var (
	taskMdNewDomain         string
	taskMdNewTitle          string
	taskMdNewBody           string
	taskMdNewBodyFile       string
	taskMdNewOwner          string
	taskMdNewCreatedBy      string
	taskMdNewAssignedTo     string
	taskMdNewParentStory    string
	taskMdNewParentEpic     string
	taskMdNewAssignedSprint string
	taskMdNewPriority       string
	taskMdNewStatus         string
	taskMdNewJSON           bool
	taskMdNewDryRun         bool
)

func newTaskMdNewCmd() *cobra.Command {
	c := &cobra.Command{
		Use:          "new",
		Short:        "Create a markdown task under docs/<domain>/tasks/ (US Phase 3)",
		SilenceUsage: true,
		Long: `Create a new markdown-canonical Task at
lightwave-media/docs/<domain>/tasks/T-NNNN-<slug>.md. Frontmatter
honours documentation-workflow.md §4: id, domain, type, title, status,
owner, created_by, assigned_to, created_at, updated_at + optional
linkage (parent_story, parent_epic, assigned_sprint, priority).

The ID is auto-allocated as the next available T-NNNN across all
domains (zero-padded to 4 digits).

Examples:
  lw task new --domain software --title "Audit cdn allowlist"
  lw task new --domain software --title "Smoke" --body-file ./body.md \
              --parent-story US-003 --assigned-sprint SPR-001 \
              --priority p2_high --assigned-to platform-engineer`,
		RunE: runTaskMdNew,
	}
	c.Flags().StringVar(&taskMdNewDomain, "domain", "", "Domain (software, cinematography, …) — required")
	c.Flags().StringVar(&taskMdNewTitle, "title", "", "Task title (required)")
	c.Flags().StringVar(&taskMdNewBody, "body", "", "Inline markdown body (under the frontmatter fence)")
	c.Flags().StringVar(&taskMdNewBodyFile, "body-file", "", "Read body from file")
	c.Flags().StringVar(&taskMdNewOwner, "owner", "", "Owner user-id (default: joel)")
	c.Flags().StringVar(&taskMdNewCreatedBy, "created-by", "", "created_by user-id (default: matches --owner)")
	c.Flags().StringVar(&taskMdNewAssignedTo, "assigned-to", "", "assigned_to user-id (default: matches --owner)")
	c.Flags().StringVar(&taskMdNewParentStory, "parent-story", "", "Parent User Story id (US-NNN)")
	c.Flags().StringVar(&taskMdNewParentEpic, "parent-epic", "", "Parent Epic Brief id (EB-NNN)")
	c.Flags().StringVar(&taskMdNewAssignedSprint, "assigned-sprint", "", "Sprint id (SPR-NNN)")
	c.Flags().StringVar(&taskMdNewPriority, "priority", "", "Priority (p1_urgent|p2_high|p3_medium|p4_low)")
	c.Flags().StringVar(&taskMdNewStatus, "status", "", "Initial status (default: draft)")
	c.Flags().BoolVar(&taskMdNewJSON, "json", false, "Emit JSON envelope on success")
	c.Flags().BoolVar(&taskMdNewDryRun, "dry-run", false, "Resolve ID + filename, do not write")
	_ = c.MarkFlagRequired("domain")
	_ = c.MarkFlagRequired("title")

	return c
}

func runTaskMdNew(_ *cobra.Command, _ []string) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	body := taskMdNewBody
	if taskMdNewBodyFile != "" {
		if taskMdNewBody != "" {
			return errors.New("--body and --body-file are mutually exclusive")
		}

		b, err := os.ReadFile(taskMdNewBodyFile)
		if err != nil {
			return fmt.Errorf("read %s: %w", taskMdNewBodyFile, err)
		}

		body = string(b)
	}

	opts := mdtasks.NewOptions{
		LightwaveRoot:  cfg.Paths.LightwaveRoot,
		Domain:         taskMdNewDomain,
		Title:          taskMdNewTitle,
		Body:           body,
		Owner:          taskMdNewOwner,
		CreatedBy:      taskMdNewCreatedBy,
		AssignedTo:     taskMdNewAssignedTo,
		ParentStory:    taskMdNewParentStory,
		ParentEpic:     taskMdNewParentEpic,
		AssignedSprint: taskMdNewAssignedSprint,
		Priority:       taskMdNewPriority,
		Status:         taskMdNewStatus,
	}

	if taskMdNewDryRun {
		nextID, err := mdtasks.NextTaskID(opts.LightwaveRoot)
		if err != nil {
			return err
		}

		fmt.Println(color.CyanString("DRY RUN — no file written"))
		fmt.Printf("Domain:  %s\n", opts.Domain)
		fmt.Printf("Next ID: %s\n", nextID)
		fmt.Printf("Title:   %s\n", opts.Title)
		fmt.Printf("Body:    %d bytes\n", len(opts.Body))

		return nil
	}

	path, id, err := mdtasks.New(opts)
	if err != nil {
		return err
	}

	if taskMdNewJSON {
		return emitJSON(map[string]any{
			"id":     id,
			"path":   path,
			"domain": opts.Domain,
			"title":  opts.Title,
		})
	}

	fmt.Printf("created %s at %s\n", color.CyanString(id), path)

	return nil
}

// ----------------------------------------------------------------------
// lw task edit
// ----------------------------------------------------------------------

var (
	taskMdEditDomain         string
	taskMdEditTitle          string
	taskMdEditStatus         string
	taskMdEditAssignedTo     string
	taskMdEditParentStory    string
	taskMdEditParentEpic     string
	taskMdEditAssignedSprint string
	taskMdEditPriority       string
)

func newTaskMdEditCmd() *cobra.Command {
	c := &cobra.Command{
		Use:          "edit <task-id>",
		Short:        "Update a markdown task's frontmatter fields",
		SilenceUsage: true,
		Long: `Edit specific frontmatter fields on a markdown Task. Unknown
frontmatter keys (story_points, implementation_target, refs_*, etc.)
survive untouched. updated_at is set automatically.

Examples:
  lw task edit T-0001 --status ready
  lw task edit T-0001 --assigned-to platform-engineer --priority p2_high
  lw task edit T-0042 --assigned-sprint SPR-002`,
		Args: cobra.ExactArgs(1),
		RunE: runTaskMdEdit,
	}
	c.Flags().StringVar(&taskMdEditDomain, "domain", "", "Restrict lookup to a single domain (faster)")
	c.Flags().StringVar(&taskMdEditTitle, "title", "", "Update title")
	c.Flags().StringVar(&taskMdEditStatus, "status", "", "Update status (draft|ready|in_progress|in_review|done|blocked)")
	c.Flags().StringVar(&taskMdEditAssignedTo, "assigned-to", "", "Update assigned_to")
	c.Flags().StringVar(&taskMdEditParentStory, "parent-story", "", "Update parent_story")
	c.Flags().StringVar(&taskMdEditParentEpic, "parent-epic", "", "Update parent_epic")
	c.Flags().StringVar(&taskMdEditAssignedSprint, "assigned-sprint", "", "Update assigned_sprint")
	c.Flags().StringVar(&taskMdEditPriority, "priority", "", "Update priority")

	return c
}

func runTaskMdEdit(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	id := args[0]
	opts := mdtasks.EditOptions{
		LightwaveRoot: cfg.Paths.LightwaveRoot,
		Domain:        taskMdEditDomain,
	}
	any := false

	if cmd.Flags().Changed("title") {
		opts.Title = &taskMdEditTitle
		any = true
	}

	if cmd.Flags().Changed("status") {
		opts.Status = &taskMdEditStatus
		any = true
	}

	if cmd.Flags().Changed("assigned-to") {
		opts.AssignedTo = &taskMdEditAssignedTo
		any = true
	}

	if cmd.Flags().Changed("parent-story") {
		opts.ParentStory = &taskMdEditParentStory
		any = true
	}

	if cmd.Flags().Changed("parent-epic") {
		opts.ParentEpic = &taskMdEditParentEpic
		any = true
	}

	if cmd.Flags().Changed("assigned-sprint") {
		opts.AssignedSprint = &taskMdEditAssignedSprint
		any = true
	}

	if cmd.Flags().Changed("priority") {
		opts.Priority = &taskMdEditPriority
		any = true
	}

	if !any {
		return errors.New("no fields to update — pass at least one --status / --assigned-to / etc.")
	}

	path, err := mdtasks.Edit(id, opts)
	if err != nil {
		return err
	}

	fmt.Printf("edited %s -> %s\n", color.CyanString(id), path)

	return nil
}

// ----------------------------------------------------------------------
// lw task close
// ----------------------------------------------------------------------

var taskMdCloseDomain string

func newTaskMdCloseCmd() *cobra.Command {
	c := &cobra.Command{
		Use:          "close <task-id>",
		Short:        "Mark a markdown task as done (shortcut for --status done)",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE:         runTaskMdClose,
	}
	c.Flags().StringVar(&taskMdCloseDomain, "domain", "", "Restrict lookup to a single domain")

	return c
}

func runTaskMdClose(_ *cobra.Command, args []string) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	path, err := mdtasks.Close(args[0], cfg.Paths.LightwaveRoot, taskMdCloseDomain)
	if err != nil {
		return err
	}

	fmt.Printf("closed %s — %s\n", color.CyanString(args[0]), path)

	return nil
}

// ----------------------------------------------------------------------
// lw task index
// ----------------------------------------------------------------------

var (
	taskMdIndexJSON   bool
	taskMdIndexPretty bool
)

func newTaskMdIndexCmd() *cobra.Command {
	c := &cobra.Command{
		Use:          "index",
		Short:        "Rebuild the markdown-artefact cache at ~/.lightwave/state.json",
		SilenceUsage: true,
		Long: `Walk every domain's docs/<domain>/{tasks,user-stories,epic-briefs,
sprints}/ and rebuild the read-fast cache at ~/.lightwave/state.json.

Re-runnable from clean slate — the cache is never canonical. Markdown
frontmatter is the source of truth per documentation-workflow.md §7.

JSON now; Phase B (EB-005) swaps for Postgres sync via lightwave-
platform without changing this CLI surface.

Examples:
  lw task index             # rebuild + print stats
  lw task index --json      # rebuild + emit the full index on stdout
  lw task index --pretty    # rebuild + print a table summary`,
		RunE: runTaskMdIndex,
	}
	c.Flags().BoolVar(&taskMdIndexJSON, "json", false, "Print the full Index JSON on stdout (also writes to disk)")
	c.Flags().BoolVar(&taskMdIndexPretty, "pretty", false, "Print a table of indexed entries grouped by kind")

	return c
}

func runTaskMdIndex(_ *cobra.Command, _ []string) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	idx, err := mdindex.Build(cfg.Paths.LightwaveRoot)
	if err != nil {
		return err
	}

	path, err := idx.Write()
	if err != nil {
		return err
	}

	if taskMdIndexJSON {
		return emitJSON(idx)
	}

	fmt.Printf("indexed %s (%s entries) -> %s\n",
		color.CyanString(filepath.Base(cfg.Paths.LightwaveRoot)),
		color.YellowString("%d", idx.Stats["total"]),
		path)

	for _, k := range []string{"task", "user-story", "epic-brief", "sprint"} {
		if n := idx.Stats[k]; n > 0 {
			fmt.Printf("  %-12s %d\n", k, n)
		}
	}

	if n := idx.Stats["parse_errors"]; n > 0 {
		fmt.Println(color.RedString("  parse_errors %d (run with --json for paths)", n))
	}

	if taskMdIndexPretty {
		printIndexTable(idx)
	}

	return nil
}

func printIndexTable(idx *mdindex.Index) {
	if len(idx.Entries) == 0 {
		return
	}

	tbl := tablewriter.NewWriter(os.Stdout)
	tbl.SetHeader([]string{"ID", "Kind", "Domain", "Status", "Title"})
	tbl.SetBorder(false)

	for _, e := range idx.Entries {
		title := e.Title
		if len(title) > 60 {
			title = title[:57] + "…"
		}

		tbl.Append([]string{e.ID, e.Kind, e.Domain, e.Status, title})
	}

	fmt.Println()
	tbl.Render()
}

// ensureMddocsImport keeps the mddocs import live even if the command
// surface above changes — fetch-context calls into it, and we want
// `lw task` users to see consistent path semantics.
var _ = mddocs.DocsRoot
