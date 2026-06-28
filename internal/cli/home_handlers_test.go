package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// home.doctor shells out to ~/.lightwave/lib/maintenance/slop.ts via bun. The
// happy path needs both the operator runtime and bun installed, so it isn't
// hermetic; instead these tests pin the two graceful-degradation paths — the
// behavior that matters for a read-only diagnostic that must never panic when
// the runtime isn't provisioned. Both are intentionally serial: t.Setenv and
// testutil.RunHandler (stdout swap) mutate process globals.

//nolint:paralleltest // serial — t.Setenv + RunHandler swap process globals
func TestHomeDoctorHandler_MissingSlopDetector(t *testing.T) {
	// HOME with no ~/.lightwave/lib/maintenance/slop.ts → handler must report
	// the missing detector, not crash. Also proves home.doctor is registered.
	t.Setenv("HOME", t.TempDir())

	_, err := testutil.RunHandler(t, "home.doctor", nil, nil)

	require.Error(t, err, "home doctor must fail when the slop detector is absent")
	assert.Contains(t, err.Error(), "slop detector not found")
}

//nolint:paralleltest // serial — t.Setenv + RunHandler swap process globals
func TestHomeDoctorHandler_MissingBun(t *testing.T) {
	// slop.ts present but bun unavailable (empty PATH) → handler must report
	// the missing interpreter, reached only after the detector-presence check.
	home := t.TempDir()
	t.Setenv("HOME", home)

	slopDir := filepath.Join(home, ".lightwave", "lib", "maintenance")
	require.NoError(t, os.MkdirAll(slopDir, 0o755), "seed slop dir")
	require.NoError(t, os.WriteFile(filepath.Join(slopDir, "slop.ts"), []byte("// stub\n"), 0o644), "seed slop.ts")

	t.Setenv("PATH", "")

	_, err := testutil.RunHandler(t, "home.doctor", nil, nil)

	require.Error(t, err, "home doctor must fail when bun is not on PATH")
	assert.Contains(t, err.Error(), "bun not found")
}
