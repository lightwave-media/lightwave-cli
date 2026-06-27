package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/agent"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/mddocs"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

// `lw agent` — US-004 (EB-001 v_core resident orchestrator).
//
// Top-level command for the sealed-sub-session lifecycle v_core uses to
// dispatch tasks. Spawn creates a git worktree, loads a persona's system
// prompt, layers `lw task fetch-context` on top, and shells the chosen
// agent binary (claude / pi) in the worktree as a background process.
// list / status / stop / provision round out the lifecycle.
//
// This is a new top-level domain not yet declared in lightwave-core's
// commands.yaml — wired hardcoded in root.go alongside auditCmd/cdnCmd/
// etc. Schema entry lands in a sibling lightwave-core PR; until then the
// dispatcher silently leaves the `agent` namespace to this file.

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Sealed sub-session lifecycle: spawn / list / status / stop",
	Long: `Manages the sealed sub-sessions v_core dispatches per Task.

Each spawn creates a fresh git worktree, loads the persona's system
prompt from ~/.brain/cortex/agents/createOS-domains/software/ (or
$LW_PERSONA_DIR), appends the markdown bundle from
` + "`lw task fetch-context`" + `, and shells the agent binary (claude / pi)
as a background process with stdout+stderr captured to a log file.

State is persisted under ~/.lightwave/agents/<id>.json so v_core can
poll status across CLI invocations.`,
}

var (
	agentSpawnPersona     string
	agentSpawnTask        string
	agentSpawnRepo        string
	agentSpawnShell       string
	agentSpawnShellArgs   []string
	agentSpawnPromptStdin bool
	agentSpawnDryRun      bool
	agentSpawnJSON        bool
)

var agentSpawnCmd = &cobra.Command{
	Use:          "spawn",
	Short:        "Spawn a sealed sub-session in a fresh worktree (US-004)",
	SilenceUsage: true,
	Long: `Create a fresh git worktree off the configured --repo, load the
persona's system prompt, append the markdown context bundle produced by
` + "`lw task fetch-context`" + `, and start the agent shell (claude / pi)
as a detached background process.

Required flags:
  --persona <name>   one of: platform-engineer, frontend-engineer,
                     infrastructure-engineer, qa-engineer, compliance,
                     triager, research-analyst, brain
  --task <T-NNNN>    the markdown Task whose context seeds the session

Optional flags:
  --repo <path>      git repo for the worktree (default: cwd)
  --shell <bin>      agent binary (default: claude; accepts pi or any
                     headless LLM CLI that takes a prompt on argv)
  --shell-arg <flag> repeatable; argv inserted before the prompt
                     (e.g. --shell-arg=-p for Claude Code print-mode)
  --prompt-stdin     pipe the prompt via stdin instead of as an argv
                     (some shells require this for long inputs)
  --dry-run          print the resolved plan + worktree path; do not act
  --json             machine-readable success envelope

Examples:
  lw agent spawn --persona platform-engineer --task T-0001 \
                 --repo ~/dev/lightwave-sys --shell-arg=-p
  lw agent spawn --persona qa-engineer --task T-0042 --dry-run`,
	RunE: runAgentSpawn,
}

var agentListCmd = &cobra.Command{
	Use:          "list",
	Short:        "List spawned agents (newest first)",
	SilenceUsage: true,
	RunE:         runAgentList,
}

var agentStatusCmd = &cobra.Command{
	Use:          "status <agent-id>",
	Short:        "Show one agent's status (UUID or short prefix accepted)",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE:         runAgentStatus,
}

var (
	agentStopForce bool
	agentStopYes   bool
)

var agentStopCmd = &cobra.Command{
	Use:          "stop <agent-id>",
	Short:        "Stop an agent and remove its worktree",
	SilenceUsage: true,
	Long: `Send SIGTERM to the agent process, wait up to 3s, then remove the
worktree and delete the branch. Use --force to send SIGKILL.

Idempotent — already-exited agents are reaped (worktree cleanup only).`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentStop,
}

var (
	agentProvisionRoles []string
)

var agentProvisionCmd = &cobra.Command{
	Use:          "provision <agent-name>",
	Short:        "Provision an agent user record + token (Phase B; STUB)",
	SilenceUsage: true,
	Long: `Per EB-005 (Phase B storage), every v_<role> agent gets a first-class
user record + API token + RBAC role assignments in lightwave-platform.

In Phase A this is a STUB — prints what WOULD happen so v_core's
provisioning code path can be exercised end-to-end. The real
implementation lands when EB-005 ships.`,
	Args: cobra.ExactArgs(1),
	RunE: runAgentProvisionStub,
}

func init() {
	agentSpawnCmd.Flags().StringVar(&agentSpawnPersona, "persona", "", "Persona name (required)")
	agentSpawnCmd.Flags().StringVar(&agentSpawnTask, "task", "", "Task ID, e.g. T-0001 (required)")
	agentSpawnCmd.Flags().StringVar(&agentSpawnRepo, "repo", "", "Git repo for the worktree (default: cwd)")
	agentSpawnCmd.Flags().StringVar(&agentSpawnShell, "shell", "claude", "Agent binary to invoke")
	agentSpawnCmd.Flags().StringArrayVar(&agentSpawnShellArgs, "shell-arg", nil, "argv inserted before the prompt (repeatable)")
	agentSpawnCmd.Flags().BoolVar(&agentSpawnPromptStdin, "prompt-stdin", false, "Pipe the prompt via stdin instead of argv")
	agentSpawnCmd.Flags().BoolVar(&agentSpawnDryRun, "dry-run", false, "Print plan, do not act")
	agentSpawnCmd.Flags().BoolVar(&agentSpawnJSON, "json", false, "Emit JSON envelope")
	_ = agentSpawnCmd.MarkFlagRequired("persona")
	_ = agentSpawnCmd.MarkFlagRequired("task")

	agentStopCmd.Flags().BoolVar(&agentStopForce, "force", false, "Send SIGKILL instead of SIGTERM")
	agentStopCmd.Flags().BoolVar(&agentStopYes, "yes", false, "Skip confirmation prompt (CI/agent use)")

	agentProvisionCmd.Flags().StringSliceVar(&agentProvisionRoles, "roles", nil, "RBAC roles to assign (repeatable, comma-separated)")

	agentCmd.AddCommand(agentSpawnCmd)
	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentStatusCmd)
	agentCmd.AddCommand(agentStopCmd)
	agentCmd.AddCommand(agentProvisionCmd)
}

func runAgentSpawn(cmd *cobra.Command, _ []string) error {
	if agentSpawnRepo == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		agentSpawnRepo = cwd
	}

	absRepo, err := filepath.Abs(agentSpawnRepo)
	if err != nil {
		return fmt.Errorf("resolve --repo: %w", err)
	}

	agentSpawnRepo = absRepo

	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	// 1. Load persona prompt.
	personaBody, personaPath, err := agent.LoadPersonaPrompt(agentSpawnPersona)
	if err != nil {
		var pnf *agent.PersonaNotFoundError
		if errors.As(err, &pnf) {
			fmt.Fprintf(os.Stderr,
				"persona %q not found.\nSearched: %s\n\nLightWave canonical personas (per v_core.yaml): platform-engineer, frontend-engineer, infrastructure-engineer, qa-engineer, compliance, triager, research-analyst, brain.\nMissing personas need stub YAML files at ~/.brain/cortex/agents/createOS-domains/software/<name>.yaml (see EB-001 §10 Q1, US-002).\n",
				pnf.Name, strings.Join(pnf.SearchedIn, "\n          "))
			os.Exit(1)
		}

		return err
	}

	// 2. Bundle context from the task's markdown chain.
	seed, err := mddocs.FindByID(cfg.Paths.LightwaveRoot, "", agentSpawnTask)
	if err != nil {
		return fmt.Errorf("fetch task %s: %w", agentSpawnTask, err)
	}

	bundle, err := mddocs.BuildBundle(cfg.Paths.LightwaveRoot, seed)
	if err != nil {
		return fmt.Errorf("build context bundle: %w", err)
	}

	prompt := assemblePrompt(personaBody, personaPath, bundle.Render())

	id := agent.NewID()

	if agentSpawnDryRun {
		short := id[:8]

		fmt.Println(color.CyanString("DRY RUN — no worktree created, no process spawned"))
		fmt.Printf("Agent id (would be): %s\n", short)
		fmt.Printf("Persona:             %s (from %s)\n", agentSpawnPersona, personaPath)
		fmt.Printf("Task:                %s — %s\n", seed.Frontmatter.ID, seed.Frontmatter.Title)
		fmt.Printf("Repo:                %s\n", agentSpawnRepo)
		fmt.Printf("Worktree (planned):  %s/.worktrees/agent-%s\n", agentSpawnRepo, short)
		fmt.Printf("Branch (planned):    feature/agent-%s-%s-%s\n",
			strings.ToLower(agentSpawnTask), slugify(agentSpawnPersona), short)
		fmt.Printf("Shell:               %s %s\n", agentSpawnShell, strings.Join(agentSpawnShellArgs, " "))
		fmt.Printf("Prompt size:         %d bytes\n", len(prompt))

		if len(bundle.Warnings) > 0 {
			fmt.Println(color.YellowString("Bundle warnings:"))

			for _, w := range bundle.Warnings {
				fmt.Printf("  - %s\n", w)
			}
		}

		return nil
	}

	a, err := agent.Spawn(agent.SpawnOptions{
		ID:          id,
		TaskID:      agentSpawnTask,
		Persona:     agentSpawnPersona,
		Repo:        agentSpawnRepo,
		Shell:       agentSpawnShell,
		ShellArgs:   agentSpawnShellArgs,
		Prompt:      prompt,
		PromptStdin: agentSpawnPromptStdin,
	})
	if err != nil {
		return err
	}

	if agentSpawnJSON {
		return emitJSON(map[string]any{
			"id":           a.ID,
			"short_id":     a.ShortID(),
			"task_id":      a.TaskID,
			"persona":      a.Persona,
			"repo":         a.Repo,
			"worktree":     a.Worktree,
			"branch":       a.Branch,
			"shell":        a.Shell,
			"pid":          a.PID,
			"log_path":     a.LogPath,
			"context_path": a.ContextPath,
			"status":       string(a.Status),
			"started_at":   a.StartedAt,
		})
	}

	fmt.Printf("Spawned %s (%s) for %s\n",
		color.CyanString(a.ShortID()),
		color.YellowString(a.Persona),
		color.YellowString(a.TaskID),
	)
	fmt.Printf("  worktree: %s\n", a.Worktree)
	fmt.Printf("  branch:   %s\n", a.Branch)
	fmt.Printf("  pid:      %d\n", a.PID)
	fmt.Printf("  log:      %s\n", a.LogPath)

	return nil
}

// assemblePrompt concatenates the persona YAML and the task context
// bundle into a single prompt body, separated by a fenced block so the
// downstream agent can parse them.
func assemblePrompt(personaBody, personaPath, bundle string) string {
	var b strings.Builder
	b.WriteString("# Sealed sub-session prompt\n\n")
	b.WriteString("You are a LightWave engineering persona spawned by v_core to execute one Task. Honour the persona spec below and the constraints in the bundled context. Inner-ring tool surface is enforced by the harness, not by this prompt.\n\n")
	fmt.Fprintf(&b, "## Persona — sourced from `%s`\n\n```yaml\n", personaPath)
	b.WriteString(strings.TrimRight(personaBody, "\n"))
	b.WriteString("\n```\n\n")
	b.WriteString("## Task context\n\n")
	b.WriteString(bundle)

	return b.String()
}

func runAgentList(_ *cobra.Command, _ []string) error {
	agents, err := agent.List()
	if err != nil {
		return err
	}
	// Refresh status for any running agents before rendering.
	for _, a := range agents {
		_ = agent.RefreshStatus(a)
	}

	if len(agents) == 0 {
		fmt.Println(color.YellowString("No agents spawned yet."))
		return nil
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ID", "Status", "Persona", "Task", "PID", "Started", "Worktree"})
	table.SetBorder(false)

	for _, a := range agents {
		started := a.StartedAt.Local().Format("2006-01-02 15:04")
		table.Append([]string{
			a.ShortID(),
			string(a.Status),
			a.Persona,
			a.TaskID,
			strconv.Itoa(a.PID),
			started,
			a.Worktree,
		})
	}

	table.Render()

	return nil
}

func runAgentStatus(_ *cobra.Command, args []string) error {
	a, err := agent.Load(args[0])
	if err != nil {
		return err
	}

	if err := agent.RefreshStatus(a); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("%s %s\n", color.CyanString("Agent:"), color.YellowString(a.ID))
	fmt.Printf("%s %s\n", color.CyanString("Status:"), colorAgentStatus(a.Status))
	fmt.Printf("%s %s\n", color.CyanString("Persona:"), a.Persona)
	fmt.Printf("%s %s\n", color.CyanString("Task:"), a.TaskID)
	fmt.Printf("%s %d\n", color.CyanString("PID:"), a.PID)
	fmt.Printf("%s %s\n", color.CyanString("Repo:"), a.Repo)
	fmt.Printf("%s %s\n", color.CyanString("Worktree:"), a.Worktree)
	fmt.Printf("%s %s\n", color.CyanString("Branch:"), a.Branch)
	fmt.Printf("%s %s\n", color.CyanString("Shell:"), a.Shell)
	fmt.Printf("%s %s\n", color.CyanString("Log:"), a.LogPath)
	fmt.Printf("%s %s\n", color.CyanString("Context:"), a.ContextPath)
	fmt.Printf("%s %s\n", color.CyanString("Started:"), a.StartedAt.Local().Format("2006-01-02 15:04:05"))

	if !a.ExitedAt.IsZero() {
		fmt.Printf("%s %s\n", color.CyanString("Exited:"), a.ExitedAt.Local().Format("2006-01-02 15:04:05"))
	}

	if a.Error != "" {
		fmt.Printf("%s %s\n", color.RedString("Error:"), a.Error)
	}

	return nil
}

func runAgentStop(_ *cobra.Command, args []string) error {
	a, err := agent.Load(args[0])
	if err != nil {
		return err
	}

	_ = agent.RefreshStatus(a)

	if !agentStopYes && a.Status == agent.StatusRunning {
		fmt.Printf("Stop agent %s (%s, pid %d, task %s)? [y/N] ",
			color.CyanString(a.ShortID()), a.Persona, a.PID, a.TaskID)

		var answer string

		_, _ = fmt.Scanln(&answer)
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	if err := agent.Stop(a, agentStopForce); err != nil {
		return err
	}

	fmt.Printf("Stopped %s — worktree removed.\n", color.CyanString(a.ShortID()))

	return nil
}

func runAgentProvisionStub(_ *cobra.Command, args []string) error {
	name := args[0]

	roles := agentProvisionRoles
	if len(roles) == 0 {
		roles = []string{"(none — pass --roles)"}
	}

	fmt.Println(color.YellowString("STUB — Phase A. Would atomically:"))
	fmt.Printf("  1. Create user record %s in lightwave-platform\n", color.CyanString(name))
	fmt.Printf("  2. Mint API token, store in SSM at /lightwave/agents/%s/token\n", name)
	fmt.Printf("  3. Assign RBAC roles: %s\n", strings.Join(roles, ", "))
	fmt.Printf("  4. Write DelegateAgentConfig entry to lightwave-sys\n")
	fmt.Println()
	fmt.Println("Implementation lands with EB-005 (Phase B storage). See:")
	fmt.Println("  lightwave-media/docs/software/epic-briefs/EB-001-v-core-resident-orchestrator.md §3.2")

	return nil
}

func colorAgentStatus(s agent.Status) string {
	switch s {
	case agent.StatusRunning:
		return color.GreenString(string(s))
	case agent.StatusExited:
		return color.HiBlackString(string(s))
	case agent.StatusError:
		return color.RedString(string(s))
	}

	return string(s)
}
