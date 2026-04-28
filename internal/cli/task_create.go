package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
	"github.com/lightwave-media/lightwave-cli/internal/paperclip"
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
	GitHubIssueNumber   int         `json:"github_issue_number,omitempty"`
	GitHubURL           string      `json:"github_url,omitempty"`
	Documents           []docRef    `json:"documents,omitempty"`
	Attachments         []attachRef `json:"attachments,omitempty"`
	Labels              []string    `json:"labels,omitempty"`
	Warnings            []string    `json:"warnings,omitempty"`
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

// runTaskCreate is the atomic fan-out implementation backing `lw task create`.
//
// Flow:
//  1. Validate flags + resolve description body
//  2. Resolve assignee → companyID via Paperclip (sets target tenant for the issue)
//  3. Build doc and attachment lists from flags
//  4. Dry-run: print intent and exit
//  5. db.CreateTask (createOS canonical record)
//  6. paperclip.CreateIssue with full metadata (priority, parent, project,
//     billing-code, labels, blockedBy, blocks)
//  7. Resolve labels (find or create) and patch issue.labelIds
//  8. PutDocument for prd/plan/--doc entries
//  9. UploadAttachment for --attach entries
//  10. createGitHubIssueForTask + Projects sync (existing behavior)
//  11. Persist GitHub cross-ref to createos_task.notion_id (legacy column —
//     proper paperclip_issue_id/github_issue_number columns require a Django
//     migration, see plan §4.4)
//  12. Print three-identifier output (text or JSON)
//
// Failure handling: createOS step is fail-fast (nothing to roll back).
// Paperclip create failure marks the createOS task as errored. Per-label,
// per-doc, per-attachment, per-GitHub failures degrade to warnings so the
// task still lands as a usable record.
func runTaskCreate(cmd *cobra.Command, args []string) error {
	if taskCreateTitle == "" {
		return fmt.Errorf("--title is required")
	}
	if taskCreateDescription != "" && taskCreateDescriptionFile != "" {
		return fmt.Errorf("--description and --description-file are mutually exclusive")
	}

	body, err := resolveTaskBody()
	if err != nil {
		return err
	}

	docs, err := collectDocuments()
	if err != nil {
		return err
	}

	attachments, err := collectAttachments()
	if err != nil {
		return err
	}

	ctx := context.Background()
	pc := paperclip.NewClient()

	// Resolve assignee → companyID. Required for Paperclip create.
	var (
		companyID string
		agentID   string
		agentName string
	)
	if taskCreateAssign != "" {
		agents, err := pc.ListAllAgents(ctx)
		if err != nil {
			return fmt.Errorf("paperclip: list agents: %w", err)
		}
		target := findAgentByName(agents, taskCreateAssign)
		if target == nil {
			return fmt.Errorf("agent %q not found in any Paperclip company", taskCreateAssign)
		}
		companyID = target.CompanyID
		agentID = target.ID
		agentName = target.Name
	}

	// Dry run: stop here and print intent.
	if taskCreateDryRun {
		return printDryRun(body, docs, attachments, agentName, companyID)
	}

	// 1. createOS task — canonical local record.
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

	// 2. Paperclip issue (if assignee given). Without an assignee we can't
	//    pick a company — Paperclip leg is skipped, surfaced as a warning.
	if companyID != "" {
		issue := paperclip.Issue{
			Title:              taskCreateTitle,
			Description:        body,
			Status:             "todo",
			AssigneeAgentID:    agentID,
			Priority:           normalizePaperclipPriority(taskCreatePriority),
			ParentID:           taskCreateParent,
			ProjectID:          taskCreateProject,
			ProjectWorkspaceID: taskCreateProjectWS,
			BlockedByIDs:       taskCreateBlockedBy,
			BlocksIDs:          taskCreateBlocks,
			BillingCode:        taskCreateBillingCode,
		}
		created, err := pc.CreateIssue(ctx, companyID, issue)
		if err != nil {
			// Mark createOS task errored — keep going so the user sees the partial state.
			_, _ = db.UpdateTask(ctx, pool, task.ID, db.TaskUpdateOptions{
				Status: ptr("errored"),
			})
			return fmt.Errorf("paperclip: create issue (createOS task %s left in errored state): %w",
				task.ShortID, err)
		}
		result.PaperclipIssueID = created.ID
		result.PaperclipIdentifier = created.Identifier
		result.PaperclipURL = paperclipIssueURL(created.Identifier)

		// 3. Labels — find or create, then attach.
		for _, name := range taskCreateLabels {
			labelID, warn := resolveOrCreateLabel(ctx, pc, companyID, name)
			if warn != "" {
				result.Warnings = append(result.Warnings, warn)
				continue
			}
			if _, err := pc.AddIssueLabel(ctx, created.ID, labelID); err != nil {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("attach label %q: %v", name, err))
				continue
			}
			result.Labels = append(result.Labels, name)
		}

		// 4. Documents — text artifacts (PRD/plan/keyed).
		for _, d := range docs {
			doc, err := pc.PutDocument(ctx, created.ID, d.key, d.body)
			if err != nil {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("put document %q (%s): %v", d.key, d.path, err))
				continue
			}
			result.Documents = append(result.Documents, docRef{
				Key:      doc.Key,
				Revision: doc.Revision,
			})
		}

		// 5. Attachments — binary multipart uploads.
		for _, p := range attachments {
			att, err := pc.UploadAttachmentFromFile(ctx, created.ID, p)
			if err != nil {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("upload attachment %s: %v", p, err))
				continue
			}
			result.Attachments = append(result.Attachments, attachRef{
				Path: p,
				ID:   att.ID,
			})
		}
	} else {
		result.Warnings = append(result.Warnings,
			"no --assign given; Paperclip leg skipped (no company target)")
	}

	// 6. GitHub issue (existing path — preserves current behavior).
	issueNum, ghErr := createGitHubIssueForTask(task)
	if ghErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("github: %v", ghErr))
	} else if issueNum > 0 {
		result.GitHubIssueNumber = issueNum
		result.GitHubURL = fmt.Sprintf("https://github.com/%s/issues/%d", defaultGHRepo, issueNum)

		// 7. Persist cross-ref. Today this overloads notion_id; proper
		//    paperclip_issue_id / github_issue_number columns require a Django
		//    migration in lightwave-platform — see plan §4.4 / LIGA-787.
		legacyRef := fmt.Sprintf("gh-%d", issueNum)
		if _, err := db.UpdateTaskNotionID(ctx, pool, task.ID, legacyRef); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("store github cross-ref: %v", err))
		}
	}

	return printTaskCreateResult(task, result)
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

// collectDocuments resolves --prd, --plan, and --doc into a list of {key, body} pairs.
// Reads each file once and validates paths up front so the atomic fan-out
// doesn't fail halfway through.
func collectDocuments() ([]docInput, error) {
	var out []docInput

	if taskCreatePRD != "" {
		body, err := os.ReadFile(taskCreatePRD)
		if err != nil {
			return nil, fmt.Errorf("read --prd %s: %w", taskCreatePRD, err)
		}
		out = append(out, docInput{key: "prd", path: taskCreatePRD, body: body})
	}
	if taskCreatePlan != "" {
		body, err := os.ReadFile(taskCreatePlan)
		if err != nil {
			return nil, fmt.Errorf("read --plan %s: %w", taskCreatePlan, err)
		}
		out = append(out, docInput{key: "plan", path: taskCreatePlan, body: body})
	}
	for _, kv := range taskCreateDocs {
		key, path, ok := strings.Cut(kv, "=")
		if !ok || key == "" || path == "" {
			return nil, fmt.Errorf("--doc must be key=path, got %q", kv)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read --doc %s=%s: %w", key, path, err)
		}
		out = append(out, docInput{key: key, path: path, body: body})
	}
	return out, nil
}

// collectAttachments validates that every --attach path exists and is readable.
func collectAttachments() ([]string, error) {
	for _, p := range taskCreateAttach {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("--attach %s: %w", p, err)
		}
	}
	return taskCreateAttach, nil
}

// findAgentByName resolves a kebab/lowercase/spaced name to an agent.
func findAgentByName(agents []paperclip.Agent, name string) *paperclip.Agent {
	target := normalizeAgentName(name)
	for i := range agents {
		if normalizeAgentName(agents[i].Name) == target {
			return &agents[i]
		}
	}
	return nil
}

// normalizePaperclipPriority maps createOS priority strings (p1_urgent, p2_high...)
// to Paperclip's vocabulary (low|medium|high|critical). Pass-through for
// already-Paperclip-native values.
func normalizePaperclipPriority(p string) string {
	switch strings.ToLower(p) {
	case "p1_urgent", "critical", "urgent":
		return "critical"
	case "p2_high", "high":
		return "high"
	case "p3_medium", "medium", "":
		return "medium"
	case "p4_low", "low":
		return "low"
	default:
		return p
	}
}

// resolveOrCreateLabel looks up a label by name; creates it on miss.
func resolveOrCreateLabel(ctx context.Context, pc *paperclip.Client, companyID, name string) (string, string) {
	label, err := pc.FindLabelByName(ctx, companyID, name)
	if err == nil {
		return label.ID, ""
	}
	created, err := pc.CreateLabel(ctx, companyID, name, "")
	if err != nil {
		return "", fmt.Sprintf("resolve label %q: %v", name, err)
	}
	return created.ID, ""
}

// paperclipIssueURL builds a viewable URL for a Paperclip identifier.
func paperclipIssueURL(identifier string) string {
	if identifier == "" {
		return ""
	}
	base := "http://localhost:3100"
	// Mirror paperclip.NewClient's base URL resolution.
	if u := os.Getenv("PAPERCLIP_URL"); u != "" {
		base = strings.TrimRight(u, "/")
	}
	return fmt.Sprintf("%s/issues/%s", base, identifier)
}

func ptr[T any](v T) *T { return &v }

// printDryRun renders the resolved intent without making any mutations.
func printDryRun(body string, docs []docInput, attachments []string, agentName, companyID string) error {
	if taskCreateJSON {
		out := taskCreateResult{
			CreateosShortID: "(dry-run)",
			DryRun:          true,
		}
		for _, d := range docs {
			out.Documents = append(out.Documents, docRef{Key: d.key})
		}
		for _, a := range attachments {
			out.Attachments = append(out.Attachments, attachRef{Path: a})
		}
		out.Labels = taskCreateLabels
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Printf("%s lw task create (dry-run)\n", color.CyanString("→"))
	fmt.Printf("  Title:    %s\n", taskCreateTitle)
	fmt.Printf("  Priority: %s (paperclip: %s)\n", taskCreatePriority, normalizePaperclipPriority(taskCreatePriority))
	fmt.Printf("  Type:     %s\n", taskCreateType)
	if agentName != "" {
		fmt.Printf("  Assignee: %s (company %s)\n", agentName, companyID)
	} else {
		fmt.Printf("  Assignee: %s — Paperclip leg will be skipped\n", color.YellowString("(none)"))
	}
	if len(taskCreateLabels) > 0 {
		fmt.Printf("  Labels:   %s\n", strings.Join(taskCreateLabels, ", "))
	}
	if taskCreateParent != "" {
		fmt.Printf("  Parent:   %s\n", taskCreateParent)
	}
	if taskCreateProject != "" {
		fmt.Printf("  Project:  %s\n", taskCreateProject)
	}
	if len(taskCreateBlocks) > 0 {
		fmt.Printf("  Blocks:   %s\n", strings.Join(taskCreateBlocks, ", "))
	}
	if len(taskCreateBlockedBy) > 0 {
		fmt.Printf("  BlockedBy: %s\n", strings.Join(taskCreateBlockedBy, ", "))
	}
	if len(docs) > 0 {
		fmt.Println("  Documents:")
		for _, d := range docs {
			fmt.Printf("    %s ← %s (%d bytes)\n", d.key, d.path, len(d.body))
		}
	}
	if len(attachments) > 0 {
		fmt.Println("  Attachments:")
		for _, a := range attachments {
			fmt.Printf("    %s\n", filepath.Base(a))
		}
	}
	if len(body) > 0 {
		preview := body
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		fmt.Printf("  Body:     %d bytes\n            %s\n", len(body), strings.ReplaceAll(preview, "\n", " "))
	}
	fmt.Printf("\n%s no mutations performed.\n", color.YellowString("⚠"))
	return nil
}

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
