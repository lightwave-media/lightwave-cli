package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/cron"
	"github.com/lightwave-media/lightwave-cli/internal/testutil"
)

// cronWorkspace points both halves of the reconciliation at throwaway trees:
// the stamp at a temp LW_LIGHTWAVE_ROOT, and the launchd fleet at a temp HOME.
//
// Pinning HOME is what makes these tests say anything. Without it the handler
// reads this machine's real ~/Library/LaunchAgents, so the assertions would
// pass or fail on whatever the operator happens to have installed — a test
// whose subject is the developer's laptop.
func cronWorkspace(t *testing.T) (jobsDir, agentsDir string) {
	t.Helper()

	workspace := t.TempDir()
	jobsDir = filepath.Join(workspace, "lightwave-core", "src", "schemas", cron.JobsFamily)
	require.NoError(t, os.MkdirAll(jobsDir, 0o755))

	home := t.TempDir()
	agentsDir = filepath.Join(home, "Library", "LaunchAgents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o755))

	t.Setenv("HOME", home)
	t.Setenv("LW_LIGHTWAVE_ROOT", workspace)
	config.Reset()
	t.Cleanup(config.Reset)

	return jobsDir, agentsDir
}

func writeJob(t *testing.T, dir, name, body string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestCronListNamesTheDeclaredJobNothingSchedules(t *testing.T) {
	jobsDir, _ := cronWorkspace(t)

	writeJob(t, jobsDir, "project_board_hygiene.yaml", `
id: project_board_hygiene
title: "Project board hygiene"
schedule: "*/30 * * * *"
handler:
  transport: cli
  command: "lw project sync"
`)

	out, err := testutil.RunHandler(t, "cron.list", nil, nil)
	require.NoError(t, err)

	assert.Contains(t, out, "project_board_hygiene")
	assert.Contains(t, out, "declared, nothing schedules it")
	assert.Contains(t, out, "lw cron sync",
		"the report names the verb that would fix it, and that it is not built")
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestCronListJSONIsMachineReadable(t *testing.T) {
	jobsDir, agentsDir := cronWorkspace(t)

	writeJob(t, jobsDir, "gh_notif_feedback_loop.yaml", `
id: gh_notif_feedback_loop
schedule: "*/15 * * * *"
handler:
  transport: cli
  command: "/bin/bash dev/gh-notif-signal.sh"
`)
	require.NoError(t, os.WriteFile(
		filepath.Join(agentsDir, "com.lightwave.gh-notif-signal.plist"), []byte(`<?xml version="1.0"?>
<plist version="1.0"><dict>
  <key>Label</key><string>com.lightwave.gh-notif-signal</string>
  <key>ProgramArguments</key><array>
    <string>/bin/bash</string><string>dev/gh-notif-signal.sh</string>
  </array>
</dict></plist>`), 0o644))

	out, err := testutil.RunHandler(t, "cron.list", nil, map[string]any{"json": true})
	require.NoError(t, err)

	var report cron.Report
	require.NoError(t, json.Unmarshal([]byte(out), &report), "--json emits only JSON")

	require.Len(t, report.Rows, 1, "the agent runs the declared command, so it is one row not two")
	assert.Equal(t, cron.VerdictScheduled, report.Rows[0].Verdict)
	assert.Equal(t, "com.lightwave.gh-notif-signal", report.Rows[0].Label)
	assert.Equal(t, 0, report.UnrunnableJobs,
		"a shell command is not an lw verb, so it is never accused of being missing")
}

// TestCronListFlagsAScheduledJobWhoseVerbIsMissing is the finding the verb was
// built for: launchd runs it, launchd reports success, the command does nothing.
//
//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestCronListFlagsAScheduledJobWhoseVerbIsMissing(t *testing.T) {
	jobsDir, _ := cronWorkspace(t)

	writeJob(t, jobsDir, "notion_knowledge_hygiene.yaml", `
id: notion_knowledge_hygiene
schedule: "*/30 * * * *"
handler:
  transport: cli
  command: "lw nosuchverb sync --json"
`)

	out, err := testutil.RunHandler(t, "cron.list", nil, nil)
	require.NoError(t, err)

	assert.Contains(t, out, "does not expose")
	assert.Contains(t, out, "nosuchverb")
	assert.Contains(t, out, "they cannot have run")
}

// TestCronListRefusesAWorkspaceWithNoDeclarations pins the rejection path.
//
// Reporting "0 jobs, all clean" when the stamp cannot be found is the exact
// defect class this verb exists to catch — a check whose subject does not exist
// reads as a healthy machine. So an absent family must be an error.
//
//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestCronListRefusesAWorkspaceWithNoDeclarations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LW_LIGHTWAVE_ROOT", t.TempDir())
	config.Reset()
	t.Cleanup(config.Reset)

	_, err := testutil.RunHandler(t, "cron.list", nil, nil)
	require.Error(t, err, "silence is the failure mode; an unreadable stamp must say so")
	assert.Contains(t, err.Error(), "no scheduled-job declarations at")
}
