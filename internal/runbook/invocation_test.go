package runbook_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/runbook"
)

// Where a runbook may run, and the one-line `apply <slug>` (#553, #537).

const (
	lookSlug     = "look"
	actSlug      = "act"
	actDir       = "test/act"
	checkOnlyMDX = `<Check id="look" command="true" />`
	mutatingMDX  = `<Check id="look" command="true" />
<Command id="act" command="touch acted" />`
)

func TestStart_CheckOnlyRunbookRunsOutsideAWorktree(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{lookSlug: "test/look"}, map[string]string{lookSlug: checkOnlyMDX})

	// session-signoff runs after its PR merged and its worktree is gone:
	// not a worktree, not even a repository.
	cwd, root := t.TempDir(), t.TempDir()
	opts := startOpts(core, cwd, lookSlug)
	opts.CheckOnlyRoot = root

	inst, err := runbook.Start(opts)
	require.NoError(t, err)
	assert.FileExists(t, runbook.Path(root, inst.TaskID, inst.InstanceID), "the print goes under the check-only root")
	assert.NoDirExists(t, filepath.Join(cwd, ".tasks"), "nothing is written where it ran")

	got, err := runbook.Apply(&runbook.ApplyOpts{
		CoreRoot: core, Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID, CheckOnlyRoot: root,
	})
	require.NoError(t, err)
	assert.Equal(t, runbook.StatusCompleted, got.Status)
}

// A runbook that can change files still runs only in a task worktree.
func TestStart_RunbookThatChangesFilesRefusesOutsideAWorktree(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{actSlug: actDir}, map[string]string{actSlug: mutatingMDX})

	cwd, root := initWorktree(t, "main"), t.TempDir()
	opts := startOpts(core, cwd, actSlug)
	opts.CheckOnlyRoot = root

	_, err := runbook.Start(opts)
	require.ErrorIs(t, err, runbook.ErrOnMain)
	assert.NoDirExists(t, filepath.Join(root, ".tasks"))
	assert.NoDirExists(t, filepath.Join(cwd, ".tasks"))
}

// The library never picks $HOME on its own: with no check-only root given, a
// check-only runbook outside a worktree is refused, not written somewhere.
func TestStart_CheckOnlyOutsideAWorktreeNeedsARoot(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{lookSlug: "test/look"}, map[string]string{lookSlug: checkOnlyMDX})
	cwd := t.TempDir()

	_, err := runbook.Start(startOpts(core, cwd, lookSlug))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no check-only root")
	assert.NoDirExists(t, filepath.Join(cwd, ".tasks"))
}

func runOpts(core, cwd string) (*runbook.StartOpts, *runbook.ApplyOpts) {
	start := &runbook.StartOpts{CoreRoot: core, Cwd: cwd, Slug: actSlug, Agent: "v_cli-developer", Task: "553"}
	apply := &runbook.ApplyOpts{CoreRoot: core, Cwd: cwd, Task: "553"}

	return start, apply
}

func TestRun_StartsThenResumesThenStartsAfresh(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{actSlug: actDir}, map[string]string{actSlug: mutatingMDX})
	cwd := initWorktree(t, "feature/537-run")

	start, apply := runOpts(core, cwd)
	first, err := runbook.Run(start, apply)
	require.NoError(t, err)
	require.Equal(t, runbook.StatusWaitingApproval, first.Status, "a fresh instance runs up to the sign-off")

	_, err = runbook.StepComplete(&runbook.ApplyOpts{
		Cwd: cwd, Task: "553", InstanceID: first.InstanceID, StepID: actSlug, SignoffTier: signoffOperator,
	})
	require.NoError(t, err)

	start, apply = runOpts(core, cwd)
	resumed, err := runbook.Run(start, apply)
	require.NoError(t, err)
	assert.Equal(t, first.InstanceID, resumed.InstanceID, "the open instance is resumed, not replaced")
	assert.Equal(t, runbook.StatusCompleted, resumed.Status)
	assert.FileExists(t, filepath.Join(cwd, "acted"))

	start, apply = runOpts(core, cwd)
	again, err := runbook.Run(start, apply)
	require.NoError(t, err)
	assert.NotEqual(t, first.InstanceID, again.InstanceID, "a finished instance is not reopened")
}

// Inputs are bound when an instance starts. Vars given while one is open
// would otherwise be dropped without a word.
func TestRun_RefusesVarsWhileAnInstanceIsOpen(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	mdx := inputsBlock(nameInputs) + mutatingMDX
	writeCatalog(t, core, map[string]string{actSlug: actDir}, map[string]string{actSlug: mdx})
	cwd := initWorktree(t, "feature/537-vars")

	start, apply := runOpts(core, cwd)
	start.Vars = map[string]string{nameInput: "first"}
	first, err := runbook.Run(start, apply)
	require.NoError(t, err)

	start, apply = runOpts(core, cwd)
	start.Vars = map[string]string{nameInput: "second"}
	_, err = runbook.Run(start, apply)
	require.ErrorIs(t, err, runbook.ErrBoundAtStart)
	assert.Contains(t, err.Error(), first.InstanceID, "the error names the open instance")
}

// Dry-run binds at start like inputs. A dry run resuming a real instance would
// run its signed-off steps for real; a real run resuming a dry one would
// complete it with every step skipped and pass `status --require completed`.
func TestRun_RefusesADifferentDryRunWhileAnInstanceIsOpen(t *testing.T) {
	t.Parallel()

	for name, firstDry := range map[string]bool{"dry over real": false, "real over dry": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			core := t.TempDir()
			writeCatalog(t, core, map[string]string{actSlug: actDir}, map[string]string{actSlug: mutatingMDX})
			cwd := initWorktree(t, "feature/537-dry")

			start, apply := runOpts(core, cwd)
			start.DryRun = firstDry
			first, err := runbook.Run(start, apply)
			require.NoError(t, err)
			require.Equal(t, runbook.StatusWaitingApproval, first.Status)

			start, apply = runOpts(core, cwd)
			start.DryRun = !firstDry
			_, err = runbook.Run(start, apply)
			require.ErrorIs(t, err, runbook.ErrBoundAtStart)
			assert.NoFileExists(t, filepath.Join(cwd, "acted"))
		})
	}
}

// Without --instance, status answers for the newest instance in either root,
// not the first root that has any.
func TestStatus_WithoutInstanceReadsTheNewestAcrossRoots(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{lookSlug: "test/look"}, map[string]string{lookSlug: checkOnlyMDX})
	cwd, root := initWorktree(t, "feature/537-roots"), t.TempDir()

	older := startOpts(core, cwd, lookSlug)
	older.InstanceID = ""
	inWorktree, err := runbook.Start(older)
	require.NoError(t, err)
	inWorktree.CreatedAt = "2026-01-01T00:00:00.000000000Z"
	require.NoError(t, runbook.Save(cwd, inWorktree))

	newer := startOpts(core, t.TempDir(), lookSlug) // not a worktree: goes under root
	newer.InstanceID = ""
	newer.CheckOnlyRoot = root
	outside, err := runbook.Start(newer)
	require.NoError(t, err)

	got, err := runbook.Status(&runbook.ApplyOpts{Cwd: cwd, Task: outside.TaskID, CheckOnlyRoot: root})
	require.NoError(t, err)
	assert.Equal(t, outside.InstanceID, got.InstanceID)
}

// Re-applying a failed instance still fails; it once exited 0.
func TestApply_AFailedInstanceStaysFailed(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"fails": "test/fails"}, map[string]string{"fails": `<Check id="no" command="false" />`})
	cwd := initWorktree(t, "feature/537-failed")

	inst, err := runbook.Start(startOpts(core, cwd, "fails"))
	require.NoError(t, err)

	opts := &runbook.ApplyOpts{CoreRoot: core, Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID}
	_, err = runbook.Apply(opts)
	require.ErrorIs(t, err, runbook.ErrCheckFailed)

	again, err := runbook.Apply(opts)
	require.ErrorIs(t, err, runbook.ErrCheckFailed)
	assert.Equal(t, runbook.StatusFailed, again.Status)
}

func TestDescribe_ReportsInputsStepsAndWhereItRuns(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	mdx := "---\nname: Act\ndescription: Does a thing\nstatus: active\n---\n" + inputsBlock(nameInputs) + mutatingMDX
	writeCatalog(t, core, map[string]string{actSlug: actDir, lookSlug: "test/look"},
		map[string]string{actSlug: mdx, lookSlug: checkOnlyMDX})

	desc, err := runbook.Describe(core, actSlug)
	require.NoError(t, err)
	assert.Equal(t, "Does a thing", desc.Description)
	assert.Equal(t, []string{"Name", "Loud"}, []string{desc.Inputs[0].Name, desc.Inputs[1].Name})
	assert.Equal(t, []string{lookSlug, actSlug}, []string{desc.Steps[0].ID, desc.Steps[1].ID})
	assert.False(t, desc.CheckOnly)

	look, err := runbook.Describe(core, lookSlug)
	require.NoError(t, err)
	assert.True(t, look.CheckOnly)

	_, err = runbook.Describe(core, "absent")
	require.ErrorIs(t, err, runbook.ErrNoMatch)
}
