//nolint:testpackage // needs internal access to rootCmd
package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

//nolint:paralleltest // mutates the shared rootCmd via applyDecommissions
func TestRunbookCmd_Registered(t *testing.T) {
	applyDecommissions(rootCmd)
	rb := findChild(rootCmd, "runbook")
	require.NotNil(t, rb, "runbook command should be registered")
	for _, name := range []string{"start", "status", "apply", "step-complete", "cancel"} {
		sub := findChild(rb, name)
		require.NotNil(t, sub, "lw runbook %s should be registered", name)
		require.NotNil(t, sub.RunE)
	}
}
