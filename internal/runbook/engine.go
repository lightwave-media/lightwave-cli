package runbook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
	"github.com/lightwave-media/lightwave-cli/internal/git"
)

// StartOpts is the input to Start.
type StartOpts struct {
	CoreRoot   string
	Cwd        string
	Slug       string
	Agent      string
	Task       string
	Repo       string
	Branch     string
	Session    string
	InstanceID string
	DryRun     bool
}

// ApplyOpts is the input to Apply / Status / Cancel / StepComplete.
type ApplyOpts struct {
	CoreRoot    string
	Cwd         string
	Task        string
	InstanceID  string
	StepID      string
	SignoffTier string
	Reason      string
	Require     string
}

// Start looks up a published edition, refuses main, and writes the instance.
func Start(opts *StartOpts) (*Instance, error) {
	if err := requireFields(opts); err != nil {
		return nil, err
	}

	if err := refuseMainAndRequireWorktree(opts.Cwd); err != nil {
		return nil, err
	}

	index, err := LoadIndex(opts.CoreRoot)
	if err != nil {
		return nil, err
	}

	entry, err := Lookup(index, opts.Slug)
	if err != nil {
		return nil, err
	}

	edition, err := LoadEdition(opts.CoreRoot, entry)
	if err != nil {
		return nil, err
	}

	branch, err := currentBranch(opts.Cwd)
	if err != nil {
		return nil, err
	}

	id := opts.InstanceID
	if id == "" {
		id = uuid.NewString()
	}

	inst := &Instance{
		InstanceID:   id,
		RunbookSlug:  opts.Slug,
		AgentID:      opts.Agent,
		TaskID:       opts.Task,
		SessionID:    opts.Session,
		RepoSlug:     opts.Repo,
		WorktreePath: opts.Cwd,
		Branch:       firstNonEmpty(opts.Branch, branch),
		Status:       StatusPending,
		DryRun:       opts.DryRun,
		EditionHash:  edition.Hash,
		CreatedAt:    nowUTC(),
		Steps:        pendingSteps(edition.Steps),
	}

	if err := Save(opts.Cwd, inst); err != nil {
		return nil, err
	}

	if err := writeEvidence(opts.Cwd, inst); err != nil {
		return nil, err
	}

	return inst, nil
}

// Status loads the instance. --require completed fails if not completed.
func Status(opts *ApplyOpts) (*Instance, error) {
	id, err := ResolveInstanceID(opts.Cwd, opts.Task, opts.InstanceID)
	if err != nil {
		return nil, err
	}

	inst, err := Load(opts.Cwd, opts.Task, id)
	if err != nil {
		return nil, err
	}

	if opts.Require != "" && inst.Status != opts.Require {
		return inst, fmt.Errorf("instance status %s, required %s", inst.Status, opts.Require)
	}

	return inst, nil
}

// Apply runs the published edition's steps in order.
//
// Check steps run immediately. Command and Template steps are high-blast: the
// run pauses at the first one without an operator signoff tier and returns
// WaitingApproval. Once signed off (StepComplete), a subsequent Apply executes
// them for real — Command runs its body, Template renders its blueprint into
// the worktree. DryRun executes nothing.
//
// Sign-off is a gate on *authorisation*, not on execution: a signed step that
// does not do its work, but reports completed, is the failure this design
// exists to prevent.
func Apply(opts *ApplyOpts) (*Instance, error) {
	if err := refuseMainAndRequireWorktree(opts.Cwd); err != nil {
		return nil, err
	}

	id, err := ResolveInstanceID(opts.Cwd, opts.Task, opts.InstanceID)
	if err != nil {
		return nil, err
	}

	inst, err := Load(opts.Cwd, opts.Task, id)
	if err != nil {
		return nil, err
	}

	if inst.Status == StatusCancelled || inst.Status == StatusFailed || inst.Status == StatusCompleted {
		return inst, nil
	}

	index, err := LoadIndex(opts.CoreRoot)
	if err != nil {
		return nil, err
	}

	entry, err := Lookup(index, inst.RunbookSlug)
	if err != nil {
		return nil, err
	}

	edition, err := LoadEdition(opts.CoreRoot, entry)
	if err != nil {
		return nil, err
	}

	if edition.Hash != inst.EditionHash {
		inst.Status = StatusFailed
		_ = Save(opts.Cwd, inst)

		return inst, fmt.Errorf("%w: instance %s published %s", ErrEditionMismatch, inst.EditionHash, edition.Hash)
	}

	byID := map[string]Step{}
	for i := range edition.Steps {
		byID[edition.Steps[i].ID] = edition.Steps[i]
	}

	inst.Status = StatusRunning

	for i := range inst.Steps {
		st := &inst.Steps[i]
		if st.Status == stepCompleted {
			continue
		}

		edStep, ok := byID[st.ID]
		if !ok {
			inst.Status = StatusFailed
			st.Status = stepFailed
			st.Output = "step missing from published edition"
			_ = Save(opts.Cwd, inst)

			return inst, ErrEditionMismatch
		}

		if edStep.HighBlast && st.SignoffTier == "" {
			st.Status = stepWaiting
			inst.Status = StatusWaitingApproval

			inst.CurrentStepID = st.ID
			if err := Save(opts.Cwd, inst); err != nil {
				return nil, err
			}

			if err := writeEvidence(opts.Cwd, inst); err != nil {
				return nil, err
			}

			return inst, nil
		}

		if inst.DryRun {
			st.Status = stepCompleted
			st.Output = "dry-run: not executed"

			continue
		}

		// HighBlast is a SIGN-OFF gate, not an execution gate. The check above
		// already refused to proceed without a signoff tier; past that point a
		// signed step must actually do its work.
		//
		// It used to short-circuit here for every HighBlast step and report
		// "operator signed off; command not executed by this kernel" — status
		// completed, nothing done. That made Command and Template steps inert
		// while reporting success, so a runbook that declares
		// `<Template path="templates/product-module" />` created no files and
		// said it had. Apply() only runs inside a worktree
		// (refuseMainAndRequireWorktree), which is what makes executing safe.
		//
		// edition.Dir is catalog-relative; edition.Path is the absolute
		// runbook.mdx, so its parent is what a Template's `path` is relative to.
		out, runErr := executeStep(opts.Cwd, filepath.Dir(edition.Path), &edStep)

		st.Output = out
		if runErr != nil {
			st.Status = stepFailed
			inst.Status = StatusFailed
			inst.CurrentStepID = st.ID
			_ = Save(opts.Cwd, inst)
			_ = writeEvidence(opts.Cwd, inst)

			return inst, fmt.Errorf("%w: %s: %w", ErrCheckFailed, st.ID, runErr)
		}

		st.Status = stepCompleted
	}

	inst.Status = StatusCompleted

	inst.CurrentStepID = ""
	if err := Save(opts.Cwd, inst); err != nil {
		return nil, err
	}

	if err := writeEvidence(opts.Cwd, inst); err != nil {
		return nil, err
	}

	return inst, nil
}

// StepComplete records operator sign-off on a waiting step.
func StepComplete(opts *ApplyOpts) (*Instance, error) {
	if opts.StepID == "" {
		return nil, errors.New("--step is required")
	}

	id, err := ResolveInstanceID(opts.Cwd, opts.Task, opts.InstanceID)
	if err != nil {
		return nil, err
	}

	inst, err := Load(opts.Cwd, opts.Task, id)
	if err != nil {
		return nil, err
	}

	found := false

	for i := range inst.Steps {
		if inst.Steps[i].ID != opts.StepID {
			continue
		}

		found = true

		tier := opts.SignoffTier
		if strings.EqualFold(tier, "deny") || strings.EqualFold(tier, "defer") {
			inst.Status = StatusWaitingApproval
			inst.Steps[i].Output = "operator " + strings.ToLower(tier)
			_ = Save(opts.Cwd, inst)

			return inst, ErrDenied
		}

		inst.Steps[i].SignoffTier = firstNonEmpty(tier, "operator")
		if inst.Steps[i].Status == stepWaiting {
			inst.Steps[i].Status = stepPending
		}

		inst.Status = StatusRunning
	}

	if !found {
		return inst, fmt.Errorf("step %s not on instance", opts.StepID)
	}

	if err := Save(opts.Cwd, inst); err != nil {
		return nil, err
	}

	return inst, nil
}

// Cancel marks the instance cancelled.
func Cancel(opts *ApplyOpts) (*Instance, error) {
	id, err := ResolveInstanceID(opts.Cwd, opts.Task, opts.InstanceID)
	if err != nil {
		return nil, err
	}

	inst, err := Load(opts.Cwd, opts.Task, id)
	if err != nil {
		return nil, err
	}

	inst.Status = StatusCancelled
	if opts.Reason != "" {
		inst.CurrentStepID = ""
		if len(inst.Steps) > 0 {
			last := &inst.Steps[len(inst.Steps)-1]
			if last.Output == "" {
				last.Output = opts.Reason
			}
		}
	}

	if err := Save(opts.Cwd, inst); err != nil {
		return nil, err
	}

	if err := writeEvidence(opts.Cwd, inst); err != nil {
		return nil, err
	}

	return inst, nil
}

func requireFields(opts *StartOpts) error {
	switch {
	case opts.Slug == "":
		return errors.New("--slug is required")
	case opts.Agent == "":
		return errors.New("--agent is required")
	case opts.Task == "":
		return errors.New("--task is required")
	case opts.Cwd == "":
		return errors.New("cwd is required")
	case opts.CoreRoot == "":
		return errors.New("lightwave-core root is required")
	default:
		return nil
	}
}

func refuseMainAndRequireWorktree(cwd string) error {
	branch, err := currentBranch(cwd)
	if err != nil {
		return err
	}

	switch strings.ToLower(branch) {
	case "main", "master", "head":
		return fmt.Errorf("%w (branch %s)", ErrOnMain, branch)
	}

	if _, err := os.Stat(filepath.Join(cwd, worktreeDot)); err != nil {
		return fmt.Errorf("%w: missing %s", ErrNotWorktree, worktreeDot)
	}

	return nil
}

func currentBranch(cwd string) (string, error) {
	g := git.NewGit(cwd)

	return g.CurrentBranch()
}

func pendingSteps(steps []Step) []StepState {
	out := make([]StepState, 0, len(steps))
	for _, s := range steps {
		out = append(out, StepState{
			ID:     s.ID,
			Kind:   s.Kind,
			Status: stepPending,
		})
	}

	return out
}

// executeStep performs one step's actual work and returns its output.
//
// A step kind that has nothing to do is not an error — some steps are prose
// with no command or path. A step that cannot do its work IS an error: it must
// never report completed, which is the failure mode this replaced.
func executeStep(cwd, runbookDir string, step *Step) (string, error) {
	switch step.Kind {
	case KindTemplate:
		return renderTemplateStep(cwd, runbookDir, step)

	case KindCheck, KindCommand:
		if step.Command == "" {
			return "", nil
		}

		return runShell(cwd, step.Command)

	default:
		return "", nil
	}
}

// renderTemplateStep renders a Template step's blueprint into the worktree
// through the linked boilerplate engine — no `boilerplate` binary, no shell.
//
// step.Path is relative to the runbook's own directory, which is how the
// published runbooks express it:
//
//	<Template id="product" path="templates/product-module" target="worktree" />
func renderTemplateStep(cwd, runbookDir string, step *Step) (string, error) {
	if step.Path == "" {
		return "", fmt.Errorf("template step %q has no path attribute", step.ID)
	}

	src := filepath.Join(runbookDir, step.Path)
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("template step %q: %w", step.ID, err)
	}

	// "worktree" (or unset) renders at the instance's working tree root. Any
	// other value is a path relative to it; absolute paths are refused so a
	// runbook cannot write outside the worktree it was applied in.
	dest := cwd

	if step.Target != "" && step.Target != "worktree" {
		if filepath.IsAbs(step.Target) {
			return "", fmt.Errorf("template step %q: absolute target %q is not allowed", step.ID, step.Target)
		}

		dest = filepath.Join(cwd, step.Target)
	}

	if err := blueprint.Render(context.Background(), &blueprint.RenderOptions{
		BlueprintPath: src,
		OutputFolder:  dest,
	}); err != nil {
		return "", fmt.Errorf("template step %q: %w", step.ID, err)
	}

	return fmt.Sprintf("rendered %s -> %s", step.Path, dest), nil
}

// runShell executes a step's command string.
//
// This is a shell invocation (#348): runbook commands are authored as shell
// one-liners in MDX, so they are not argv-decomposable without changing the
// authoring format. It is bounded by Apply() refusing to run outside a
// worktree, and Command steps additionally require an operator signoff tier
// before reaching here. Check steps do not — that asymmetry is #348's subject.
func runShell(cwd, command string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", command)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()

	return strings.TrimSpace(string(out)), err
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}

func writeEvidence(cwd string, inst *Instance) error {
	dir := Dir(cwd, inst.TaskID, inst.InstanceID)
	body := fmt.Sprintf("# runbook instance %s\n\nslug: %s\nagent: %s\ntask: %s\nbranch: %s\nstatus: **%s**\nedition: %s\n\n## steps\n",
		inst.InstanceID, inst.RunbookSlug, inst.AgentID, inst.TaskID, inst.Branch, inst.Status, inst.EditionHash)

	for _, s := range inst.Steps {
		body += fmt.Sprintf("- %s (%s): %s %s\n", s.ID, s.Kind, s.Status, s.Output)
	}

	return os.WriteFile(filepath.Join(dir, "evidence.md"), []byte(body), filePerm)
}
