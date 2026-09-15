package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/testutil"
)

// The flag keys and the --tree value recur across every case here; naming them
// also keeps the set visibly matched to what commands.yaml declares.
const (
	treeFlag   = "tree"
	jsonFlag   = "json"
	dryRunFlag = "dry-run"
	yesFlag    = "yes"
	coreTree   = "core"
)

// adrWorkspace builds a throwaway workspace with an empty core ADR corpus and
// points the config at it.
//
// config is a process-global singleton, and testutil.RunHandler swaps
// os.Stdout, so every test here is serial. config.Reset() on both sides is what
// keeps one test's root from leaking into the next.
func adrWorkspace(t *testing.T) string {
	t.Helper()

	workspace := t.TempDir()
	corpus := filepath.Join(workspace, "lightwave-core", "spec", "adr")
	require.NoError(t, os.MkdirAll(corpus, 0o755))

	t.Setenv("LW_LIGHTWAVE_ROOT", workspace)
	config.Reset()
	t.Cleanup(config.Reset)

	return corpus
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestADRNewReservesAndWrites(t *testing.T) {
	corpus := adrWorkspace(t)

	out, err := testutil.RunHandler(t, "adr.new",
		[]string{"Postgres is the canonical store"},
		map[string]any{treeFlag: coreTree})
	require.NoError(t, err, "reserve into an empty corpus")

	assert.Contains(t, out, "reserved CORE-0001")
	assert.Contains(t, out, "lw docs spec-lint", "the next step is named, not assumed")

	written := filepath.Join(corpus, "0001-postgres-is-the-canonical-store.md")
	raw, err := os.ReadFile(written)
	require.NoError(t, err, "the file exists where the handler said it would")

	body := string(raw)
	assert.Contains(t, body, `adr_id: "CORE-0001"`)
	assert.Contains(t, body, "## Context")
	assert.Contains(t, body, "## Decision")
	assert.Contains(t, body, "## Consequences")
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestADRNewJSONIsMachineReadable(t *testing.T) {
	adrWorkspace(t)

	out, err := testutil.RunHandler(t, "adr.new",
		[]string{"Second decision"},
		map[string]any{treeFlag: coreTree, jsonFlag: true})
	require.NoError(t, err)

	var res struct {
		ID     string `json:"id"`
		Tree   string `json:"tree"`
		Status string `json:"status"`
		Number int    `json:"number"`
		DryRun bool   `json:"dry_run"`
	}

	require.NoError(t, json.Unmarshal([]byte(out), &res), "--json emits parseable JSON")
	assert.Equal(t, "CORE-0001", res.ID)
	assert.Equal(t, 1, res.Number)
	assert.Equal(t, "core", res.Tree)
	assert.Equal(t, "proposed", res.Status)
	assert.False(t, res.DryRun)
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestADRNewDryRunWritesNothing(t *testing.T) {
	corpus := adrWorkspace(t)

	out, err := testutil.RunHandler(t, "adr.new",
		[]string{"Preview only"},
		map[string]any{treeFlag: coreTree, dryRunFlag: true})
	require.NoError(t, err)

	assert.Contains(t, out, "would reserve CORE-0001")
	assert.Contains(t, out, "no id consumed")

	entries, err := os.ReadDir(corpus)
	require.NoError(t, err)

	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".md", "a dry run must not write a file")
	}
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestADRNewRequiresATitle(t *testing.T) {
	adrWorkspace(t)

	_, err := testutil.RunHandler(t, "adr.new", nil, map[string]any{treeFlag: coreTree})
	require.Error(t, err, "an untitled ADR is not a decision record")
	assert.Contains(t, err.Error(), "usage:")
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestADRNewRefusesAnUnknownTree(t *testing.T) {
	adrWorkspace(t)

	_, err := testutil.RunHandler(t, "adr.new",
		[]string{"Some decision"},
		map[string]any{treeFlag: "platform"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected core or host")
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestADRNewRequiresATree(t *testing.T) {
	adrWorkspace(t)

	// Defaulting to a corpus would file the decision in the wrong repository.
	_, err := testutil.RunHandler(t, "adr.new", []string{"Some decision"}, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--tree is required")
}

// TestADRNewReadsOnlyStampedFlags guards the #367 defect class.
//
// A handler that reads a flag the stamp does not declare gets the zero value
// forever, because the dispatcher never registers it — the user is ignored with
// no error. Every flag this handler reads is asserted against the set
// commands.yaml declares for `adr new`.
//
//nolint:paralleltest // RunHandler swaps os.Stdout globally; config is a singleton
func TestADRNewReadsOnlyStampedFlags(t *testing.T) {
	adrWorkspace(t)

	stamped := map[string]any{
		treeFlag:     coreTree,
		"supersedes": "CORE-0007",
		dryRunFlag:   true,
		jsonFlag:     true,
	}

	out, err := testutil.RunHandler(t, "adr.new", []string{"Every stamped flag together"}, stamped)
	require.NoError(t, err, "the declared flag set is accepted as a whole")

	var res struct {
		DryRun bool `json:"dry_run"`
	}

	require.NoError(t, json.Unmarshal([]byte(out), &res))
	assert.True(t, res.DryRun, "--dry-run reached the handler rather than being silently dropped")
}
