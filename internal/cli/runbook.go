package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/runbook"
	"github.com/spf13/cobra"
)

var (
	runbookSlug        string
	runbookAgent       string
	runbookTask        string
	runbookRepo        string
	runbookBranch      string
	runbookSession     string
	runbookInstance    string
	runbookRequire     string
	runbookCwd         string
	runbookStep        string
	runbookSignoffTier string
	runbookReason      string
	runbookDryRun      bool
)

var runbookCmd = &cobra.Command{
	Use:   "runbook",
	Short: "Apply a published runbook in a task worktree, not on main",
	Long: `Agent runbook kernel (ADR-0039). Pick a published edition from
lightwave-core/src/runbooks, dry-run or apply in the task worktree, pause
on high-blast-radius Command/Template steps, and leave instance evidence
under .tasks/{task}/runbooks/{instance}/.

Dead ends: no catalog match (file a tool-gap, do not improvise); checkout
is main; edition hash mismatch; check failure.`,
}

var runbookStartCmd = &cobra.Command{
	Use:          "start",
	Short:        "Create a worktree instance print for a published runbook",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runRunbookStart,
}

var runbookStatusCmd = &cobra.Command{
	Use:          "status",
	Short:        "Read instance state; --require completed gates done",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runRunbookStatus,
}

var runbookApplyCmd = &cobra.Command{
	Use:          "apply",
	Short:        "Apply Check steps; pause on high-blast Command/Template",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runRunbookApply,
}

var runbookStepCompleteCmd = &cobra.Command{
	Use:          "step-complete",
	Short:        "Record operator sign-off (or deny/defer) on a waiting step",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runRunbookStepComplete,
}

var runbookCancelCmd = &cobra.Command{
	Use:          "cancel",
	Short:        "Cancel a runbook instance",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runRunbookCancel,
}

func init() {
	runbookStartCmd.Flags().StringVar(&runbookSlug, "slug", "", "Published runbook slug")
	runbookStartCmd.Flags().StringVar(&runbookAgent, "agent", "", "Persona id (v_*)")
	runbookStartCmd.Flags().StringVar(&runbookTask, "task", "", "Task / GitHub issue id")
	runbookStartCmd.Flags().StringVar(&runbookRepo, "repo", "", "Repo slug")
	runbookStartCmd.Flags().StringVar(&runbookBranch, "branch", "", "Expected branch")
	runbookStartCmd.Flags().StringVar(&runbookSession, "session", "", "Session id")
	runbookStartCmd.Flags().BoolVar(&runbookDryRun, "dry-run", false, "Record plan; do not execute checks")

	runbookStatusCmd.Flags().StringVar(&runbookTask, "task", "", "Task id")
	runbookStatusCmd.Flags().StringVar(&runbookInstance, "instance", "", "Instance id")
	runbookStatusCmd.Flags().StringVar(&runbookRequire, "require", "", "Required status (e.g. completed)")

	runbookApplyCmd.Flags().StringVar(&runbookTask, "task", "", "Task id")
	runbookApplyCmd.Flags().StringVar(&runbookInstance, "instance", "", "Instance id")
	runbookApplyCmd.Flags().StringVar(&runbookCwd, "cwd", "", "Worktree path (default: cwd)")

	runbookStepCompleteCmd.Flags().StringVar(&runbookTask, "task", "", "Task id")
	runbookStepCompleteCmd.Flags().StringVar(&runbookInstance, "instance", "", "Instance id")
	runbookStepCompleteCmd.Flags().StringVar(&runbookStep, "step", "", "Step id")
	runbookStepCompleteCmd.Flags().StringVar(&runbookSignoffTier, "signoff-tier", "", "operator | deny | defer")

	runbookCancelCmd.Flags().StringVar(&runbookTask, "task", "", "Task id")
	runbookCancelCmd.Flags().StringVar(&runbookInstance, "instance", "", "Instance id")
	runbookCancelCmd.Flags().StringVar(&runbookReason, "reason", "", "Cancel reason")

	runbookCmd.AddCommand(runbookStartCmd)
	runbookCmd.AddCommand(runbookStatusCmd)
	runbookCmd.AddCommand(runbookApplyCmd)
	runbookCmd.AddCommand(runbookStepCompleteCmd)
	runbookCmd.AddCommand(runbookCancelCmd)
}

func runRunbookStart(cmd *cobra.Command, _ []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	inst, err := runbook.Start(&runbook.StartOpts{
		CoreRoot: coreRepoPath(),
		Cwd:      cwd,
		Slug:     runbookSlug,
		Agent:    runbookAgent,
		Task:     runbookTask,
		Repo:     runbookRepo,
		Branch:   runbookBranch,
		Session:  runbookSession,
		DryRun:   runbookDryRun,
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(),
		"runbook instance %s slug=%s status=%s path=%s\n",
		inst.InstanceID, inst.RunbookSlug, inst.Status,
		runbook.Path(cwd, inst.TaskID, inst.InstanceID))

	return err
}

func runRunbookStatus(cmd *cobra.Command, _ []string) error {
	cwd, err := workCwd()
	if err != nil {
		return err
	}

	inst, err := runbook.Status(&runbook.ApplyOpts{
		Cwd:        cwd,
		Task:       runbookTask,
		InstanceID: runbookInstance,
		Require:    runbookRequire,
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(),
		"runbook instance %s slug=%s status=%s step=%s\n",
		inst.InstanceID, inst.RunbookSlug, inst.Status, inst.CurrentStepID)

	return err
}

func runRunbookApply(cmd *cobra.Command, _ []string) error {
	cwd, err := workCwd()
	if err != nil {
		return err
	}

	inst, err := runbook.Apply(&runbook.ApplyOpts{
		CoreRoot:   coreRepoPath(),
		Cwd:        cwd,
		Task:       runbookTask,
		InstanceID: runbookInstance,
	})
	if err != nil {
		return err
	}

	msg := "applied"
	if inst.Status == runbook.StatusWaitingApproval {
		msg = "paused for operator approval"
	}

	evidence := filepath.Join(runbook.Dir(cwd, inst.TaskID, inst.InstanceID), "evidence.md")

	_, err = fmt.Fprintf(cmd.OutOrStdout(),
		"runbook %s instance %s status=%s step=%s evidence=%s\n",
		msg, inst.InstanceID, inst.Status, inst.CurrentStepID, evidence)
	if err != nil {
		return err
	}

	maybeCommentIssue(inst, evidence)

	return nil
}

func runRunbookStepComplete(cmd *cobra.Command, _ []string) error {
	cwd, err := workCwd()
	if err != nil {
		return err
	}

	inst, err := runbook.StepComplete(&runbook.ApplyOpts{
		Cwd:         cwd,
		Task:        runbookTask,
		InstanceID:  runbookInstance,
		StepID:      runbookStep,
		SignoffTier: runbookSignoffTier,
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(),
		"runbook step %s signed instance %s status=%s\n",
		runbookStep, inst.InstanceID, inst.Status)

	return err
}

func runRunbookCancel(cmd *cobra.Command, _ []string) error {
	cwd, err := workCwd()
	if err != nil {
		return err
	}

	inst, err := runbook.Cancel(&runbook.ApplyOpts{
		Cwd:        cwd,
		Task:       runbookTask,
		InstanceID: runbookInstance,
		Reason:     runbookReason,
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(),
		"runbook instance %s cancelled\n", inst.InstanceID)

	return err
}

func workCwd() (string, error) {
	if runbookCwd != "" {
		return runbookCwd, nil
	}

	return os.Getwd()
}

func coreRepoPath() string {
	return filepath.Join(lightwaveRoot(), "lightwave-core")
}

// maybeCommentIssue posts evidence.md onto the GitHub issue named by
// instance.TaskID. Best-effort: missing gh or a bad repo must not fail apply.
func maybeCommentIssue(inst *runbook.Instance, evidencePath string) {
	if inst == nil || inst.RepoSlug == "" || inst.TaskID == "" {
		return
	}

	body, err := os.ReadFile(evidencePath)
	if err != nil {
		return
	}

	repo := inst.RepoSlug
	if !strings.Contains(repo, "/") {
		repo = "lightwave-media/" + repo
	}

	cmd := exec.CommandContext(context.Background(), "gh", "issue", "comment", inst.TaskID, "--repo", repo, "--body", string(body))
	_ = cmd.Run()
}
