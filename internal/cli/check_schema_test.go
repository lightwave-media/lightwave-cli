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
	"github.com/lightwave-media/lightwave-cli/internal/sst"
	"github.com/lightwave-media/lightwave-cli/internal/testutil"
)

// skipIfNoLightwaveCore skips when the sibling lightwave-core repo isn't
// checked out. CI runs lightwave-cli stand-alone (lightwave-core is private,
// the default GITHUB_TOKEN can't reach it without a cross-repo PAT), so the
// skip is legitimate there.
//
// It must not be the state on a developer machine. From 2026-06 until #387
// this helper stat'd
//
//	<root>/packages/lightwave-core/lightwave/schema/definitions/config/cli/commands.yaml
//
// which carries BOTH the dissolved ~/dev/lightwave-media umbrella and the
// pre-rebuild schema layout. Neither has existed since the flat-sibling move,
// so the path could never resolve and all three tests below skipped on every
// machine — including ones with a healthy ~/dev/lightwave-core. The gate they
// cover (schema-drift-check.yml, armed and blocking merges since #301) had no
// running tests at all.
//
// Two changes keep that from recurring. The path now comes from
// sst.CLIConfigPath — the same resolver the production loader uses, so a future
// layout move updates this helper for free instead of silently re-skipping.
// And LW_SURFACE_GATE_STRICT=1 turns the skip into a failure, matching
// command_surface_test.go: a gate that skips in CI is indistinguishable from a
// gate that passes, which is the whole bug (#350).
func skipIfNoLightwaveCore(t *testing.T) {
	t.Helper()

	cfg := config.Get()
	if cfg == nil {
		if os.Getenv("LW_SURFACE_GATE_STRICT") == "1" {
			t.Fatal("LW_SURFACE_GATE_STRICT=1 but config did not load, so the " +
				"schema-drift tests would have checked nothing")
		}

		t.Skip("config not loaded; schema-drift tests skip")
	}

	path := sst.CLIConfigPath(cfg.Paths.LightwaveRoot)
	if _, err := os.Stat(path); err != nil {
		if os.Getenv("LW_SURFACE_GATE_STRICT") == "1" {
			t.Fatalf("LW_SURFACE_GATE_STRICT=1 but the stamp is unreadable at %s: %v — "+
				"the schema-drift tests would have checked nothing", path, err)
		}

		t.Skipf("lightwave-core schema not present at %s; skipping schema-drift test", path)
	}
}

// TestStampPathIsFlatSiblingLayout is the regression guard for #387.
//
// It pins the SHAPE of the resolved path rather than driving skipIfNoLightwaveCore
// through the environment, deliberately: LW_LIGHTWAVE_ROOT is exported by the
// operator harness (settings.json `env`), so an inline override in a test run is
// silently replaced and a control built on it proves nothing — it passes whether
// or not the code is correct. Asserting the shape needs no environment and fails
// for exactly the reason the bug existed.
//
// The two substrings below are the fingerprints of the layouts that produced the
// silent skip: `packages/` from the dissolved ~/dev/lightwave-media umbrella, and
// `definitions/` from the pre-rebuild schema tree. If either reappears in the
// resolver, every schema-drift test goes back to skipping on every machine and
// nothing else would report it.
func TestStampPathIsFlatSiblingLayout(t *testing.T) {
	t.Parallel()

	got := sst.CLIConfigPath("/ROOT")

	assert.Equal(t,
		filepath.Join("/ROOT", "lightwave-core", "src", "schemas", "interfaces", "cli", "commands.yaml"),
		got,
		"stamp path must be the flat-sibling layout")

	assert.NotContains(t, got, "packages",
		"dissolved ~/dev/lightwave-media umbrella layout is back (#387)")
	assert.NotContains(t, got, "definitions",
		"pre-rebuild lightwave/schema/definitions layout is back (#387)")
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

// The key strings recur across the table; naming them also makes the two
// denominators visually distinct at each call site.
const (
	taskList   = "task.list"
	taskCreate = "task.create"
	adrNew     = "adr.new" // declared under an in_development domain
)

// These tests drive ComputeSchemaDrift directly rather than through
// testutil.RunHandler, so they run everywhere — including CI, which has no
// lightwave-core checkout and therefore skips every handler-level schema test.
//
// That matters here specifically: the defect below was shipped and armed for
// months while the tests that could have caught it were skipping. A guard that
// only runs on a developer machine is not a guard (CLAUDE.md §18).

// TestComputeSchemaDrift_InDevelopmentHandlerIsNotOrphaned is the regression
// test for the defect that blocked the lightwave-core#647 cohort.
//
// `adr.new` is DECLARED in commands.yaml under an `_status: in_development`
// domain. It is therefore absent from KeysPublished and present in Keys. Before
// the fix the orphan direction measured against the published set, so
// registering the handler the declaration was waiting for made the armed gate
// fail and block the merge — the exact opposite of the mechanism's purpose.
func TestComputeSchemaDrift_InDevelopmentHandlerIsNotOrphaned(t *testing.T) {
	t.Parallel()

	published := []string{taskList, taskCreate}
	stamped := []string{taskList, taskCreate, adrNew} // adr is in_development
	registry := []string{taskList, taskCreate, adrNew}
	surface := []string{taskList, taskCreate}

	missing, orphaned, unstamped := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Empty(t, orphaned, "a handler whose command IS stamped must never be orphaned")
	assert.Empty(t, missing, "every published command has its handler")
	assert.Empty(t, unstamped, "nothing invocable is unstamped")
}

// TestComputeSchemaDrift_StillCatchesATrueOrphan is the other direction.
//
// Widening the orphan denominator could have silenced the check entirely. A
// handler with no declaration anywhere must still be reported — that is the
// invariant AGENTS.md states as "no registered handler without a schema entry".
func TestComputeSchemaDrift_StillCatchesATrueOrphan(t *testing.T) {
	t.Parallel()

	published := []string{taskList}
	stamped := []string{taskList}
	registry := []string{taskList, "ghost.verb"} // declared nowhere
	surface := []string{taskList}

	_, orphaned, _ := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Equal(t, []string{"ghost.verb"}, orphaned,
		"a handler with no declaration at all is still drift")
}

// TestComputeSchemaDrift_MissingStillSkipsInDevelopment pins the behaviour the
// narrowed set was introduced for, so the fix above cannot regress it.
func TestComputeSchemaDrift_MissingStillSkipsInDevelopment(t *testing.T) {
	t.Parallel()

	// `cron.sync` is declared in_development and has no handler yet. That is
	// the state the mechanism exists to permit.
	published := []string{taskList}
	stamped := []string{taskList, "cron.sync"}
	registry := []string{taskList}
	surface := []string{taskList}

	missing, orphaned, _ := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Empty(t, missing, "an in_development command may be declared ahead of its handler")
	assert.Empty(t, orphaned)
}

func TestComputeSchemaDrift_MissingHandlerIsReported(t *testing.T) {
	t.Parallel()

	published := []string{taskList, taskCreate}
	stamped := []string{taskList, taskCreate}
	registry := []string{taskList}
	surface := []string{taskList}

	missing, _, _ := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Equal(t, []string{taskCreate}, missing,
		"a published command with no handler is drift")
}

// TestComputeSchemaDrift_UnstampedUsesTheStampedSet guards the third direction
// against the same conflation. An in_development command that IS invocable
// (LW_CLI_DEV_DOMAINS=1) is stamped, so it is not part of the unstamped
// backlog.
func TestComputeSchemaDrift_UnstampedUsesTheStampedSet(t *testing.T) {
	t.Parallel()

	published := []string{taskList}
	stamped := []string{taskList, adrNew}
	registry := []string{taskList, adrNew}
	surface := []string{taskList, adrNew, "audit.run"} // audit is cobra-only

	_, _, unstamped := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Equal(t, []string{"audit.run"}, unstamped,
		"only the genuinely undeclared cobra-tree command is unstamped")
}

func TestComputeSchemaDrift_SortsEveryDirection(t *testing.T) {
	t.Parallel()

	published := []string{"b.two", "a.one"}
	stamped := []string{"b.two", "a.one"}
	registry := []string{"z.orphan", "m.orphan"}
	surface := []string{"z.surface", "a.surface"}

	missing, orphaned, unstamped := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Equal(t, []string{"a.one", "b.two"}, missing)
	assert.Equal(t, []string{"m.orphan", "z.orphan"}, orphaned)
	assert.Equal(t, []string{"a.surface", "z.surface"}, unstamped)
}
