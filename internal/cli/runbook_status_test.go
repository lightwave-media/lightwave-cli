package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/git"
	"github.com/lightwave-media/lightwave-cli/internal/runbook"
	"github.com/lightwave-media/lightwave-cli/internal/testutil"
	"github.com/lightwave-media/lightwave-cli/internal/testutil/gitfixture"
	"github.com/stretchr/testify/require"
)

const receiptRepoFlag = "repo"

//nolint:paralleltest // handler globals, cwd, stdout and config are process-wide
func TestRunbookStatusHandlerBindsExecutionExpectations(t *testing.T) {
	cwd, core := executionHandlerFixture(t)
	inst, err := runbook.Start(&runbook.StartOpts{
		CoreRoot: core, Cwd: cwd, Slug: "receipt", Agent: "fixture", Task: "receipt",
		Repo: "lightwave-cli", InstanceID: "one",
	})
	require.NoError(t, err)
	_, err = runbook.Apply(&runbook.ApplyOpts{CoreRoot: core, Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID})
	require.NoError(t, err)
	sha, err := git.NewGit(cwd).Rev("HEAD")
	require.NoError(t, err)
	flags := map[string]any{"task": inst.TaskID, "instance": inst.InstanceID, "require": "executed",
		"slug": "receipt", receiptRepoFlag: "lightwave-media/lightwave-cli", "sha": sha}
	_, err = testutil.RunHandler(t, "runbook.apply", nil, map[string]any{"cwd": t.TempDir(), "task": inst.TaskID})
	require.Error(t, err)
	_, err = testutil.RunHandler(t, "runbook.status", nil, flags)
	require.NoError(t, err)
	flags["sha"] = "wrong"
	_, err = testutil.RunHandler(t, "runbook.status", nil, flags)
	require.ErrorContains(t, err, "40-hex")
	flags["sha"] = sha
	delete(flags, "slug")
	_, err = testutil.RunHandler(t, "runbook.status", nil, flags)
	require.ErrorContains(t, err, "--slug", "a previous call must not supply a missing expectation")
}

func executionHandlerFixture(t *testing.T) (string, string) {
	t.Helper()
	cwd, fleet := t.TempDir(), t.TempDir()
	t.Chdir(cwd)
	t.Setenv("LW_LIGHTWAVE_ROOT", fleet)
	config.Reset()
	t.Cleanup(config.Reset)
	core := filepath.Join(fleet, "lightwave-core")
	for rel, body := range map[string]string{
		"src/runbooks/__index.yaml":             "categories:\n  test:\n    receipt: test/receipt\n",
		"src/runbooks/test/receipt/runbook.mdx": `<Check id="ok" command="true" />`,
	} {
		path := filepath.Join(core, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	for rel, body := range map[string]string{".gitignore": ".tasks/\n", ".lw-worktree.yaml": "issue: receipt\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(cwd, rel), []byte(body), 0o644))
	}

	for _, args := range [][]string{{"init", "-b", "main"}, {"add", "."}, {"commit", "-m", "published verifier"}} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = core
		cmd.Env = gitfixture.Env()
		require.NoError(t, cmd.Run())
	}
	for _, args := range [][]string{{"init", "-b", "feature/receipt"}, {"add", "."}, {"commit", "-m", "fixture"},
		{"remote", "add", "origin", "https://github.com/lightwave-media/lightwave-cli.git"}} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = cwd
		cmd.Env = gitfixture.Env()
		require.NoError(t, cmd.Run())
	}
	return cwd, core
}
