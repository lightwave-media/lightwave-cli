package runbook_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/runbook"
)

// These pin the step contract the lightwave-core catalog is written against
// (the Runbooks app's), measured from the catalog: <Inputs> filled by --var,
// {{ .inputs.X }} and $X, injected log_* helpers, RUNBOOK_OUTPUT feeding
// {{ .outputs.<step>.<key> }}, exit 2 = warn, RUNBOOK_DRY_RUN, \" escapes.

const (
	fence = "```"
	// nameInput is the required input every inputs fixture below declares.
	nameInput       = "Name"
	signoffOperator = "operator"
)

func inputsBlock(yaml string) string {
	return "<Inputs id=\"cfg\">\n" + fence + "yaml\n" + yaml + fence + "\n</Inputs>\n"
}

const nameInputs = `variables:
  - name: Name
    type: string
    validations:
      - required
  - name: Loud
    type: bool
    default: false
`

// startWith starts slug with vars in a fresh worktree and returns both.
func startWith(t *testing.T, core, slug string, vars map[string]string, dryRun bool) (string, *runbook.Instance) {
	t.Helper()

	cwd := initWorktree(t, "feature/546-"+slug)
	opts := startOpts(core, cwd, slug)
	opts.Vars = vars
	opts.DryRun = dryRun

	inst, err := runbook.Start(opts)
	require.NoError(t, err)

	return cwd, inst
}

func apply(core, cwd string, inst *runbook.Instance, auditPath string) (*runbook.Instance, error) {
	return runbook.Apply(&runbook.ApplyOpts{
		CoreRoot: core, Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID, AuditPath: auditPath,
	})
}

func TestApply_ScriptGetsInputsHelpersAndFeedsOutputsForward(t *testing.T) {
	t.Parallel()
	core := t.TempDir()

	mdx := inputsBlock(nameInputs) + `
<Check id="greet" title="greet" path="checks/greet.sh" inputsId="cfg" />
<Check id="use" title="use the output" command="touch {{ .outputs.greet.greeting }}" />
`
	writeCatalog(t, core, map[string]string{"greet": "test/greet"}, map[string]string{"greet": mdx})
	writeFile(t, core, "src/runbooks/test/greet/checks/greet.sh", `#!/usr/bin/env bash
set -euo pipefail
log_info "greeting {{ .inputs.Name }}"
touch "rendered-{{ .inputs.Name }}"
touch "env-$Name"
{{- if .inputs.Loud }}
touch loud
{{- end }}
echo "greeting=hi-$Name" >> "$RUNBOOK_OUTPUT"
`)
	cwd, inst := startWith(t, core, "greet", map[string]string{nameInput: "alice"}, false)

	got, err := apply(core, cwd, inst, "")
	require.NoError(t, err)
	require.Equal(t, runbook.StatusCompleted, got.Status)

	assert.FileExists(t, filepath.Join(cwd, "rendered-alice"), "{{ .inputs.Name }} is rendered into the script")
	assert.FileExists(t, filepath.Join(cwd, "env-alice"), "the input is also exported as $Name")
	assert.FileExists(t, filepath.Join(cwd, "hi-alice"), "a later step reads {{ .outputs.greet.greeting }}")
	assert.NoFileExists(t, filepath.Join(cwd, "loud"), "an unset bool default is false in {{- if }}")
	assert.Equal(t, map[string]string{"greeting": "hi-alice"}, got.Steps[0].Outputs)
}

func TestStart_RefusesInputsTheFormWouldRefuse(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"greet": "test/greet"}, map[string]string{
		"greet": inputsBlock(nameInputs) + `<Check id="noop" command="true" />`,
	})

	for name, tc := range map[string]struct {
		vars map[string]string
		want error
	}{
		"a typo is not silently dropped": {map[string]string{nameInput: "a", "Nme": "b"}, runbook.ErrUnknownInput},
		"a required input is required":   {map[string]string{}, runbook.ErrInvalidInput},
		"a bool must parse":              {map[string]string{nameInput: "a", "Loud": "maybe"}, runbook.ErrInvalidInput},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cwd := initWorktree(t, "feature/546-refuse")
			opts := startOpts(core, cwd, "greet")
			opts.Vars = tc.vars

			_, err := runbook.Start(opts)
			require.ErrorIs(t, err, tc.want)
			assert.NoDirExists(t, filepath.Join(cwd, ".tasks"), "a refused start writes no instance")
		})
	}
}

func TestApply_ScriptExitTwoWarnsAndCompletes(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"warn": "test/warn"}, map[string]string{
		"warn": `<Check id="soft" path="checks/soft.sh" />`,
	})
	writeFile(t, core, "src/runbooks/test/warn/checks/soft.sh", "log_warn 'degraded'\nexit 2\n")
	cwd, inst := startWith(t, core, "warn", nil, false)

	got, err := apply(core, cwd, inst, "")
	require.NoError(t, err, "exit 2 is a warning, not a failure")
	assert.Equal(t, runbook.StatusCompleted, got.Status)
	assert.True(t, got.Steps[0].Warned)

	evidence, err := os.ReadFile(filepath.Join(runbook.Dir(cwd, inst.TaskID, inst.InstanceID), "evidence.md"))
	require.NoError(t, err)
	assert.Contains(t, string(evidence), "soft (check): completed (warned)")
}

// Decision of record: a dry-run runs only scripts that read RUNBOOK_DRY_RUN,
// so nothing that ignores the flag can do real work.
func TestApply_DryRunRunsOnlyDryRunAwareScripts(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"dry": "test/dry"}, map[string]string{
		"dry": `<Check id="aware" path="checks/aware.sh" />
<Check id="unaware" path="checks/unaware.sh" />
<Check id="inline" command="touch inline-ran" />`,
	})
	writeFile(t, core, "src/runbooks/test/dry/checks/aware.sh",
		`if [ "${RUNBOOK_DRY_RUN:-false}" = "true" ]; then touch dry-seen; else touch real-run; fi`+"\n")
	writeFile(t, core, "src/runbooks/test/dry/checks/unaware.sh", "touch should-not-run\n")
	cwd, inst := startWith(t, core, "dry", nil, true)

	got, err := apply(core, cwd, inst, "")
	require.NoError(t, err)
	assert.Equal(t, runbook.StatusCompleted, got.Status)

	assert.FileExists(t, filepath.Join(cwd, "dry-seen"), "a dry-run-aware script runs, told it is a dry run")
	assert.NoFileExists(t, filepath.Join(cwd, "real-run"))
	assert.NoFileExists(t, filepath.Join(cwd, "should-not-run"), "a script that ignores the flag never runs")
	assert.NoFileExists(t, filepath.Join(cwd, "inline-ran"))
	assert.Equal(t, []string{"completed", "skipped", "skipped"},
		[]string{got.Steps[0].Status, got.Steps[1].Status, got.Steps[2].Status})
}

// An inline command runs as argv with each word rendered on its own, so an
// input value is one argument and can never become shell syntax.
func TestApply_InlineInputIsOneArgumentNeverSyntax(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"argv": "test/argv"}, map[string]string{
		"argv": inputsBlock(nameInputs) + `<Check id="make" command="touch {{ .inputs.Name }}" />`,
	})
	cwd, inst := startWith(t, core, "argv", map[string]string{nameInput: "x; touch pwned"}, false)

	got, err := apply(core, cwd, inst, "")
	require.NoError(t, err)
	assert.Equal(t, runbook.StatusCompleted, got.Status)
	assert.FileExists(t, filepath.Join(cwd, "x; touch pwned"), "the whole value is one file name")
	assert.NoFileExists(t, filepath.Join(cwd, "pwned"), "the value was never parsed as a second command")
}

// Decision of record: no shell unless signed off, Checks included (#348).
func TestApply_CheckThatNeedsAShellWaitsForSignoff(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"shell": "test/shell"}, map[string]string{
		"shell": `<Check id="both" command="touch a && touch b" />`,
	})
	cwd, inst := startWith(t, core, "shell", nil, false)

	got, err := apply(core, cwd, inst, "")
	require.NoError(t, err)
	require.Equal(t, runbook.StatusWaitingApproval, got.Status)
	assert.NoFileExists(t, filepath.Join(cwd, "a"), "nothing runs under a shell before sign-off")

	_, err = runbook.StepComplete(&runbook.ApplyOpts{
		Cwd: cwd, Task: inst.TaskID, InstanceID: inst.InstanceID, StepID: "both", SignoffTier: signoffOperator,
	})
	require.NoError(t, err)

	got, err = apply(core, cwd, inst, "")
	require.NoError(t, err)
	assert.Equal(t, runbook.StatusCompleted, got.Status)
	assert.FileExists(t, filepath.Join(cwd, "a"))
	assert.FileExists(t, filepath.Join(cwd, "b"))
}

func TestParseSteps_HighBlastMarksChecksThatNeedAShell(t *testing.T) {
	t.Parallel()

	for command, wantSignoff := range map[string]bool{
		`go version`: false,
		`lw harness status --instance {{ .inputs.X }}`:    false,
		`echo {{ .inputs.X | lower }}`:                    false,
		`python3 -c 'print(1)'`:                           false,
		`git branch --show-current && git status --short`: true,
		`python3 -c 'print(1)' | tee out`:                 true,
		`test -f ~/x`:                                     true,
		`echo \"$HOME\"`:                                  true,
		`ls *.go`:                                         true,
		// An interpreter runs its argument as code, shell syntax or not.
		`bash -c 'touch {{ .inputs.Name }}'`: true,
		`env FOO=1 make`:                     true,
		`psql -d db -t -c \"SELECT 1 FROM t WHERE s='{{ .inputs.T }}'\"`: true,
	} {
		steps := runbook.ParseSteps(`<Check id="c" command="` + command + `" />`)
		require.Len(t, steps, 1, command)
		assert.Equal(t, wantSignoff, steps[0].HighBlast, command)
	}

	commands := runbook.ParseSteps(`<Command id="c" command="true" />`)
	assert.True(t, commands[0].HighBlast, "every Command step waits for sign-off")
}

// The catalog escapes quotes inside attributes; the old parser cut such a
// command off at the first \".
func TestApply_EscapedQuotesAndExpected(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		expected string
		wantErr  bool
	}{
		"expected word present":        {"good", false},
		"expected word absent fails":   {"bad", true},
		"a word inside another is not": {"goo", true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			core := t.TempDir()
			writeCatalog(t, core, map[string]string{"say": "test/say"}, map[string]string{
				"say": `<Check id="say" command="printf \"%s\" \"all good\"" expected="` + tc.expected + `" />`,
			})
			cwd, inst := startWith(t, core, "say", nil, false)

			got, err := apply(core, cwd, inst, "")
			if tc.wantErr {
				require.ErrorIs(t, err, runbook.ErrCheckFailed)
				assert.Equal(t, runbook.StatusFailed, got.Status)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, "all good", got.Steps[0].Output)
		})
	}
}

func TestApply_TimeoutFailsTheStep(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"slow": "test/slow"}, map[string]string{
		"slow": `<Check id="slow" command="sleep 5" timeoutMs={200} />`,
	})
	cwd, inst := startWith(t, core, "slow", nil, false)

	began := time.Now()
	_, err := apply(core, cwd, inst, "")
	require.ErrorIs(t, err, runbook.ErrCheckFailed)
	assert.Contains(t, err.Error(), "timed out")
	assert.Less(t, time.Since(began), 3*time.Second)
}

func TestApply_AuditsEveryStepDecision(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"audited": "test/audited"}, map[string]string{
		"audited": `<Check id="look" command="true" />
<Command id="act" command="true" />`,
	})
	cwd, inst := startWith(t, core, "audited", nil, false)
	auditPath := filepath.Join(t.TempDir(), "shell.jsonl")

	_, err := apply(core, cwd, inst, auditPath)
	require.NoError(t, err)

	f, err := os.Open(auditPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	var rows []map[string]any

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &row))
		rows = append(rows, row)
	}

	require.Len(t, rows, 2, "one row for the check that ran, one for the command that paused")
	assert.Equal(t, "runbook", rows[0]["surface"])
	assert.Equal(t, "allow", rows[0]["decision"])
	assert.Equal(t, "ok", rows[0]["outcome"])
	assert.Equal(t, inst.InstanceID, rows[0]["correlation_id"])
	assert.Equal(t, "argv", rows[0]["metadata"].(map[string]any)["mode"])
	assert.Equal(t, "pause", rows[1]["decision"])
}

// evidence.md is what gets posted to the task's GitHub issue, so it carries
// status lines only; output stays in the 0600 instance print (#546).
func TestApply_EvidenceCarriesNoStepOutput(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"loud": "test/loud"}, map[string]string{
		"loud": `<Check id="talk" command="echo token-abc123" />`,
	})
	cwd, inst := startWith(t, core, "loud", nil, false)

	got, err := apply(core, cwd, inst, "")
	require.NoError(t, err)
	assert.Equal(t, "token-abc123", got.Steps[0].Output, "the instance print keeps the output")

	dir := runbook.Dir(cwd, inst.TaskID, inst.InstanceID)
	evidence, err := os.ReadFile(filepath.Join(dir, "evidence.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(evidence), "abc123")

	for _, name := range []string{"evidence.md", "instance.yaml"} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), name)
	}
}

// Instance ids are random UUIDs; sorting them does not find the latest one.
func TestResolveInstanceID_PicksTheLatestCreated(t *testing.T) {
	t.Parallel()
	core := t.TempDir()
	writeCatalog(t, core, map[string]string{"latest": "test/latest"}, map[string]string{
		"latest": `<Check id="ok" command="true" />`,
	})
	cwd := initWorktree(t, "feature/546-latest")

	for id, created := range map[string]string{"zzz-older": "2026-01-01T00:00:00Z", "aaa-newer": "2026-01-02T00:00:00Z"} {
		opts := startOpts(core, cwd, "latest")
		opts.InstanceID = id
		inst, err := runbook.Start(opts)
		require.NoError(t, err)

		inst.CreatedAt = created
		require.NoError(t, runbook.Save(cwd, inst))
	}

	id, err := runbook.ResolveInstanceID(cwd, "325", "")
	require.NoError(t, err)
	assert.Equal(t, "aaa-newer", id)
}

func TestParseVars_FileThenFlagsAndBadPairsRefused(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "vars.yaml")
	require.NoError(t, os.WriteFile(file, []byte("Name: from-file\nCount: 3\n"), 0o600))

	vars, err := runbook.ParseVars([]string{"Name=from-flag", "Url=https://x/y?a=1,b=2"}, file)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{nameInput: "from-flag", "Count": "3", "Url": "https://x/y?a=1,b=2"}, vars)

	_, err = runbook.ParseVars([]string{"no-equals-sign"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Key=Value")
}
