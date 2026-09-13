package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/spf13/cobra"
)

// taskCreateResult is the JSON output payload from `lw task create`.
// Contains all three system identifiers (createOS, Paperclip, GitHub) so
// downstream tooling and humans can navigate to any surface.
type taskCreateResult struct {
	CreateosTaskID      string      `json:"createos_task_id"`
	CreateosShortID     string      `json:"createos_short_id"`
	PaperclipIssueID    string      `json:"paperclip_issue_id,omitempty"`
	PaperclipIdentifier string      `json:"paperclip_identifier,omitempty"`
	PaperclipURL        string      `json:"paperclip_url,omitempty"`
	GitHubURL           string      `json:"github_url,omitempty"`
	Documents           []docRef    `json:"documents,omitempty"`
	Attachments         []attachRef `json:"attachments,omitempty"`
	Labels              []string    `json:"labels,omitempty"`
	Warnings            []string    `json:"warnings,omitempty"`
	GitHubIssueNumber   int         `json:"github_issue_number,omitempty"`
	DryRun              bool        `json:"dry_run,omitempty"`
}

type docRef struct {
	Key      string `json:"key"`
	Revision int    `json:"revision,omitempty"`
}

type attachRef struct {
	Path string `json:"path"`
	ID   string `json:"id,omitempty"`
}

// runTaskCreate is the fan-out implementation backing `lw task create`.
//
// Flow:
//  1. Validate flags; reject the Paperclip-only ones (see paperclipOnlyFlags)
//  2. Resolve description body
//  3. Dry-run: print intent and exit — no network, no database
//  4. db.CreateTask (createOS canonical record)
//  5. createGitHubIssueForTask, carrying --assign and --label
//  6. Persist the GitHub cross-ref
//  7. Print the identifiers (text or JSON)
//
// The Paperclip leg is gone (#351). It called a local service on :3100 that
// belongs to the retired Django-era stack and has not been running; every call
// against it returned connection refused. createOS is fail-fast (it is the
// canonical record); the GitHub leg degrades to a warning so the task still
// lands as a usable record.
func runTaskCreate(cmd *cobra.Command, args []string) error {
	if taskCreateTitle == "" {
		return errors.New("--title is required")
	}

	if taskCreateDescription != "" && taskCreateDescriptionFile != "" {
		return errors.New("--description and --description-file are mutually exclusive")
	}

	if err := rejectPaperclipOnlyFlags(); err != nil {
		return err
	}

	body, err := resolveTaskBody()
	if err != nil {
		return err
	}

	// Dry-run BEFORE any I/O. It used to sit after a Paperclip agent lookup, so
	// `--assign x --dry-run` made a network call and failed — a preview with a
	// side effect, against this repo's own destructive-command standard (#351).
	if taskCreateDryRun {
		return printDryRun(body, taskCreateAssign)
	}

	ctx := context.Background()

	pool, err := db.Connect(ctx)
	if err != nil {
		return fmt.Errorf("database connection failed: %w", err)
	}
	defer db.Close()

	createOpts := db.TaskCreateOptions{
		Title:       taskCreateTitle,
		Description: body,
		Priority:    taskCreatePriority,
		TaskType:    taskCreateType,
		Category:    taskCreateCategory,
		EpicID:      taskCreateEpic,
		SprintID:    taskCreateSprint,
		StoryID:     taskCreateStory,
	}

	task, err := db.CreateTask(ctx, pool, createOpts)
	if err != nil {
		return fmt.Errorf("createOS: create task: %w", err)
	}

	result := taskCreateResult{
		CreateosTaskID:  task.ID,
		CreateosShortID: task.ShortID,
	}

	if taskCreateSkipGitHub {
		result.Warnings = append(result.Warnings, "GitHub leg skipped (--skip-github)")

		return printTaskCreateResult(task, result)
	}

	issueNum, ghErr := createGitHubIssueForTask(task, taskCreateAssign, taskCreateLabels)
	if ghErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("github: %v", ghErr))
	} else if issueNum > 0 {
		result.GitHubIssueNumber = issueNum
		result.GitHubURL = fmt.Sprintf("https://github.com/%s/issues/%d", defaultGHRepo, issueNum)
		result.Labels = taskCreateLabels

		// Cross-ref still overloads notion_id. The comment here used to say the
		// proper column "requires a Django migration"; there is no Django, and
		// the column question is now a Go-side schema change.
		legacyRef := fmt.Sprintf("gh-%d", issueNum)
		if _, err := db.UpdateTaskNotionID(ctx, pool, task.ID, legacyRef); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("store github cross-ref: %v", err))
		}
	}

	return printTaskCreateResult(task, result)
}

// paperclipOnlyFlags are the flags whose ONLY implementation was the Paperclip
// leg, with the reason each is not silently carried forward.
//
// They were inert before this change, not merely Paperclip-bound: the block
// that consumed them sat inside `else if companyID != ""`, and companyID was
// set only when --assign resolved against Paperclip. So without --assign they
// were silently ignored, and with --assign the command errored before reaching
// them. There was no path on which they worked.
//
// Failing loudly is the point. Silently accepting a flag that does nothing is
// the worst of the three options, and quietly deleting them would make a
// product decision by omission — which of these carry intent worth re-homing is
// a call for a person, and #351 says so explicitly. An error keeps the choice
// visible.
var paperclipOnlyFlags = []struct {
	value  func() bool
	name   string
	reason string
}{
	{name: "prd", value: func() bool { return taskCreatePRD != "" },
		reason: "document storage was a Paperclip capability; spec/ artifacts referenced from the issue body are the likely home"},
	{name: "plan", value: func() bool { return taskCreatePlan != "" },
		reason: "document storage was a Paperclip capability; spec/ artifacts referenced from the issue body are the likely home"},
	{name: "doc", value: func() bool { return len(taskCreateDocs) > 0 },
		reason: "document storage was a Paperclip capability; spec/ artifacts referenced from the issue body are the likely home"},
	{name: "attach", value: func() bool { return len(taskCreateAttach) > 0 },
		reason: "attachment upload was a Paperclip capability; GitHub issue attachments are the likely home"},
	{name: "parent", value: func() bool { return taskCreateParent != "" },
		reason: "the task graph lived in Paperclip; GitHub issue links or the createOS task graph are the candidates"},
	{name: "blocked-by", value: func() bool { return len(taskCreateBlockedBy) > 0 },
		reason: "the task graph lived in Paperclip; GitHub issue links or the createOS task graph are the candidates"},
	{name: "blocks", value: func() bool { return len(taskCreateBlocks) > 0 },
		reason: "the task graph lived in Paperclip; GitHub issue links or the createOS task graph are the candidates"},
	{name: "project", value: func() bool { return taskCreateProject != "" },
		reason: "a Paperclip-domain concept with no current analogue"},
	{name: "project-workspace", value: func() bool { return taskCreateProjectWS != "" },
		reason: "a Paperclip-domain concept with no current analogue"},
	{name: "billing-code", value: func() bool { return taskCreateBillingCode != "" },
		reason: "a Paperclip-domain concept with no current analogue"},
}

// rejectPaperclipOnlyFlags errors on any flag whose only implementation was the
// retired Paperclip leg.
func rejectPaperclipOnlyFlags() error {
	for _, f := range paperclipOnlyFlags {
		if !f.value() {
			continue
		}

		return fmt.Errorf(
			"--%s was implemented only by the retired Paperclip leg and has been inert since "+
				"before it was retired (#351): %s. It is not silently ignored, and it is not "+
				"deleted, because which of these to re-home is a product call rather than "+
				"something to infer from code",
			f.name, f.reason)
	}

	return nil
}

type docInput struct {
	key  string
	path string
	body []byte
}

// resolveTaskBody returns the description body from --description or
// --description-file, expanding \n in inline strings.
func resolveTaskBody() (string, error) {
	if taskCreateDescriptionFile != "" {
		raw, err := os.ReadFile(taskCreateDescriptionFile)
		if err != nil {
			return "", fmt.Errorf("read --description-file %s: %w", taskCreateDescriptionFile, err)
		}

		return string(raw), nil
	}

	return strings.ReplaceAll(taskCreateDescription, `\n`, "\n"), nil
}

func ptr[T any](v T) *T { return &v }

// printDryRun renders the resolved intent without making any mutations.
// printDryRun previews the fan-out without touching the network or the database.
//
// It previously ran AFTER a Paperclip agent lookup, so `--assign x --dry-run`
// made a network call and errored — a preview with a side effect (#351). It is
// now the first thing that happens after flag validation.
func printDryRun(body, assignee string) error {
	if taskCreateJSON {
		out := taskCreateResult{
			CreateosShortID: "(dry-run)",
			DryRun:          true,
			Labels:          taskCreateLabels,
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(out)
	}

	fmt.Printf("%s lw task create (dry-run)\n", color.CyanString("→"))
	fmt.Printf("  Title:    %s\n", taskCreateTitle)
	fmt.Printf("  Priority: %s\n", taskCreatePriority)
	fmt.Printf("  Type:     %s\n", taskCreateType)

	if assignee != "" {
		fmt.Printf("  Assignee: %s (GitHub)\n", assignee)
	}

	if len(taskCreateLabels) > 0 {
		fmt.Printf("  Labels:   %s\n", strings.Join(taskCreateLabels, ", "))
	}

	if len(body) > 0 {
		preview := body
		if len(preview) > dryRunBodyPreview {
			preview = preview[:dryRunBodyPreview] + "…"
		}

		fmt.Printf("  Body:     %d bytes\n            %s\n", len(body), strings.ReplaceAll(preview, "\n", " "))
	}

	fmt.Printf("\n%s no mutations performed.\n", color.YellowString("⚠"))

	return nil
}

// dryRunBodyPreview caps the description preview in dry-run output.
const dryRunBodyPreview = 200

// printTaskCreateResult renders human-readable output OR JSON depending on flags.
func printTaskCreateResult(task *db.Task, result taskCreateResult) error {
	if taskCreateJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(result)
	}

	fmt.Printf("Created task %s: %s\n", color.YellowString(task.ShortID), task.Title)

	if result.PaperclipIdentifier != "" {
		fmt.Printf("  → Paperclip:  %s  %s\n",
			color.CyanString(result.PaperclipIdentifier),
			color.HiBlackString(result.PaperclipURL))
	}

	if result.GitHubIssueNumber > 0 {
		fmt.Printf("  → GitHub:     #%d  %s\n",
			result.GitHubIssueNumber,
			color.HiBlackString(result.GitHubURL))
	}

	if len(result.Documents) > 0 {
		var keys []string
		for _, d := range result.Documents {
			keys = append(keys, d.Key)
		}

		fmt.Printf("  → Documents:  %s\n", strings.Join(keys, ", "))
	}

	if len(result.Attachments) > 0 {
		var names []string
		for _, a := range result.Attachments {
			names = append(names, filepath.Base(a.Path))
		}

		fmt.Printf("  → Attached:   %s\n", strings.Join(names, ", "))
	}

	if len(result.Labels) > 0 {
		fmt.Printf("  → Labels:     %s\n", strings.Join(result.Labels, ", "))
	}

	for _, w := range result.Warnings {
		fmt.Printf("  %s %s\n", color.YellowString("Warning:"), w)
	}

	return nil
}
