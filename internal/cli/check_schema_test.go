package cli_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
	"github.com/lightwave-media/lightwave-cli/internal/testutil"
)

// PR9 of the gruntwork-harden mission: prove `lw check schema`
// detects BOTH drift directions and that LW_CHECK_SCHEMA_STRICT=1
// turns the drift report into a non-zero exit. The drift detection
// logic itself lives in checkSchemaHandler (internal/cli/check_handlers.go);
// these tests exercise the handler end-to-end via testutil.RunHandler
// so the CI gate's contract is what they assert.
//
// All three tests intentionally serial — RunHandler swaps process-global
// os.Stdout to capture output, and setting LW_CHECK_SCHEMA_STRICT via
// t.Setenv is also process-global. See internal/testutil/testutil_test.go
// for the same nolint pattern.

var registerDriftFixtures sync.Once

// registerOrphanedHandler installs a handler whose key intentionally
// does NOT exist in commands.yaml. This is the "orphaned handler"
// arm — handler registered but no schema entry.
func registerOrphanedHandler() {
	registerDriftFixtures.Do(func() {
		cli.RegisterHandler("schema_drift_fixture.orphan", func(_ context.Context, _ []string, _ map[string]any) error {
			return nil
		})
	})
}

//nolint:paralleltest // intentionally serial — t.Setenv + RunHandler swap process globals
func TestCheckSchema_StrictEnvFailsOnOrphanedHandler(t *testing.T) {
	registerOrphanedHandler()
	t.Setenv("LW_CHECK_SCHEMA_STRICT", "1")

	out, err := testutil.RunHandler(t, "check.schema", nil, nil)
	require.Error(t, err, "expected non-zero exit when orphaned handler exists; report follows:\n%s", out)
	assert.Contains(t, err.Error(), "drift detected")
	assert.Contains(t, err.Error(), "orphaned")
	// The orphan key should appear in the human-readable report output.
	assert.Contains(t, out, "schema_drift_fixture.orphan",
		"expected orphan key in the report stdout")
}

//nolint:paralleltest // intentionally serial — t.Setenv + RunHandler swap process globals
func TestCheckSchema_NonStrictReportsButPasses(t *testing.T) {
	registerOrphanedHandler()
	// Explicitly unset to defeat any ambient value.
	t.Setenv("LW_CHECK_SCHEMA_STRICT", "")

	out, err := testutil.RunHandler(t, "check.schema", nil, nil)
	require.NoError(t, err, "default mode is informational; expected exit 0 even with drift. stdout:\n%s", out)
	// Drift IS still reported — the gate just doesn't fire.
	assert.Contains(t, out, "schema_drift_fixture.orphan")
}

//nolint:paralleltest // intentionally serial — t.Setenv + RunHandler swap process globals
func TestCheckSchema_JSONShapeReportsBothDriftDirections(t *testing.T) {
	registerOrphanedHandler()
	t.Setenv("LW_CHECK_SCHEMA_STRICT", "")

	out, err := testutil.RunHandler(t, "check.schema", nil, map[string]any{"json": true})
	require.NoError(t, err)
	// JSON report fields exist (we don't fully parse here — just shape-check).
	assert.Contains(t, out, `"missing_handlers"`, "json output missing missing_handlers key")
	assert.Contains(t, out, `"orphaned_handlers"`, "json output missing orphaned_handlers key")
	assert.Contains(t, out, `"handler_match_ratio"`, "json output missing handler_match_ratio key")
}
