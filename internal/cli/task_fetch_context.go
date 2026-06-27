package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/mddocs"
	"github.com/spf13/cobra"
)

// `lw task fetch-context <id>` — US-003 (EB-001 v_core resident orchestrator).
//
// Bundles a markdown Task + its parent User Story + Epic Brief + Sprint, plus
// any frontmatter-linked SAD/NFRs/DDD/PRD/Naming refs, into a single markdown
// blob suitable for handoff to a sealed sub-session spawned by US-004's
// `lw agent spawn`.
//
// Reads ONLY from `lightwave-media/docs/<domain>/<kind>/<ID>-*.md`. Never
// touches the Postgres `lw task` surface — that is the legacy Phase A
// substrate; markdown is canonical per the documentation-workflow.md §7.

func init() {
	RegisterHandler("task.fetch-context", taskFetchContextHandler)
}

// AttachOrphanTaskCommands attaches handler-only task commands (ones with no
// schema entry yet in lightwave-core/.../commands.yaml) to the dispatched
// `task` subtree. Called from rootCmd Execute() after BuildDispatched runs.
//
// This is the bridge for shipping commands in lightwave-cli ahead of the
// lightwave-core schema update. When the schema entry lands, the dispatcher
// will already have attached the command — the guard below makes this a
// no-op in that case. Once every command listed here has a schema entry,
// this function can be deleted.
func AttachOrphanTaskCommands(root *cobra.Command) {
	taskCmd := findSubcommand(root, "task")
	if taskCmd == nil {
		return
	}

	if findSubcommand(taskCmd, "fetch-context") == nil {
		taskCmd.AddCommand(newTaskFetchContextCobraCmd())
	}
	// Extras registered by task_md.go's init (new/edit/close/index).
	for _, c := range orphanTaskCommandsExtra {
		if findSubcommand(taskCmd, c.Name()) == nil {
			taskCmd.AddCommand(c)
		}
	}
}

func findSubcommand(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}

	return nil
}

func newTaskFetchContextCobraCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "fetch-context <task-id>",
		Short: "Assemble a markdown context bundle for a Task (US-003)",
		Long: `Read a markdown Task by ID, walk its frontmatter linkage (parent_story,
parent_epic, assigned_sprint, refs_sad/nfrs/ddd/prd/naming), and emit a
single markdown blob to stdout.

Used by lw agent spawn (US-004) to seed sealed sub-sessions with the full
context a persona needs to execute the task. Reads from
lightwave-media/docs/<domain>/{tasks,user-stories,epic-briefs,sprints}/.

Examples:
  lw task fetch-context T-0001
  lw task fetch-context T-0001 --domain software
  lw task fetch-context T-0001 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := map[string]any{}

			if cmd.Flags().Changed("domain") {
				v, _ := cmd.Flags().GetString("domain")
				flags["domain"] = v
			}

			if cmd.Flags().Changed("json") {
				v, _ := cmd.Flags().GetBool("json")
				flags["json"] = v
			}

			return taskFetchContextHandler(cmd.Context(), args, flags)
		},
	}
	c.Flags().String("domain", "", "Restrict search to a single docs domain (software, cinematography, …); default searches all")
	c.Flags().Bool("json", false, "Emit a JSON envelope (path index + body) instead of rendered markdown")

	return c
}

func taskFetchContextHandler(_ context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("task id required (e.g. T-0001)")
	}

	id := args[0]

	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	root := cfg.Paths.LightwaveRoot
	if root == "" {
		return errors.New("paths.lightwave_root not configured")
	}

	domain := flagStr(flags, "domain")

	seed, err := mddocs.FindByID(root, domain, id)
	if err != nil {
		if errors.Is(err, mddocs.ErrNotFound) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		return err
	}

	if seed.Frontmatter.Type != "" && seed.Frontmatter.Type != string(mddocs.KindTask) {
		return fmt.Errorf("artefact %s has type %q, expected task — fetch-context only seeds from tasks",
			id, seed.Frontmatter.Type)
	}

	bundle, err := mddocs.BuildBundle(root, seed)
	if err != nil {
		return err
	}

	if asJSON(flags) {
		return emitJSON(buildBundleJSON(bundle))
	}

	fmt.Print(bundle.Render())

	return nil
}

// bundleJSON is the stable JSON envelope emitted by --json. Agents and the
// future `lw agent spawn` consumer should template against THIS shape, not
// the prose markdown — markdown is for human inspection.
type bundleJSON struct {
	Seed     map[string]any   `json:"seed"`
	Story    map[string]any   `json:"story,omitempty"`
	Epic     map[string]any   `json:"epic,omitempty"`
	Sprint   map[string]any   `json:"sprint,omitempty"`
	Markdown string           `json:"markdown"`
	Refs     []map[string]any `json:"refs,omitempty"`
	Warnings []string         `json:"warnings,omitempty"`
}

func buildBundleJSON(b *mddocs.Bundle) bundleJSON {
	out := bundleJSON{
		Warnings: b.Warnings,
		Markdown: b.Render(),
		Seed:     artefactJSON(b.Seed),
	}
	if b.Story != nil {
		out.Story = artefactJSON(b.Story)
	}

	if b.Epic != nil {
		out.Epic = artefactJSON(b.Epic)
	}

	if b.Sprint != nil {
		out.Sprint = artefactJSON(b.Sprint)
	}

	for _, r := range b.Refs {
		out.Refs = append(out.Refs, map[string]any{
			"kind": r.Kind,
			"path": r.Path,
			"body": r.Body,
		})
	}

	return out
}

func artefactJSON(a *mddocs.Artefact) map[string]any {
	return map[string]any{
		"path":        a.Path,
		"frontmatter": a.Frontmatter,
		"body":        a.Body,
	}
}
