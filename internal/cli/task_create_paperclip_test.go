//nolint:testpackage // mutates the unexported task-create flag globals
package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #351 listed four faults in `lw task create`'s Paperclip leg. These pin the
// three that are behavioural; the fourth (--skip-paperclip being `unknown flag`
// through the dispatcher) is gone with the flag itself.

func resetPaperclipOnlyFlags(t *testing.T) {
	t.Helper()

	taskCreateTitle = "probe"
	taskCreateDryRun = false
	taskCreateAssign = ""
	taskCreateLabels = nil
	taskCreatePRD = ""
	taskCreatePlan = ""
	taskCreateDocs = nil
	taskCreateAttach = nil
	taskCreateParent = ""
	taskCreateProject = ""
	taskCreateProjectWS = ""
	taskCreateBlockedBy = nil
	taskCreateBlocks = nil
	taskCreateBillingCode = ""
}

// TestPaperclipOnlyFlagsAreRejected is fault 4 of the issue: ~13 of the 23 flags
// were inert. The block consuming them sat inside `else if companyID != ""`,
// and companyID was set only when --assign resolved against Paperclip — so with
// no --assign they were silently ignored, and with --assign the command errored
// before reaching them. There was no path on which they worked.
//
// Silently accepting a flag that does nothing is the worst of the options.
// Deleting them quietly would be the second worst, because it makes a product
// decision by omission.
//
//nolint:paralleltest // mutates the package-level flag globals
func TestPaperclipOnlyFlagsAreRejected(t *testing.T) {
	cases := map[string]func(){
		"prd":               func() { taskCreatePRD = "/tmp/x.md" },
		"plan":              func() { taskCreatePlan = "/tmp/x.md" },
		"doc":               func() { taskCreateDocs = []string{"k=/tmp/x.md"} },
		"attach":            func() { taskCreateAttach = []string{"/tmp/x.png"} },
		"parent":            func() { taskCreateParent = "T-1" },
		"project":           func() { taskCreateProject = "P-1" },
		"project-workspace": func() { taskCreateProjectWS = "W-1" },
		"blocked-by":        func() { taskCreateBlockedBy = []string{"T-2"} },
		"blocks":            func() { taskCreateBlocks = []string{"T-3"} },
		"billing-code":      func() { taskCreateBillingCode = "ACME-1" },
	}

	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			resetPaperclipOnlyFlags(t)
			set()

			err := rejectPaperclipOnlyFlags()
			require.Error(t, err, "--%s must not be silently accepted", name)
			assert.Contains(t, err.Error(), "--"+name)
			assert.Contains(t, err.Error(), "#351",
				"the error must point at the open decision, not just refuse")
		})
	}
}

//nolint:paralleltest // mutates the package-level flag globals
func TestSurvivingFlagsAreNotRejected(t *testing.T) {
	resetPaperclipOnlyFlags(t)

	// --assign and --label survive: GitHub has both natively, so the intent is
	// re-homed rather than retired.
	taskCreateAssign = "someone"
	taskCreateLabels = []string{"p1", "needs-triage"}

	require.NoError(t, rejectPaperclipOnlyFlags(),
		"--assign and --label were ported to the GitHub leg and must still work")
}

// TestDryRunTouchesNothing is fault 3: the assignee lookup ran BEFORE the
// dry-run exit, so `--assign x --dry-run` made a network call and errored — a
// preview with a side effect, against this repo's own destructive-command
// standard.
//
// A dry run with an assignee now returns cleanly. If it still reached out it
// would fail here, because there is no Paperclip service and no database in a
// unit test.
//
//nolint:paralleltest // mutates the package-level flag globals
func TestDryRunTouchesNothing(t *testing.T) {
	resetPaperclipOnlyFlags(t)

	taskCreateAssign = "someone"
	taskCreateDryRun = true

	require.NoError(t, runTaskCreate(nil, nil),
		"dry-run must not contact any service or database")
}

// TestTaskCreateHelpDropsPaperclip guards the user-visible story. The long help
// advertised --skip-paperclip as the escape hatch for a dead service, and that
// flag was never declared in commands.yaml — so the documented workaround was
// itself unreachable.
func TestTaskCreateHelpDropsPaperclip(t *testing.T) {
	t.Parallel()

	assert.NotContains(t, taskCreateCmd.Short, "Paperclip",
		"the short help still advertises a retired leg")
	assert.NotContains(t, taskCreateCmd.Long, "--skip-paperclip",
		"the long help still advertises a flag that no longer exists")
	assert.Contains(t, taskCreateCmd.Long, "#351",
		"the long help should say where the leg went")
}
