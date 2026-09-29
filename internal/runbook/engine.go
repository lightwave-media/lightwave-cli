package runbook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
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
	// Vars fill the runbook's <Inputs>; they are checked here, before any
	// instance exists, and bound again from the pinned edition on each apply.
	Vars map[string]string
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
	// AuditPath receives one audit_event row per step decision; see audit.go.
	AuditPath string
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

	if _, err := BindInputs(edition.Inputs, opts.Vars); err != nil {
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
		Vars:         opts.Vars,
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

	if inst.Finished() {
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

	inputs, err := BindInputs(edition.Inputs, inst.Vars)
	if err != nil {
		return inst, err
	}

	run := &runner{
		cwd:        opts.Cwd,
		runbookDir: filepath.Dir(edition.Path),
		inputs:     inputs,
		outputs:    completedOutputs(inst.Steps),
		dryRun:     inst.DryRun,
	}

	inst.Status = StatusRunning

	for i := range inst.Steps {
		st := &inst.Steps[i]
		if st.Status == stepCompleted || st.Status == stepSkipped {
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

			return inst, audit(opts.AuditPath, inst, &edStep, "pause", "awaiting_signoff", stepResult{})
		}

		// HighBlast is a SIGN-OFF gate, not an execution gate: past the check
		// above a signed step must actually do its work. It once reported
		// signed Command and Template steps completed without running them, so
		// a runbook declaring a Template created nothing and said it had.
		res, runErr := run.run(&edStep)

		st.Output = tailOutput(res.output)
		st.Outputs = res.outputs
		st.Warned = res.warned

		if res.outputs != nil {
			run.outputs[st.ID] = res.outputs
		}

		if auditErr := audit(opts.AuditPath, inst, &edStep, "allow", outcomeOf(res, runErr), res); auditErr != nil && runErr == nil {
			runErr = fmt.Errorf("step ran but its audit row was not written: %w", auditErr)
		}

		if runErr != nil {
			st.Status = stepFailed
			inst.Status = StatusFailed
			inst.CurrentStepID = st.ID
			_ = Save(opts.Cwd, inst)
			_ = writeEvidence(opts.Cwd, inst)

			return inst, fmt.Errorf("%w: %s: %w", ErrCheckFailed, st.ID, runErr)
		}

		st.Status = stepCompleted
		if res.skipped {
			st.Status = stepSkipped
		}
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

	// Signing off a step on a finished instance set it running again, so a
	// cancelled or failed runbook came back to life on the next apply.
	if inst.Finished() {
		return inst, fmt.Errorf("%w: %s is %s", ErrFinished, inst.InstanceID, inst.Status)
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

// completedOutputs rebuilds {{ .outputs }} from steps an earlier apply ran,
// so a run resumed after a sign-off still sees them.
func completedOutputs(steps []StepState) map[string]map[string]string {
	outputs := map[string]map[string]string{}

	for _, st := range steps {
		if st.Status == stepCompleted && st.Outputs != nil {
			outputs[st.ID] = st.Outputs
		}
	}

	return outputs
}

func outcomeOf(res stepResult, err error) string {
	switch {
	case err != nil:
		return "fail"
	case res.skipped:
		return "skipped"
	case res.warned:
		return "warn"
	default:
		return "ok"
	}
}

// maxStepOutput bounds what an instance print keeps of one step's output.
const maxStepOutput = 4096

func tailOutput(output string) string {
	if len(output) <= maxStepOutput {
		return output
	}

	return "…" + output[len(output)-maxStepOutput:]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}

// writeEvidence writes status lines only. evidence.md is what gets posted to
// the task's GitHub issue, so step output — which can carry anything a script
// printed — stays in the 0600 instance print (lightwave-cli#546).
func writeEvidence(cwd string, inst *Instance) error {
	var body strings.Builder

	fmt.Fprintf(&body, "# runbook instance %s\n\nslug: %s\nagent: %s\ntask: %s\nbranch: %s\nstatus: **%s**\nedition: %s\n\n## steps\n",
		inst.InstanceID, inst.RunbookSlug, inst.AgentID, inst.TaskID, inst.Branch, inst.Status, inst.EditionHash)

	for _, s := range inst.Steps {
		warned := ""
		if s.Warned {
			warned = " (warned)"
		}

		fmt.Fprintf(&body, "- %s (%s): %s%s\n", s.ID, s.Kind, s.Status, warned)
	}

	dir := Dir(cwd, inst.TaskID, inst.InstanceID)

	return os.WriteFile(filepath.Join(dir, "evidence.md"), []byte(body.String()), filePerm)
}
