package runbook_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/runbook"
)

// Pins for the review of #557: where a value becomes code, how a timeout
// ends a script, and what a dry run or a finished instance may do.

const injection = "x; touch pwned"

// A script's source is code. An input rendered into it must be inert there;
// the same value is still usable as data from the environment.
func TestApply_ScriptRefusesAnInputThatWouldBecomeCode(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		script  string
		wantErr bool
	}{
		"rendered into the source is refused": {`touch {{ .inputs.Name }}` + "\n", true},
		"read from the environment is data":   {`touch "$Name"` + "\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			core := t.TempDir()
			writeCatalog(t, core, map[string]string{"inj": "test/inj"}, map[string]string{
				"inj": inputsBlock(nameInputs) + `<Check id="make" path="checks/make.sh" />`,
			})
			writeFile(t, core, "src/runbooks/test/inj/checks/make.sh", tc.script)
			cwd, inst := startWith(t, core, "inj", map[string]string{nameInput: injection}, false)

			got, err := apply(core, cwd, inst, "")
			assert.NoFileExists(t, filepath.Join(cwd, "pwned"), "the value never ran as a command")

			if tc.wantErr {
				require.ErrorIs(t, err, runbook.ErrUnsafeValue)
				assert.Equal(t, runbook.StatusFailed, got.Status)

				return
			}

			require.NoError(t, err)
			assert.FileExists(t, filepath.Join(cwd, injection))
		})
	}
}

// `bash -c '...'` has no shell syntax outside its quotes, so it once ran as
// argv without sign-off, and the value inside the quotes ran as code.
func TestApply_InterpreterCheckWaitsAndRefusesUnsafeValues(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"interp": "test/interp"}, map[string]string{
		"interp": inputsBlock(nameInputs) + `<Check id="sh" command="bash -c 'touch {{ .inputs.Name }}'" />`,
	})
	cwd, inst := startWith(t, core, "interp", map[string]string{nameInput: injection}, false)

	got, err := apply(core, cwd, inst, "")
	require.NoError(t, err)
	require.Equal(t, runbook.StatusWaitingApproval, got.Status, "an interpreter call waits for sign-off")

	_, err = runbook.StepComplete(&runbook.ApplyOpts{
		Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID, StepID: "sh", SignoffTier: signoffOperator,
	})
	require.NoError(t, err)

	_, err = apply(core, cwd, inst, "")
	require.ErrorIs(t, err, runbook.ErrUnsafeValue, "sign-off approves the command, not the values in it")
	assert.NoFileExists(t, filepath.Join(cwd, "pwned"))
}

// Killing only bash left its children running and holding the output pipe,
// so a timed-out script ran on until they exited.
func TestApply_TimeoutKillsAScriptsChildren(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"slow": "test/slow"}, map[string]string{
		"slow": `<Check id="slow" path="checks/slow.sh" timeoutMs={300} />`,
	})
	writeFile(t, core, "src/runbooks/test/slow/checks/slow.sh", "sleep 30 &\necho $! > child.pid\nwait\n")
	cwd, inst := startWith(t, core, "slow", nil, false)

	began := time.Now()
	_, err := apply(core, cwd, inst, "")
	require.ErrorIs(t, err, runbook.ErrCheckFailed)
	assert.Contains(t, err.Error(), "timed out")
	assert.Less(t, time.Since(began), 5*time.Second)

	raw, err := os.ReadFile(filepath.Join(cwd, "child.pid"))
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	assert.Eventually(t, func() bool { return syscall.Kill(pid, 0) != nil }, 3*time.Second, 50*time.Millisecond,
		"the script's child was killed with it")
}

// A step that exits 0 but leaves a background child holding stdout (a server
// it started) succeeded; waiting on the child would hang the runbook.
func TestApply_ScriptLeavingABackgroundChildCompletes(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"bg": "test/bg"}, map[string]string{
		"bg": `<Check id="serve" path="checks/serve.sh" />`,
	})
	writeFile(t, core, "src/runbooks/test/bg/checks/serve.sh", "sleep 10 &\nexit 0\n")
	cwd, inst := startWith(t, core, "bg", nil, false)

	began := time.Now()
	got, err := apply(core, cwd, inst, "")
	require.NoError(t, err)
	assert.Equal(t, runbook.StatusCompleted, got.Status)
	assert.Less(t, time.Since(began), 6*time.Second)
}

// A dry-run-aware script that reads a skipped step's outputs has nothing to
// render; it is skipped, not a failed run.
func TestApply_DryRunSkipsAScriptReadingASkippedStep(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"chain": "test/chain"}, map[string]string{
		"chain": `<Check id="make" path="checks/make.sh" />
<Check id="use" path="checks/use.sh" />`,
	})
	writeFile(t, core, "src/runbooks/test/chain/checks/make.sh", "echo \"id=42\" >> \"$RUNBOOK_OUTPUT\"\n")
	writeFile(t, core, "src/runbooks/test/chain/checks/use.sh",
		"[ \"$RUNBOOK_DRY_RUN\" = true ] && log_info 'would use {{ .outputs.make.id }}'\n")
	cwd, inst := startWith(t, core, "chain", nil, true)

	got, err := apply(core, cwd, inst, "")
	require.NoError(t, err)
	assert.Equal(t, runbook.StatusCompleted, got.Status)
	assert.Equal(t, []string{"skipped", "skipped"}, []string{got.Steps[0].Status, got.Steps[1].Status})
	assert.Contains(t, got.Steps[1].Output, `step "make"`)
}

// expected= binds a path= script the same as an inline command.
func TestApply_ScriptMustPrintItsExpectedWord(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"exp": "test/exp"}, map[string]string{
		"exp": `<Check id="look" path="checks/look.sh" expected="found" />`,
	})
	writeFile(t, core, "src/runbooks/test/exp/checks/look.sh", "echo missing\n")
	cwd, inst := startWith(t, core, "exp", nil, false)

	got, err := apply(core, cwd, inst, "")
	require.ErrorIs(t, err, runbook.ErrCheckFailed)
	assert.Equal(t, runbook.StatusFailed, got.Status)
}

// A dry run lists a Template's files and writes none, so the step is skipped.
func TestApply_DryRunTemplateIsSkipped(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeFile(t, core, "src/runbooks/test/tmpl/templates/mini/boilerplate.yml", "variables: []\n")
	writeFile(t, core, "src/runbooks/test/tmpl/templates/mini/made.txt", "made\n")
	writeCatalog(t, core, map[string]string{"tmpl": "test/tmpl"}, map[string]string{
		"tmpl": `<Template id="scaffold" path="templates/mini" />`,
	})
	cwd, inst := startWith(t, core, "tmpl", nil, true)

	_, err := runbook.StepComplete(&runbook.ApplyOpts{
		Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID, StepID: "scaffold", SignoffTier: signoffOperator,
	})
	require.NoError(t, err)

	got, err := apply(core, cwd, inst, "")
	require.NoError(t, err)
	assert.Equal(t, "skipped", got.Steps[0].Status)
	assert.NoFileExists(t, filepath.Join(cwd, "made.txt"))
}

// Signing off a step on a finished instance set it running again.
func TestStepComplete_RefusesAFinishedInstance(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"act": "test/act"}, map[string]string{
		"act": `<Command id="act" command="touch acted" />`,
	})
	cwd, inst := startWith(t, core, "act", nil, false)

	_, err := runbook.Cancel(&runbook.ApplyOpts{Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID})
	require.NoError(t, err)

	_, err = runbook.StepComplete(&runbook.ApplyOpts{
		Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID, StepID: "act", SignoffTier: signoffOperator,
	})
	require.ErrorIs(t, err, runbook.ErrFinished)

	got, err := runbook.Load(cwd, inst.TaskID, inst.InstanceID)
	require.NoError(t, err)
	assert.Equal(t, runbook.StatusCancelled, got.Status)
}

// Two instances started in the same second must still order by time.
func TestResolveInstanceID_OrdersWithinTheSameSecond(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"latest": "test/latest"}, map[string]string{
		"latest": `<Check id="ok" command="true" />`,
	})
	cwd := initWorktree(t, "feature/546-same-second")

	var last string

	for range 5 {
		opts := startOpts(core, cwd, "latest")
		opts.InstanceID = "" // a random id each time, as in real use

		inst, err := runbook.Start(opts)
		require.NoError(t, err)

		last = inst.InstanceID
	}

	id, err := runbook.ResolveInstanceID(cwd, "325", "")
	require.NoError(t, err)
	assert.Equal(t, last, id)
}
