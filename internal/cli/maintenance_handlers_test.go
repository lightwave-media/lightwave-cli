package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/maintenance"
	"github.com/lightwave-media/lightwave-cli/internal/testutil"
)

// maintenanceWorkspace points the stamp and the print at throwaway trees.
//
// LW_HOME_PRINT is the estate's existing override for the rendered home, so
// nothing here reaches the operator's real ~/.lightwave. That matters more than
// usual: two of these verbs delete and truncate.
func maintenanceWorkspace(t *testing.T) (stampRoot, printRoot string) {
	t.Helper()

	stampRoot = t.TempDir()
	meta := filepath.Join(stampRoot, "lightwave-core", "src", "schemas", "data", "meta")
	require.NoError(t, os.MkdirAll(meta, 0o755))

	writeFile(t, filepath.Join(meta, "homedir.yaml"), `
example:
  dirs:
    - path: "observability"
    - path: "skills"
`)
	writeFile(t, filepath.Join(meta, "homedir_zones.yaml"), `
example:
  version: "1.7.0"
  zones:
    RUNTIME:
      wipe_on_reset: true
      dirs: [observability]
    AUTHORED:
      wipe_on_reset: false
      dirs: [skills]
`)

	printRoot = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(printRoot, "observability"), 0o755))

	t.Setenv("LW_LIGHTWAVE_ROOT", stampRoot)
	t.Setenv("LW_HOME_PRINT", printRoot)
	config.Reset()
	t.Cleanup(config.Reset)

	return stampRoot, printRoot
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestMaintenanceScanSlopReportsDrift(t *testing.T) {
	_, print := maintenanceWorkspace(t)

	require.NoError(t, os.MkdirAll(filepath.Join(print, "undeclared-dir"), 0o755))

	out, err := testutil.RunHandler(t, "maintenance.scan-slop", nil, nil)
	require.NoError(t, err)

	assert.Contains(t, out, "undeclared-dir/")
	assert.Contains(t, out, "zones v1.7.0", "the report names the stamp version it judged against")
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestMaintenanceScanSlopJSONIsMachineReadable(t *testing.T) {
	_, print := maintenanceWorkspace(t)

	require.NoError(t, os.MkdirAll(filepath.Join(print, "undeclared-dir"), 0o755))

	out, err := testutil.RunHandler(t, "maintenance.scan-slop", nil, map[string]any{jsonFlag: true})
	require.NoError(t, err)

	var findings maintenance.Findings
	require.NoError(t, json.Unmarshal([]byte(out), &findings), "--json emits only JSON")
	assert.Contains(t, findings.UndeclaredTopLevel, "undeclared-dir/")
}

// TestMaintenanceRefusesAPrintThatIsNotThere is the rejection path.
//
// A hygiene verb reporting "0 signals, all clean" against a tree that does not
// exist is the exact failure it was built to detect. Worse for the two
// destructive verbs: a wrong root that scans empty is indistinguishable from a
// right root that is clean.
//
//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestMaintenanceRefusesAPrintThatIsNotThere(t *testing.T) {
	stamp, _ := maintenanceWorkspace(t)

	t.Setenv("LW_LIGHTWAVE_ROOT", stamp)
	t.Setenv("LW_HOME_PRINT", filepath.Join(t.TempDir(), "never-rendered"))
	config.Reset()

	for _, key := range []string{
		"maintenance.scan-slop", "maintenance.rotate-logs", "maintenance.prune-empty",
	} {
		_, err := testutil.RunHandler(t, key, nil, nil)
		require.Error(t, err, key)
		assert.Contains(t, err.Error(), "no rendered print at", key)
	}
}

// TestMaintenanceDryRunWritesNothing is the property that makes --dry-run worth
// having: it must be provably inert, not merely quiet.
//
//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestMaintenanceDryRunWritesNothing(t *testing.T) {
	_, print := maintenanceWorkspace(t)

	log := filepath.Join(print, "observability", "ledger.jsonl")
	writeFile(t, log, strings.Repeat("x", maintenance.MaxLogBytes+1))

	empty := filepath.Join(print, "observability", "handoffs")
	require.NoError(t, os.MkdirAll(empty, 0o755))

	dryRun := map[string]any{dryRunFlag: true}

	rotateOut, err := testutil.RunHandler(t, "maintenance.rotate-logs", nil, dryRun)
	require.NoError(t, err)
	assert.Contains(t, rotateOut, "would be archived")

	pruneOut, err := testutil.RunHandler(t, "maintenance.prune-empty", nil, dryRun)
	require.NoError(t, err)
	assert.Contains(t, pruneOut, "would be removed")

	info, err := os.Stat(log)
	require.NoError(t, err)
	assert.Positive(t, info.Size(), "the log still holds its bytes")

	_, err = os.Stat(empty)
	require.NoError(t, err, "the empty dir is still there")

	_, err = os.Stat(filepath.Join(print, maintenance.ArchiveDir))
	require.Error(t, err, "a dry run does not even create the archive directory")
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestMaintenanceRotateLogsArchivesAndTruncates(t *testing.T) {
	_, print := maintenanceWorkspace(t)

	log := filepath.Join(print, "observability", "ledger.jsonl")
	writeFile(t, log, strings.Repeat("x", maintenance.MaxLogBytes+1))

	out, err := testutil.RunHandler(t, "maintenance.rotate-logs", nil, map[string]any{yesFlag: true})
	require.NoError(t, err)
	assert.Contains(t, out, "rotated 1 of 1")

	info, err := os.Stat(log)
	require.NoError(t, err)
	assert.Zero(t, info.Size())

	archives, err := os.ReadDir(filepath.Join(print, maintenance.ArchiveDir))
	require.NoError(t, err)
	require.Len(t, archives, 1)
	assert.True(t, strings.HasSuffix(archives[0].Name(), ".gz"))
}

// TestMaintenancePruneEmptyLeavesProtectedZonesAlone: --yes skips the prompt,
// never the zone policy.
//
//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestMaintenancePruneEmptyLeavesProtectedZonesAlone(t *testing.T) {
	_, print := maintenanceWorkspace(t)

	wipeable := filepath.Join(print, "observability", "handoffs")
	protected := filepath.Join(print, "skills", "journey-first", "evals")
	require.NoError(t, os.MkdirAll(wipeable, 0o755))
	require.NoError(t, os.MkdirAll(protected, 0o755))

	out, err := testutil.RunHandler(t, "maintenance.prune-empty", nil, map[string]any{yesFlag: true})
	require.NoError(t, err)
	assert.Contains(t, out, "AUTHORED", "the report says WHY it was left alone")

	_, err = os.Stat(wipeable)
	require.Error(t, err, "a RUNTIME empty dir is removed")

	_, err = os.Stat(protected)
	require.NoError(t, err, "an AUTHORED empty dir survives --yes")
}
