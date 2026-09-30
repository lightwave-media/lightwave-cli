package runbook

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/git"
)

var fullCommit = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// cleanCommit uses the checkout's existing ignore policy, including runtime
// prints. It does not excuse other untracked or modified source.
func cleanCommit(cwd string) (string, error) {
	g := git.NewGit(cwd)

	status, err := g.Status()
	if err != nil {
		return "", err
	}

	if !status.Clean {
		return "", nil
	}

	return g.Rev("HEAD")
}

// beginExecution persists provenance before work can start. A nil marker is
// legacy evidence, while true with no commit is permanently unverifiable.
func beginExecution(cwd string, inst *Instance) error {
	if inst.DryRun || inst.ExecutionStarted == nil || *inst.ExecutionStarted {
		return nil
	}

	commit, err := cleanCommit(cwd)
	if err != nil {
		return err
	}

	*inst.ExecutionStarted = true
	if inst.EditionCommit != "" {
		inst.Commit = commit
	}

	return Save(cwd, inst)
}

// invalidateProvenance runs before resuming and after each executed step so a
// later return to the old clean commit cannot erase observed source changes.
func invalidateProvenance(cwd string, inst *Instance) error {
	if inst.Commit == "" {
		return nil
	}

	commit, sourceErr := cleanCommit(cwd)
	if sourceErr == nil && commit == inst.Commit {
		return nil
	}

	inst.Commit = ""
	if err := Save(cwd, inst); err != nil {
		return err
	}

	return sourceErr
}

func requireExecuted(opts *ApplyOpts, inst *Instance) error {
	if opts.Slug == "" || opts.Repo == "" || !fullCommit.MatchString(opts.SHA) {
		return errors.New("executed requires --slug, --repo owner/repo and --sha full 40-hex commit")
	}

	if inst.Status != StatusCompleted || inst.DryRun || inst.ExecutionStarted == nil || !*inst.ExecutionStarted {
		return errors.New("instance has no completed real execution")
	}

	if !fullCommit.MatchString(inst.EditionCommit) {
		return errors.New("instance has no pinned clean Core source revision")
	}

	if inst.RunbookSlug != opts.Slug {
		return errors.New("executed runbook does not match --slug")
	}

	if err := requireRepository(opts, inst); err != nil {
		return err
	}

	if err := invalidateProvenance(opts.Cwd, inst); err != nil {
		return err
	}

	if inst.Commit == "" || inst.Commit != strings.ToLower(opts.SHA) {
		return errors.New("execution commit is absent, invalidated or does not match --sha")
	}

	return requireExecutedSteps(opts, inst)
}

func requireRepository(opts *ApplyOpts, inst *Instance) error {
	parts := strings.Split(opts.Repo, "/")

	const slugParts = 2
	if len(parts) != slugParts || parts[0] == "" || parts[1] == "" || git.NewGit(opts.Cwd).OriginSlug() != opts.Repo {
		return errors.New("checkout origin does not match --repo owner/repo")
	}

	if inst.RepoSlug != opts.Repo && inst.RepoSlug != parts[1] {
		return errors.New("instance repository does not match --repo")
	}

	actual, err := filepath.EvalSymlinks(opts.Cwd)
	if err != nil {
		return err
	}

	recorded, err := filepath.EvalSymlinks(inst.WorktreePath)
	if err != nil || actual != recorded {
		return errors.New("instance belongs to another checkout")
	}

	return nil
}

func requireExecutedSteps(opts *ApplyOpts, inst *Instance) error {
	index, err := LoadIndex(opts.CoreRoot)
	if err != nil {
		return invalidateEditionReceipt(opts.Cwd, inst, err)
	}

	entry, err := Lookup(index, inst.RunbookSlug)
	if err != nil {
		return invalidateEditionReceipt(opts.Cwd, inst, err)
	}

	edition, err := LoadEdition(opts.CoreRoot, entry)
	if err != nil {
		return invalidateEditionReceipt(opts.Cwd, inst, err)
	}

	if err := verifyEdition(opts, inst, edition); err != nil {
		return invalidateEditionReceipt(opts.Cwd, inst, err)
	}

	if len(edition.Steps) != len(inst.Steps) {
		return ErrEditionMismatch
	}

	for i := range edition.Steps {
		step := &edition.Steps[i]
		if step.Path == "" && step.Command == "" {
			return fmt.Errorf("step %s has no executable work", step.ID)
		}

		state := inst.Steps[i]
		if state.ID != step.ID || state.Kind != step.Kind || state.Status != stepCompleted || (step.HighBlast && state.SignoffTier == "") {
			return fmt.Errorf("step %s has no completed execution with required signoff", step.ID)
		}
	}

	return nil
}

func invalidateEditionReceipt(cwd string, inst *Instance, cause error) error {
	inst.Commit = ""
	if err := Save(cwd, inst); err != nil {
		return err
	}

	return fmt.Errorf("%w: %w", ErrEditionMismatch, cause)
}
