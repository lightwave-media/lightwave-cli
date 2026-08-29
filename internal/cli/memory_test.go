//nolint:testpackage // drives the unexported memory cobra commands directly
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `lw memory` was listed in VerifiedCommands — a list whose contract is "the
// command has a passing e2e/smoke test" — with no test exercising it. The
// internal/memory package was tested; the CLI wiring on top of it was not, so a
// handler could have been reading the wrong flag and nothing would have said so.
//
// This drives the real cobra RunE functions end to end. memory.Root() resolves
// through os.UserHomeDir(), which reads $HOME on Unix, so pointing HOME at a
// TempDir keeps the round trip entirely inside the test.
//
//nolint:paralleltest // sets HOME and mutates the shared mem* flag globals
func TestMemoryCmd_PutGetListDeleteRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// The flag vars are package globals shared by every memory subcommand;
	// restore them so ordering with other serial tests cannot matter.
	t.Cleanup(func() {
		memNamespace, memKey, memValue, memValueFile, memJSON = "", "", "", "", false
	})

	memNamespace = "v_core"
	memKey = "audit/last-run"
	memValue = "2026-08-29"

	require.NoError(t, runMemoryPut(memoryPutCmd, nil), "lw memory put")

	// Assert through the storage package rather than stdout: this proves the
	// command wrote the value the user supplied, at the namespace and key the
	// user supplied, which is the behaviour worth pinning.
	got, err := memory.Get("v_core", "audit/last-run")
	require.NoError(t, err, "value should exist after put")
	assert.Equal(t, "2026-08-29", string(got))

	// It must land under the redirected HOME, not the operator's real one.
	assert.FileExists(t, filepath.Join(home, ".lightwave", "memory", "v_core", "audit", "last-run"))

	keys, err := memory.List("v_core")
	require.NoError(t, err, "lw memory list")
	assert.Contains(t, keys, "audit/last-run")

	require.NoError(t, runMemoryGet(memoryGetCmd, nil), "lw memory get")
	require.NoError(t, runMemoryList(memoryListCmd, nil), "lw memory list")
	require.NoError(t, runMemoryDelete(memoryDeleteCmd, nil), "lw memory delete")

	_, err = memory.Get("v_core", "audit/last-run")
	require.Error(t, err, "value should be gone after delete")
}

// A namespace with a path separator would let a key escape its namespace
// directory. memory.validateNamespace exists to stop that; this pins that the
// CLI path actually reaches it rather than writing outside the store.
//
//nolint:paralleltest // sets HOME and mutates the shared mem* flag globals
func TestMemoryCmd_RejectsTraversalNamespace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Cleanup(func() {
		memNamespace, memKey, memValue = "", "", ""
	})

	memNamespace = "../../etc"
	memKey = "passwd"
	memValue = "nope"

	require.Error(t, runMemoryPut(memoryPutCmd, nil), "traversal namespace must be rejected")

	_, statErr := os.Stat(filepath.Join(home, "..", "etc", "passwd"))
	assert.Error(t, statErr, "nothing may be written outside the memory root")
}
