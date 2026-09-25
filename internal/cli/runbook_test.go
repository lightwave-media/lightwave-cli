//nolint:testpackage // needs internal access to findChild and BuildDispatched wiring
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/runbook"
	"github.com/lightwave-media/lightwave-cli/internal/sst"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// --var carries Key=Value pairs whose values may contain commas. Parsed like
// the stringArrayFlags (comma-split), one URL input would arrive as two
// broken pairs.
func TestRunbookVarFlag_RepeatsWithoutCommaSplitting(t *testing.T) {
	t.Parallel()

	var got []string

	cmd := buildSubcommand(sst.CLICommand{Name: "start", Description: "d", Flags: []string{"--var"}}, "runbook.start",
		func(_ context.Context, _ []string, flags map[string]any) error {
			got = flagStrSlice(flags, "var")

			return nil
		})
	cmd.SetArgs([]string{"--var", "Url=https://x/y?a=1,b=2", "--var", "Name=alice"})

	require.NoError(t, cmd.Execute())
	require.Equal(t, []string{"Url=https://x/y?a=1,b=2", "Name=alice"}, got)
}

// show exists so a caller can run a runbook without reading its MDX (#545):
// it must say which inputs are required, which steps wait for sign-off, where
// the runbook may run, and the exact command.
func TestPrintRunbookDescription_SaysHowToRunIt(t *testing.T) {
	t.Parallel()

	desc := &runbook.Description{
		Slug: "demo", Dir: "ops/demo", Status: "active", Description: "Rotates a thing",
		Inputs: []runbook.InputDecl{
			{Name: "Target", Type: "string", Validations: []string{"required"}},
			{Name: "Force", Type: "bool", Default: false},
		},
		Steps: []runbook.Step{
			{ID: "preflight", Kind: runbook.KindCheck, Path: "checks/tools.sh"},
			{ID: "rotate", Kind: runbook.KindCommand, Path: "scripts/rotate.sh", HighBlast: true},
		},
	}

	var out strings.Builder
	require.NoError(t, printRunbookDescription(&out, desc))

	got := out.String()
	require.Contains(t, got, "changes files, so runs in a task worktree")
	require.Regexp(t, `Target\s+string\s+required`, got)
	require.Regexp(t, `Force\s+bool\s+default false`, got)
	require.Regexp(t, `command\s+rotate\s+scripts/rotate.sh  \[sign-off\]`, got)
	require.NotRegexp(t, `preflight.*\[sign-off\]`, got, "a check that needs no shell does not wait")
	require.Contains(t, got, "Run: lw runbook apply demo --var Target=<value>\n")
}

// runbook reaches the CLI through the schema dispatcher, not a hardcoded
// rootCmd.AddCommand. #335 shipped the five verbs on a hardcoded tree as an
// interim; #338 registered their handlers, and the stamp publishes the domain
// with no `_status`, so the dispatcher owns it now. Attaching both listed
// `runbook` twice in `lw --help`.
//
// Building a fresh root here (rather than asserting against the rootCmd
// singleton) keeps this test independent of what init() happens to attach, and
// is the same shape mcp_handlers_test.go uses.
//
//nolint:paralleltest // BuildDispatched reads process-global config
func TestRunbookCmd_RegisteredViaDispatcher(t *testing.T) {
	if _, err := config.Load(); err != nil {
		t.Skipf("config unavailable: %v", err)
	}

	root := &cobra.Command{Use: "lw"}
	require.NoError(t, BuildDispatched(root, map[string]bool{}))

	// BuildDispatched returns nil when commands.yaml is absent — it warns and
	// attaches nothing so the binary stays usable without a lightwave-core
	// checkout. So the absence of the domain, not an error, is what says the
	// stamp was unavailable. Same guard shape as mcp_handlers_test.go.
	rb := findChild(root, "runbook")
	if rb == nil {
		t.Skip("stamp did not dispatch runbook (lightwave-core commands.yaml missing)")
	}

	for _, name := range []string{"start", "status", "apply", "step-complete", "cancel"} {
		sub := findChild(rb, name)
		require.NotNil(t, sub, "lw runbook %s should be registered", name)
		require.NotNil(t, sub.RunE, "lw runbook %s should be runnable", name)
	}
}
