package cron_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/cron"
)

func TestHandlerVerb(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"lw knowledge sync --json": "knowledge",
		"lw project sync":          "project",
		"lw cron list":             "cron",
		// Not an lw command — a job may legitimately run a script, and
		// reporting that as a missing verb would be a false accusation.
		"/bin/bash dev/gh-notif-signal.sh": "",
		"python3 -m something":             "",
		// Degenerate shapes must not produce a verb.
		"lw":           "",
		"lw --help":    "",
		"":             "",
		"   lw   epic": "epic",
	}

	for command, want := range cases {
		command, want := command, want
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, want, cron.HandlerVerb(command))
		})
	}
}

// TestReconcileFindsTheDeclaredButUnscheduledJob is the case this verb exists
// for: the stamp declares a job, nothing on the machine runs it, and both sides
// look healthy in isolation.
func TestReconcileFindsTheDeclaredButUnscheduledJob(t *testing.T) {
	t.Parallel()

	jobs := []cron.Job{{
		ID:       "notion_knowledge_hygiene",
		Schedule: "*/30 * * * *",
		Command:  "lw knowledge sync --json",
	}}
	agents := []cron.Agent{{
		Label:   "com.lightwave.pr-review",
		Program: "/bin/bash /Users/x/dev/lightwave-cli/dev/pr-review.sh",
		Loaded:  true,
	}}

	report := cron.Reconcile(jobs, agents, map[string]bool{"epic": true})

	require.Len(t, report.Rows, 2, "one declared job, one undeclared agent")

	assert.Equal(t, 1, report.Counts[cron.VerdictDeclaredNotScheduled])
	assert.Equal(t, 1, report.Counts[cron.VerdictAgentNotDeclared])
	assert.Equal(t, 0, report.Counts[cron.VerdictScheduled])

	assert.Equal(t, 1, report.UnrunnableJobs,
		"`lw knowledge` is not in the known-verb set, so the job cannot run")
}

// TestReconcileMatchesOnCommandNotLabel pins the design decision.
//
// No stamp declares how a job id maps to a launchd Label, so matching on a
// label convention would mean inventing one here. Matching on the declared
// command needs no convention and survives any renaming of either side.
func TestReconcileMatchesOnCommandNotLabel(t *testing.T) {
	t.Parallel()

	jobs := []cron.Job{{
		ID:       "project_board_hygiene",
		Schedule: "*/30 * * * *",
		Command:  "lw project sync",
	}}
	// The label bears no resemblance to the job id on purpose.
	agents := []cron.Agent{{
		Label:   "com.lightwave.totally-unrelated-name",
		Program: "/bin/sh -c lw project sync --json",
		Loaded:  true,
	}}

	report := cron.Reconcile(jobs, agents, map[string]bool{"project": true})

	require.Len(t, report.Rows, 1, "the agent matched the job, so it is not also reported as extra")
	assert.Equal(t, cron.VerdictScheduled, report.Rows[0].Verdict)
	assert.Equal(t, "com.lightwave.totally-unrelated-name", report.Rows[0].Label)
	assert.True(t, report.Rows[0].Loaded)
	assert.Equal(t, 0, report.UnrunnableJobs)
}

// TestReconcileReportsAScheduledJobWhoseVerbIsMissing is the worst case and the
// one a label-only check would miss entirely.
//
// launchd runs it, launchd reports success, and the command does nothing —
// which reads as a healthy machine.
func TestReconcileReportsAScheduledJobWhoseVerbIsMissing(t *testing.T) {
	t.Parallel()

	jobs := []cron.Job{{
		ID:      "ghost",
		Command: "lw nosuchverb run",
	}}
	agents := []cron.Agent{{
		Label:   "com.lightwave.ghost",
		Program: "lw nosuchverb run",
		Loaded:  true,
	}}

	report := cron.Reconcile(jobs, agents, map[string]bool{"epic": true})

	require.Len(t, report.Rows, 1)
	assert.Equal(t, cron.VerdictScheduled, report.Rows[0].Verdict,
		"it IS scheduled — that is what makes it dangerous")
	assert.Equal(t, "nosuchverb", report.Rows[0].VerbMissing)
	assert.Equal(t, 1, report.UnrunnableJobs)
}

// TestReconcileWithNilVerbsSkipsTheVerbCheck keeps the check optional, so a
// caller with no view of the command surface gets the scheduling half rather
// than a wrong accusation.
func TestReconcileWithNilVerbsSkipsTheVerbCheck(t *testing.T) {
	t.Parallel()

	jobs := []cron.Job{{ID: "j", Command: "lw anything at all"}}

	report := cron.Reconcile(jobs, nil, nil)

	assert.Equal(t, 0, report.UnrunnableJobs)
	assert.Empty(t, report.Rows[0].VerbMissing)
}

func TestReconcileIsDeterministic(t *testing.T) {
	t.Parallel()

	jobs := []cron.Job{
		{ID: "zulu", Command: "lw z"},
		{ID: "alpha", Command: "lw a"},
	}
	agents := []cron.Agent{
		{Label: "com.lightwave.z"},
		{Label: "com.lightwave.a"},
	}

	first := cron.Reconcile(jobs, agents, nil)
	second := cron.Reconcile(jobs, agents, nil)

	assert.Equal(t, first.Rows, second.Rows, "row order must not depend on map iteration")

	// Most-actionable-first: the declared-but-unscheduled jobs lead, then the
	// undeclared agents. Sorting on the raw verdict string would invert this,
	// because "agent-not-declared" sorts before "declared-not-scheduled".
	assert.Equal(t, "alpha", first.Rows[0].JobID)
	assert.Equal(t, "zulu", first.Rows[1].JobID)
	assert.Equal(t, cron.VerdictAgentNotDeclared, first.Rows[2].Verdict)
}

func TestLoadJobsSkipsTheShapeAndTheIndex(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "lightwave-core", "src", "schemas", cron.JobsFamily)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}

	write("notion_knowledge_hygiene.yaml", `
id: notion_knowledge_hygiene
title: "Notion knowledge mirror hygiene"
schedule: "*/30 * * * *"
handler:
  transport: cli
  command: "lw knowledge sync --json"
`)
	// The SHAPE. Reading it as an instance would report a job whose id is the
	// literal string "id" on a schedule of "str".
	write("cron_job.yaml", `
_meta:
  title: "Cron Job Definition"
required_fields:
  - name: id
    type: str
`)
	write("__index.yaml", "entries: []\n")

	jobs, err := cron.LoadJobs(root)
	require.NoError(t, err)

	require.Len(t, jobs, 1, "only the instance is a job")
	assert.Equal(t, "notion_knowledge_hygiene", jobs[0].ID)
	assert.Equal(t, "*/30 * * * *", jobs[0].Schedule)
	assert.Equal(t, "lw knowledge sync --json", jobs[0].Command)
	assert.Equal(t, "cli", jobs[0].Transport)
}

func TestLoadJobsRefusesAMissingFamily(t *testing.T) {
	t.Parallel()

	// Silence is the failure mode this whole verb exists to prevent, so an
	// absent stamp must be an error rather than an empty, healthy-looking list.
	_, err := cron.LoadJobs(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no scheduled-job declarations at")
}

func TestLoadAgentsToleratesNoLaunchAgentsDir(t *testing.T) {
	t.Parallel()

	agents, err := cron.LoadAgents(t.Context(), t.TempDir())
	require.NoError(t, err, "a machine with no LaunchAgents is legal, not broken")
	assert.Empty(t, agents)
}

func TestLoadAgentsReadsLabelAndProgram(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dir := filepath.Join(home, "Library", "LaunchAgents")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.lightwave.gh-notif-signal</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>/Users/x/dev/lightwave-cli/dev/gh-notif-signal.sh</string>
  </array>
  <key>StandardOutPath</key>
  <string>/Users/x/.lightwave/observability/launchd/out.log</string>
</dict>
</plist>`

	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "com.lightwave.gh-notif-signal.plist"), []byte(plist), 0o644))
	// A non-lightwave agent must be ignored entirely.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "com.example.other.plist"), []byte(plist), 0o644))

	agents, err := cron.LoadAgents(t.Context(), home)
	require.NoError(t, err)

	require.Len(t, agents, 1, "only com.lightwave.* agents are this estate's")
	assert.Equal(t, "com.lightwave.gh-notif-signal", agents[0].Label)
	assert.Contains(t, agents[0].Program, "gh-notif-signal.sh")

	// StandardOutPath is a <string> too. If the parser tracked raw text rather
	// than the enclosing element it would swallow that path into the program
	// arguments and the containment match would start firing on log paths.
	assert.NotContains(t, agents[0].Program, "out.log")
}
