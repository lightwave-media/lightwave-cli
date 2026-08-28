//nolint:testpackage // needs internal access to findChild and BuildDispatched wiring
package cli

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

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
	if err := BuildDispatched(root, map[string]bool{}); err != nil {
		t.Skipf("dispatcher unavailable (schema not checked out?): %v", err)
	}

	rb := findChild(root, "runbook")
	require.NotNil(t, rb, "runbook should be attached by the dispatcher")

	for _, name := range []string{"start", "status", "apply", "step-complete", "cancel"} {
		sub := findChild(rb, name)
		require.NotNil(t, sub, "lw runbook %s should be registered", name)
		require.NotNil(t, sub.RunE, "lw runbook %s should be runnable", name)
	}
}
