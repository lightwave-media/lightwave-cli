package cron_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/cron"
	"github.com/lightwave-media/lightwave-cli/internal/testutil/gitfixture"
)

// TestMain isolates the process: LoadJobs shells out to git with the process
// environment, and under a hook's GIT_DIR it would read the hook's repository
// instead of the fixture.
func TestMain(m *testing.M) {
	gitfixture.Isolate()
	os.Exit(m.Run())
}

func TestIntervalsTranslatesCronToLaunchdCalendarEntries(t *testing.T) {
	t.Parallel()

	every30, err := cron.Intervals("*/30 * * * *")
	require.NoError(t, err)
	require.Len(t, every30, 2)
	assert.Equal(t, 0, *every30[0].Minute)
	assert.Equal(t, 30, *every30[1].Minute)
	assert.Nil(t, every30[0].Hour, "an unrestricted field is left out, as launchd reads a wildcard")

	daily, err := cron.Intervals("@daily")
	require.NoError(t, err)
	require.Len(t, daily, 1)
	assert.Equal(t, 0, *daily[0].Hour)

	weekdays, err := cron.Intervals("0 8 * * 1-5")
	require.NoError(t, err)
	assert.Len(t, weekdays, 5)
}

func TestIntervalsRefusesWhatLaunchdCannotExpressExactly(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		"0 8 1 * 1",     // cron ORs day-of-month and day-of-week; one launchd dict ANDs them
		"* * * * *",     // fine for cron, but sixty entries per hour cross-producted with nothing: allowed? no -
		"61 * * * *",    // out of range
		"0 8 L * *",     // unsupported syntax
		"*/1 */1 * * *", // 1440 entries
		"0 8 * *",       // four fields
	} {
		if bad == "* * * * *" {
			// Every minute is one wildcard dict, which launchd reads as every
			// minute: legal, so not a refusal. Kept in the table to say so.
			_, err := cron.Intervals(bad)
			require.NoError(t, err, bad)

			continue
		}

		_, err := cron.Intervals(bad)
		require.Error(t, err, bad)
	}
}

func TestLastFireIsTheScheduledSlotNotTheMomentOfTheTick(t *testing.T) {
	t.Parallel()

	// A launchd retry at 08:07, or a manual run, belongs to the 08:00 window.
	now := time.Date(2026, 10, 1, 8, 7, 42, 0, time.UTC)
	fired, err := cron.LastFire("0 */4 * * *", now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC), fired)

	exact, err := cron.LastFire("*/30 * * * *", time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, 30, exact.Minute())
}

func okJob() cron.Job {
	return cron.Job{
		ID:               "nightly_audit",
		Title:            "Nightly audit",
		Schedule:         "0 3 * * *",
		Persona:          "v_scrum-manager",
		DaemonSecretsRef: "cron-nightly_audit",
		// Two names, unsorted: the goldens pin them as ONE sorted, comma-joined
		// --only argument. Space-separated, the second name would become the
		// command lw config exec runs.
		SecretNames: []string{"NULLTICKETS_API_TOKEN", "GITHUB_PACKAGES_READ_TOKEN"},
		Dispatch:    cron.Dispatch{Kind: cron.DispatchAgentSession, Target: "v_scrum-manager", PromptTemplate: "Triage the backlog."},
		Enabled:     true,
	}
}

var records = map[string]cron.Record{
	"cron-nightly_audit": {ID: "cron-nightly_audit", Names: []string{"NULLTICKETS_API_TOKEN", "GITHUB_PACKAGES_READ_TOKEN"}},
}

func TestRefusalAcceptsAJobWithPersonaRecordAndEntitledNames(t *testing.T) {
	t.Parallel()

	job := okJob()
	assert.Empty(t, cron.Refusal(&job, records))
}

func TestRefusalNamesWhatIsMissing(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		mutate func(*cron.Job)
		want   string
	}{
		"no persona":             {func(j *cron.Job) { j.Persona = "" }, "no persona"},
		"no record ref":          {func(j *cron.Job) { j.DaemonSecretsRef = "" }, "no daemon_secrets_ref"},
		"unknown record":         {func(j *cron.Job) { j.DaemonSecretsRef = "cron-other" }, "has no consumer record"},
		"unentitled secret":      {func(j *cron.Job) { j.SecretNames = []string{"OPENROUTER_API_KEY"} }, "not in record"},
		"a value in a name":      {func(j *cron.Job) { j.SecretNames = []string{"sk-live-abc"} }, "not an UPPER_SNAKE key name"},
		"no dispatch":            {func(j *cron.Job) { j.Dispatch = cron.Dispatch{} }, "no dispatch"},
		"unrun kind":             {func(j *cron.Job) { j.Dispatch.Kind = "blueprint" }, "not one lw cron run executes"},
		"session for another":    {func(j *cron.Job) { j.Dispatch.Target = "v_cto" }, "must equal persona"},
		"inexpressible schedule": {func(j *cron.Job) { j.Schedule = "0 8 1 * 1" }, "day-of-week"},
		"pasted token":           {func(j *cron.Job) { j.Dispatch.PromptTemplate = "use ghp_" + strings.Repeat("a", 36) }, "dispatch.prompt_template holds a secret-shaped value"},
		"pasted key in an arg": {func(j *cron.Job) {
			j.Dispatch.Args = map[string]any{"key": "AKIA" + strings.Repeat("B", 16)}
		}, "dispatch.args.key"},
	}

	for name, c := range cases {
		job := okJob()
		c.mutate(&job)
		assert.Contains(t, cron.Refusal(&job, records), c.want, name)
	}
}

func TestRefusalNeverEchoesTheValueItRefused(t *testing.T) {
	t.Parallel()

	job := okJob()
	job.SecretNames = []string{"sk-live-supersecret"}
	assert.NotContains(t, cron.Refusal(&job, records), "supersecret")
}

var opts = cron.RenderOptions{LwPath: "/home/u/.local/bin/lw", LogDir: "/home/u/.lightwave/observability/launchd", Path: "/home/u/.local/bin:/usr/bin:/bin"}

func TestRenderRunsTheJobUnderItsEntitledNamesOnly(t *testing.T) {
	t.Parallel()

	job := okJob()
	agent, err := cron.Render(&job, opts)
	require.NoError(t, err)
	assert.Equal(t, "com.lightwave.cron.nightly_audit", agent.Label)
	assert.Equal(t, []string{
		opts.LwPath, "config", "exec", "--only", "GITHUB_PACKAGES_READ_TOKEN,NULLTICKETS_API_TOKEN", "--",
		opts.LwPath, "cron", "run", "nightly_audit",
	}, agent.ProgramArguments)
	assert.Equal(t, "v_scrum-manager", agent.Environment["LW_AGENT_ID"])

	job.SecretNames = nil
	bare, err := cron.Render(&job, opts)
	require.NoError(t, err)
	assert.Equal(t, []string{opts.LwPath, "cron", "run", "nightly_audit"}, bare.ProgramArguments,
		"a job with no secrets runs without lw config exec")
}

// The two encoders escape for their own format. Every field comes from a
// stamp, so a command or prompt with markup in it must not break or inject.
func TestEncodersEscapeWhatTheirFormatInterprets(t *testing.T) {
	t.Parallel()

	agent := cron.LaunchAgent{
		Label:            "com.lightwave.cron.x",
		ProgramArguments: []string{"/bin/echo", `a<b>&"c"`, "${HOME}", `back\slash`},
		Environment:      map[string]string{"PATH": "/usr/bin"},
		Intervals:        []cron.Interval{{}},
	}

	plist := string(cron.EncodePlist(&agent))
	assert.Contains(t, plist, "a&lt;b&gt;&amp;&#34;c&#34;")
	assert.NotContains(t, plist, "a<b>")

	nix := string(cron.EncodeNix([]cron.LaunchAgent{agent}, "test"))
	assert.Contains(t, nix, `"\${HOME}"`, "nix would interpolate an unescaped ${")
	assert.Contains(t, nix, `"a<b>&\"c\""`)
	assert.Contains(t, nix, `"back\\slash"`)
}

func TestPlistPassesPlutilLint(t *testing.T) {
	t.Parallel()

	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("plutil is macOS-only; the golden file pins the format elsewhere")
	}

	job := okJob()
	job.Schedule = "*/30 9-17 * * 1-5"
	agent, err := cron.Render(&job, opts)
	require.NoError(t, err)

	file := filepath.Join(t.TempDir(), agent.Label+".plist")
	require.NoError(t, os.WriteFile(file, cron.EncodePlist(&agent), 0o600))

	out, err := exec.CommandContext(t.Context(), plutil, "-lint", file).CombinedOutput() //nolint:gosec // test temp file
	require.NoError(t, err, string(out))
}

func TestPlistGolden(t *testing.T) {
	t.Parallel()

	job := okJob()
	agent, err := cron.Render(&job, opts)
	require.NoError(t, err)

	want, err := os.ReadFile(filepath.Join("testdata", "nightly_audit.plist"))
	require.NoError(t, err)
	assert.Equal(t, string(want), string(cron.EncodePlist(&agent)))
}

func TestNixGolden(t *testing.T) {
	t.Parallel()

	job := okJob()
	agent, err := cron.Render(&job, opts)
	require.NoError(t, err)

	want, err := os.ReadFile(filepath.Join("testdata", "nightly_audit.nix"))
	require.NoError(t, err)
	assert.Equal(t, string(want), string(cron.EncodeNix([]cron.LaunchAgent{agent}, "lightwave-core origin/main:src/schemas/workflows/scheduled_jobs")))
}

func TestReconcileMatchesARenderedJobByItsStampedLabel(t *testing.T) {
	t.Parallel()

	runs, exit := 12, 0
	report := cron.Reconcile(
		[]cron.Job{{ID: "nightly_audit", Command: "lw scrum sync --json"}},
		[]cron.Agent{{Label: "com.lightwave.cron.nightly_audit", Program: "lw config exec --only X -- lw cron run nightly_audit", Loaded: true, Runs: &runs, LastExit: &exit}},
		nil,
	)
	require.Len(t, report.Rows, 1)
	assert.Equal(t, cron.VerdictScheduled, report.Rows[0].Verdict)
	assert.Equal(t, 12, *report.Rows[0].Runs)
}

func TestLoadJobsReadsBothShapesFromOriginMainNotTheWorkingTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	core := filepath.Join(root, "lightwave-core")
	dir := filepath.Join(core, "src", "schemas", cron.JobsFamily)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}

	write("old.yaml", "id: old\nschedule: \"0 * * * *\"\nhandler:\n  transport: cli\n  command: lw scrum sync --json\n")
	write("new.yaml", `id: new
name: New job
schedule: "0 3 * * *"
persona: v_scrum-manager
daemon_secrets_ref: cron-new
secret_names: [NULLTICKETS_API_TOKEN]
enabled: false
dispatch:
  kind: shell_command
  target: lw knowledge sync --json
`)
	gitfixture.CommitAsOriginMain(t, core)

	// Another session's uncommitted edit in the shared checkout is not a fact.
	write("new.yaml", "id: new\nschedule: \"@hourly\"\n")
	write("untracked.yaml", "id: untracked\n")

	jobs, err := cron.LoadJobs(t.Context(), root)
	require.NoError(t, err)
	require.Len(t, jobs, 2)

	assert.Equal(t, "new", jobs[0].ID)
	assert.Equal(t, "0 3 * * *", jobs[0].Schedule, "read at origin/main, not the edited working tree")
	assert.Equal(t, "New job", jobs[0].Title)
	assert.Equal(t, "lw knowledge sync --json", jobs[0].Command, "a shell_command target is the command reconcile matches")
	assert.False(t, jobs[0].Enabled)
	assert.Equal(t, []string{"NULLTICKETS_API_TOKEN"}, jobs[0].SecretNames)

	assert.Equal(t, "old", jobs[1].ID)
	assert.True(t, jobs[1].Enabled, "absent enabled means enabled")
}
