package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/testutil"
)

// skipIfNoLightwaveCore skips when the sibling lightwave-core repo
// isn't checked out at the expected workspace path. CI runs
// lightwave-cli stand-alone (lightwave-core is private, default
// GITHUB_TOKEN can't reach it without a cross-repo PAT). Local devs
// have the workspace layout via `~/dev/lightwave-media/packages/*`.
// Same pattern as internal/sst/cli_loader_test.go.
func skipIfNoLightwaveCore(t *testing.T) {
	t.Helper()
	cfg := config.Get()
	if cfg == nil {
		t.Skip("config not loaded; schema-drift tests skip")
	}
	path := filepath.Join(cfg.Paths.LightwaveRoot,
		"packages", "lightwave-core", "lightwave", "schema",
		"definitions", "config", "cli", "commands.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("lightwave-core schema not present at %s; skipping schema-drift test", path)
	}
}

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
	skipIfNoLightwaveCore(t)
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
	skipIfNoLightwaveCore(t)
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
	skipIfNoLightwaveCore(t)
	registerOrphanedHandler()
	t.Setenv("LW_CHECK_SCHEMA_STRICT", "")

	out, err := testutil.RunHandler(t, "check.schema", nil, map[string]any{"json": true})
	require.NoError(t, err)
	// JSON report fields exist (we don't fully parse here — just shape-check).
	assert.Contains(t, out, `"missing_handlers"`, "json output missing missing_handlers key")
	assert.Contains(t, out, `"orphaned_handlers"`, "json output missing orphaned_handlers key")
	assert.Contains(t, out, `"handler_match_ratio"`, "json output missing handler_match_ratio key")
}
