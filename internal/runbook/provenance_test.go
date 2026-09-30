package runbook_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/git"
	"github.com/lightwave-media/lightwave-cli/internal/runbook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const receiptRepo = "lightwave-media/lightwave-cli"
const receiptSkippedStatus = "skipped"

func receiptFixture(t *testing.T, body string, dry bool) (*runbook.ApplyOpts, *runbook.Instance) {
	t.Helper()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"receipt": "test/receipt"}, map[string]string{"receipt": body})
	writeFile(t, core, "src/schemas/shared.yaml", "version: 1\n")
	gitOk(t, core, "git", "init", "-b", "main")
	gitOk(t, core, "git", "add", ".")
	gitOk(t, core, "git", "commit", "-m", "published verifier")
	cwd := initWorktree(t, "feature/receipt")
	writeFile(t, cwd, ".gitignore", ".tasks/\n")
	gitOk(t, cwd, "git", "add", ".")
	gitOk(t, cwd, "git", "commit", "-m", "fixture")
	gitOk(t, cwd, "git", "remote", "add", "origin", "https://github.com/"+receiptRepo+".git")
	sha, err := git.NewGit(cwd).Rev("HEAD")
	require.NoError(t, err)
	opts := startOpts(core, cwd, "receipt")
	opts.DryRun = dry
	inst, err := runbook.Start(opts)
	require.NoError(t, err)
	return &runbook.ApplyOpts{CoreRoot: core, Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID,
		Require: "executed", Slug: "receipt", Repo: receiptRepo, SHA: sha}, inst
}

func receiptFields(t *testing.T, opts *runbook.ApplyOpts) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(runbook.Path(opts.Cwd, opts.Task, opts.InstanceID))
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &fields))
	return fields
}

func TestExecutedReceiptRequiresActualExecution(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="ok" command="true" />`, false)
	fields := receiptFields(t, opts)
	assert.Equal(t, false, fields["execution_started"])
	assert.Empty(t, fields["commit"])
	_, err := runbook.Status(opts)
	require.Error(t, err)
	_, err = runbook.Apply(opts)
	require.NoError(t, err)
	_, err = runbook.Status(opts)
	require.NoError(t, err)
	fields = receiptFields(t, opts)
	assert.Equal(t, true, fields["execution_started"])
	assert.Equal(t, opts.SHA, fields["commit"])
}

func TestExecutedReceiptRejectsPreview(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="ok" command="true" />`, true)
	_, err := runbook.Apply(opts)
	require.NoError(t, err)
	_, err = runbook.Status(opts)
	require.Error(t, err)
	opts.Require = "completed"
	_, err = runbook.Status(opts)
	require.NoError(t, err, "legacy status meaning stays compatible")
	assert.Equal(t, false, receiptFields(t, opts)["execution_started"])
}

func TestExecutedReceiptRejectsMissingOrWrongExpectations(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"slug", "repo", "sha", "short-sha", "wrong-slug", "wrong-repo", "wrong-sha"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			opts, _ := receiptFixture(t, `<Check id="ok" command="true" />`, false)
			_, err := runbook.Apply(opts)
			require.NoError(t, err)
			switch field {
			case "slug":
				opts.Slug = ""
			case "repo":
				opts.Repo = ""
			case "sha":
				opts.SHA = ""
			case "short-sha":
				opts.SHA = opts.SHA[:7]
			case "wrong-slug":
				opts.Slug = "another"
			case "wrong-repo":
				opts.Repo = "other/lightwave-cli"
			case "wrong-sha":
				opts.SHA = "0000000000000000000000000000000000000000"
			}
			_, err = runbook.Status(opts)
			require.Error(t, err)
		})
	}
}

func TestExecutedReceiptNeverRecapturesAfterDirtyExecution(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="first" command="true" /><Command id="second" command="true" />`, false)
	writeFile(t, opts.Cwd, "untracked-source", "dirty")
	_, err := runbook.Apply(opts)
	require.NoError(t, err)
	assert.Equal(t, true, receiptFields(t, opts)["execution_started"])
	assert.Empty(t, receiptFields(t, opts)["commit"])
	require.NoError(t, os.Remove(filepath.Join(opts.Cwd, "untracked-source")))
	opts.StepID = "second"
	_, err = runbook.StepComplete(opts)
	require.NoError(t, err)
	_, err = runbook.Apply(opts)
	require.NoError(t, err)
	_, err = runbook.Status(opts)
	require.Error(t, err)
	assert.Empty(t, receiptFields(t, opts)["commit"])
}

func TestExecutedReceiptInvalidatesSourceChangesDuringStep(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="mutate" command="touch new-source" />`, false)
	_, err := runbook.Apply(opts)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(opts.Cwd, "new-source")))
	_, err = runbook.Status(opts)
	require.Error(t, err, "returning to clean cannot erase the observed change")
	assert.Empty(t, receiptFields(t, opts)["commit"])
}

func TestExecutedReceiptCapturesApplyCommitInsteadOfStart(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="ok" command="true" />`, false)
	gitOk(t, opts.Cwd, "git", "commit", "--allow-empty", "-m", "new revision")
	sha, err := git.NewGit(opts.Cwd).Rev("HEAD")
	require.NoError(t, err)
	require.NotEqual(t, opts.SHA, sha)
	opts.SHA = sha
	_, err = runbook.Apply(opts)
	require.NoError(t, err)
	_, err = runbook.Status(opts)
	require.NoError(t, err)
}

func TestExecutedReceiptDoesNotBackfillLegacyOrInterruptedRuns(t *testing.T) {
	t.Parallel()
	for _, legacy := range []bool{true, false} {
		t.Run(map[bool]string{true: "legacy", false: "interrupted"}[legacy], func(t *testing.T) {
			t.Parallel()
			opts, inst := receiptFixture(t, `<Check id="ok" command="true" />`, false)
			if legacy {
				inst.ExecutionStarted = nil
				edition, err := runbook.LoadEdition(opts.CoreRoot, runbook.Entry{Slug: opts.Slug, Dir: "test/receipt"})
				require.NoError(t, err)
				inst.EditionHash = edition.Hash
			} else {
				*inst.ExecutionStarted = true
			}
			require.NoError(t, runbook.Save(opts.Cwd, inst))
			_, err := runbook.Apply(opts)
			require.NoError(t, err)
			_, err = runbook.Status(opts)
			require.Error(t, err)
			assert.Empty(t, receiptFields(t, opts)["commit"])
		})
	}
}

func TestExecutedReceiptInvalidatesHeadDriftBeforeResume(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="first" command="true" /><Command id="second" command="true" />`, false)
	_, err := runbook.Apply(opts)
	require.NoError(t, err)
	assert.Equal(t, opts.SHA, receiptFields(t, opts)["commit"])
	gitOk(t, opts.Cwd, "git", "commit", "--allow-empty", "-m", "changed source")
	opts.StepID = "second"
	_, err = runbook.StepComplete(opts)
	require.NoError(t, err)
	_, err = runbook.Apply(opts)
	require.NoError(t, err)
	assert.Empty(t, receiptFields(t, opts)["commit"])
	_, err = runbook.Status(opts)
	require.Error(t, err)
}

func TestExecutedReceiptRejectsIncompleteEvidence(t *testing.T) {
	t.Parallel()
	for _, changed := range []string{"missing-step", "skipped-step", "missing-signoff", "wrong-instance-repo", "wrong-origin", "dirty", "edition"} {
		t.Run(changed, func(t *testing.T) {
			t.Parallel()
			opts, _ := receiptFixture(t, `<Command id="signed" command="true" />`, false)
			opts.StepID = "signed"
			_, err := runbook.StepComplete(opts)
			require.NoError(t, err)
			inst, err := runbook.Apply(opts)
			require.NoError(t, err)
			_, err = runbook.Status(opts)
			require.NoError(t, err)
			switch changed {
			case "missing-step":
				inst.Steps = nil
			case "skipped-step":
				inst.Steps[0].Status = receiptSkippedStatus
			case "missing-signoff":
				inst.Steps[0].SignoffTier = ""
			case "wrong-instance-repo":
				inst.RepoSlug = "other"
			case "wrong-origin":
				gitOk(t, opts.Cwd, "git", "remote", "set-url", "origin", "https://github.com/other/lightwave-cli.git")
			case "dirty":
				writeFile(t, opts.Cwd, "README.md", "changed")
			case "edition":
				writeFile(t, opts.CoreRoot, "src/runbooks/test/receipt/runbook.mdx", `<Check id="another" command="true" />`)
			}
			require.NoError(t, runbook.Save(opts.Cwd, inst))
			_, err = runbook.Status(opts)
			require.Error(t, err)
		})
	}
}

func TestExecutionMarkerIsDurableBeforeTheStep(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="disk" path="disk.sh" />`, false)
	// The script observes the durable print while the step is executing.
	writeFile(t, opts.CoreRoot, "src/runbooks/test/receipt/disk.sh", "#!/bin/sh\nset -eu\ngrep -q 'execution_started: true' .tasks/325/runbooks/inst-1/instance.yaml\ngrep -q 'commit: "+opts.SHA+"' .tasks/325/runbooks/inst-1/instance.yaml\n")
	restartReceipt(t, opts)
	_, err := runbook.Apply(opts)
	require.NoError(t, err)
	_, err = runbook.Status(opts)
	require.NoError(t, err)
}

func TestExecutionPersistenceFailureRunsNoStep(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="side-effect" command="touch should-not-exist" />`, false)
	dir := runbook.Dir(opts.Cwd, opts.Task, opts.InstanceID)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { require.NoError(t, os.Chmod(dir, 0o700)) })
	_, err := runbook.Apply(opts)
	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(opts.Cwd, "should-not-exist"))
	assert.Equal(t, false, receiptFields(t, opts)["execution_started"])
}

func TestExecutedReceiptRejectsProseOnlyWork(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="manual" description="Manually verify QA" />`, false)
	_, err := runbook.Apply(opts)
	require.NoError(t, err)
	_, err = runbook.Status(opts)
	require.Error(t, err, "a description is not evidence that verification ran")
}

func TestExecutedReceiptBindsVerifierScriptBytes(t *testing.T) {
	t.Parallel()
	for _, timing := range []string{"before-apply", "after-apply"} {
		t.Run(timing, func(t *testing.T) {
			t.Parallel()
			opts, _ := receiptFixture(t, `<Check id="verify" path="verify.sh" />`, false)
			writeFile(t, opts.CoreRoot, "src/runbooks/test/receipt/verify.sh", "exit 0\n")
			// Start after the verifier exists so its original bytes are pinned.
			restartReceipt(t, opts)
			if timing == "after-apply" {
				_, err := runbook.Apply(opts)
				require.NoError(t, err)
			}
			writeFile(t, opts.CoreRoot, "src/runbooks/test/receipt/verify.sh", "exit 1\n")
			if timing == "before-apply" {
				_, err := runbook.Apply(opts)
				require.ErrorIs(t, err, runbook.ErrEditionMismatch)
			} else {
				_, err := runbook.Status(opts)
				require.ErrorIs(t, err, runbook.ErrEditionMismatch)
			}
		})
	}
}

func restartReceipt(t *testing.T, opts *runbook.ApplyOpts) {
	t.Helper()
	gitOk(t, opts.CoreRoot, "git", "add", ".")
	gitOk(t, opts.CoreRoot, "git", "commit", "-m", "publish asset")
	require.NoError(t, os.RemoveAll(runbook.Dir(opts.Cwd, opts.Task, opts.InstanceID)))
	_, err := runbook.Start(startOpts(opts.CoreRoot, opts.Cwd, opts.Slug))
	require.NoError(t, err)
}

func TestExecutedReceiptRejectsSharedVerifierSchemaDrift(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="ok" command="true" />`, false)
	_, err := runbook.Apply(opts)
	require.NoError(t, err)
	writeFile(t, opts.CoreRoot, "src/schemas/shared.yaml", "version: 2\n")
	_, err = runbook.Status(opts)
	require.ErrorIs(t, err, runbook.ErrEditionMismatch)
	writeFile(t, opts.CoreRoot, "src/schemas/shared.yaml", "version: 1\n")
	_, err = runbook.Status(opts)
	require.Error(t, err, "restoring the schema cannot erase observed invalidation")
}

func TestExecutedReceiptNeverBackfillsUnpublishedCore(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="ok" command="true" />`, false)
	writeFile(t, opts.CoreRoot, "src/schemas/shared.yaml", "version: 2\n")
	_, err := runbook.Start(startOpts(opts.CoreRoot, opts.Cwd, opts.Slug))
	require.NoError(t, err)
	assert.Empty(t, receiptFields(t, opts)["edition_commit"])
	writeFile(t, opts.CoreRoot, "src/schemas/shared.yaml", "version: 1\n")
	_, err = runbook.Apply(opts)
	require.NoError(t, err, "ordinary development execution remains available")
	_, err = runbook.Status(opts)
	require.Error(t, err)
	assert.Empty(t, receiptFields(t, opts)["commit"])
}

func TestExecutedReceiptBindsIgnoredRunbookHelpers(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="ok" command="true" />`, false)
	writeFile(t, opts.CoreRoot, ".gitignore", "helper.local\n")
	writeFile(t, opts.CoreRoot, "src/runbooks/test/receipt/helper.local", "original")
	restartReceipt(t, opts)
	_, err := runbook.Apply(opts)
	require.NoError(t, err)
	writeFile(t, opts.CoreRoot, "src/runbooks/test/receipt/helper.local", "changed")
	_, err = runbook.Status(opts)
	require.ErrorIs(t, err, runbook.ErrEditionMismatch)
}

func TestExecutedReceiptBindsReferencedAssetsOutsideRunbook(t *testing.T) {
	t.Parallel()
	opts, _ := receiptFixture(t, `<Check id="shared" path="../shared/verify.sh" />`, false)
	writeFile(t, opts.CoreRoot, ".gitignore", "verify.sh\n")
	writeFile(t, opts.CoreRoot, "src/runbooks/test/shared/verify.sh", "exit 0\n")
	restartReceipt(t, opts)
	_, err := runbook.Apply(opts)
	require.NoError(t, err)
	writeFile(t, opts.CoreRoot, "src/runbooks/test/shared/verify.sh", "exit 1\n")
	_, err = runbook.Status(opts)
	require.ErrorIs(t, err, runbook.ErrEditionMismatch)
}

func TestEditionRejectsEscapedOrSymlinkAssets(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"escape": "test/escape"}, map[string]string{
		"escape": `<Check id="escape" path="../../../../../outside.sh" />`,
	})
	cwd := initWorktree(t, "feature/receipt")
	_, err := runbook.Start(startOpts(core, cwd, "escape"))
	require.ErrorIs(t, err, runbook.ErrEditionMismatch)
	writeFile(t, core, "src/runbooks/test/escape/runbook.mdx", `<Check id="ok" command="true" />`)
	writeFile(t, core, "target.sh", "true\n")
	require.NoError(t, os.Symlink(filepath.Join(core, "target.sh"), filepath.Join(core, "src/runbooks/test/escape/link.sh")))
	_, err = runbook.Start(startOpts(core, cwd, "escape"))
	require.ErrorIs(t, err, runbook.ErrEditionMismatch)
}
