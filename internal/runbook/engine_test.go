package runbook_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/runbook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func writeCatalog(t *testing.T, core string, slugs map[string]string, mdx map[string]string) {
	t.Helper()
	idx := "_meta:\n  version: \"0.1.0\"\ncategories:\n  test:\n"
	for slug, dir := range slugs {
		idx += "    " + slug + ": " + dir + "\n"
		if body, ok := mdx[slug]; ok {
			writeFile(t, core, filepath.Join("src/runbooks", dir, "runbook.mdx"), body)
		}
	}
	writeFile(t, core, "src/runbooks/__index.yaml", idx)
}

func gitOk(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), args[0], args[1:]...)
	cmd.Dir = dir
	require.NoError(t, cmd.Run(), args)
}

func initWorktree(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	gitOk(t, dir, "git", "init", "-b", "main")
	gitOk(t, dir, "git", "config", "user.email", "test@test.com")
	gitOk(t, dir, "git", "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644))
	gitOk(t, dir, "git", "add", ".")
	gitOk(t, dir, "git", "commit", "-m", "init")
	if branch != "main" {
		gitOk(t, dir, "git", "checkout", "-b", branch)
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".lw-worktree.yaml"), []byte("issue: t1\nbranch: "+branch+"\n"), 0o644))
	}

	return dir
}

func startOpts(core, cwd, slug string) *runbook.StartOpts {
	return &runbook.StartOpts{
		CoreRoot:   core,
		Cwd:        cwd,
		Slug:       slug,
		Agent:      "v_cli-developer",
		Task:       "325",
		Repo:       "lightwave-cli",
		InstanceID: "inst-1",
	}
}

func TestLoadIndex_WalksEveryCurrentPrint(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{
		"alpha": "test/alpha",
		"beta":  "test/beta",
	}, map[string]string{
		"alpha": "---\nname: alpha\n---\n",
		"beta":  "---\nname: beta\n---\n",
	})

	index, err := runbook.LoadIndex(core)
	require.NoError(t, err)
	require.Len(t, index, 2, "census must walk every current catalog print, not a named instance")
	assert.Equal(t, "test/alpha", index["alpha"].Dir)
	assert.Equal(t, "test/beta", index["beta"].Dir)
}

func TestStart_RefusesMainWithNoWrite(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"ping": "test/ping"}, map[string]string{
		"ping": `<Check id="ok" description="noop" command="true" />`,
	})
	cwd := initWorktree(t, "main")

	_, err := runbook.Start(startOpts(core, cwd, "ping"))
	require.ErrorIs(t, err, runbook.ErrOnMain)
	_, statErr := os.Stat(filepath.Join(cwd, ".tasks"))
	assert.True(t, os.IsNotExist(statErr), "refuse on main must not write an instance")
}

func TestStart_NoMatchFilesToolGap(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"ping": "test/ping"}, map[string]string{
		"ping": `<Check id="ok" description="noop" command="true" />`,
	})
	cwd := initWorktree(t, "feature/325-x")

	_, err := runbook.Start(startOpts(core, cwd, "does-not-exist"))
	require.ErrorIs(t, err, runbook.ErrNoMatch)
	assert.Contains(t, err.Error(), "tool-gap")
}

func TestApply_PausesOnHighBlastAndDoesNotContinue(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	mdx := `<Check id="ping" description="safe" command="touch check-ran" />
<Command id="nuke" description="high blast" command="touch command-ran" />`
	writeCatalog(t, core, map[string]string{"gated": "test/gated"}, map[string]string{"gated": mdx})
	cwd := initWorktree(t, "feature/325-x")

	inst, err := runbook.Start(startOpts(core, cwd, "gated"))
	require.NoError(t, err)

	got, err := runbook.Apply(&runbook.ApplyOpts{CoreRoot: core, Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID})
	require.NoError(t, err)
	assert.Equal(t, runbook.StatusWaitingApproval, got.Status)
	assert.Equal(t, "nuke", got.CurrentStepID)
	_, checkErr := os.Stat(filepath.Join(cwd, "check-ran"))
	require.NoError(t, checkErr, "check steps run before the pause")
	_, cmdErr := os.Stat(filepath.Join(cwd, "command-ran"))
	assert.True(t, os.IsNotExist(cmdErr), "high-blast command must not run")
}

func TestApply_CheckFailureDoesNotMarkDone(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"fail": "test/fail"}, map[string]string{
		"fail": `<Check id="nope" description="fail" command="false" />`,
	})
	cwd := initWorktree(t, "feature/325-x")

	inst, err := runbook.Start(startOpts(core, cwd, "fail"))
	require.NoError(t, err)

	got, err := runbook.Apply(&runbook.ApplyOpts{CoreRoot: core, Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID})
	require.ErrorIs(t, err, runbook.ErrCheckFailed)
	assert.Equal(t, runbook.StatusFailed, got.Status)
	assert.NotEqual(t, runbook.StatusCompleted, got.Status)
}

func TestApply_EditionMismatch(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"ping": "test/ping"}, map[string]string{
		"ping": `<Check id="ok" description="noop" command="true" />`,
	})
	cwd := initWorktree(t, "feature/325-x")

	inst, err := runbook.Start(startOpts(core, cwd, "ping"))
	require.NoError(t, err)
	inst.EditionHash = "deadbeef"
	require.NoError(t, runbook.Save(cwd, inst))

	got, err := runbook.Apply(&runbook.ApplyOpts{CoreRoot: core, Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID})
	require.ErrorIs(t, err, runbook.ErrEditionMismatch)
	assert.Equal(t, runbook.StatusFailed, got.Status)
}

func TestStepCompleteThenApply_LeavesEvidence(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	mdx := `<Check id="ping" description="safe" command="true" />
<Command id="nuke" description="high blast" command="true" />`
	writeCatalog(t, core, map[string]string{"gated": "test/gated"}, map[string]string{"gated": mdx})
	cwd := initWorktree(t, "feature/325-x")

	inst, err := runbook.Start(startOpts(core, cwd, "gated"))
	require.NoError(t, err)
	_, err = runbook.Apply(&runbook.ApplyOpts{CoreRoot: core, Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID})
	require.NoError(t, err)

	_, err = runbook.StepComplete(&runbook.ApplyOpts{
		Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID, StepID: "nuke", SignoffTier: "operator",
	})
	require.NoError(t, err)

	got, err := runbook.Apply(&runbook.ApplyOpts{CoreRoot: core, Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID})
	require.NoError(t, err)
	assert.Equal(t, runbook.StatusCompleted, got.Status)

	evidence := filepath.Join(runbook.Dir(cwd, inst.TaskID, inst.InstanceID), "evidence.md")
	raw, err := os.ReadFile(evidence)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "completed")
	assert.Contains(t, string(raw), inst.InstanceID)

	st, err := runbook.Status(&runbook.ApplyOpts{Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID, Require: "completed"})
	require.NoError(t, err)
	assert.Equal(t, runbook.StatusCompleted, st.Status)
}

func TestStepComplete_DenyDoesNotComplete(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"gated": "test/gated"}, map[string]string{
		"gated": `<Command id="nuke" description="high blast" command="true" />`,
	})
	cwd := initWorktree(t, "feature/325-x")

	inst, err := runbook.Start(startOpts(core, cwd, "gated"))
	require.NoError(t, err)
	_, err = runbook.Apply(&runbook.ApplyOpts{CoreRoot: core, Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID})
	require.NoError(t, err)

	got, err := runbook.StepComplete(&runbook.ApplyOpts{
		Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID, StepID: "nuke", SignoffTier: "deny",
	})
	require.ErrorIs(t, err, runbook.ErrDenied)
	assert.Equal(t, runbook.StatusWaitingApproval, got.Status)
}

func TestPhantomIndex_EditionMismatch(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"ghost": "test/ghost"}, nil)
	cwd := initWorktree(t, "feature/325-x")

	_, err := runbook.Start(startOpts(core, cwd, "ghost"))
	require.ErrorIs(t, err, runbook.ErrEditionMismatch)
}
