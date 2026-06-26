package homepolicy_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/homepolicy"
	"github.com/stretchr/testify/require"
)

func TestSyncFlagsRegistry_CopiesOnDrift(t *testing.T) {
	home := t.TempDir()
	bpLib := filepath.Join(home, "bp")
	bpHome := filepath.Join(bpLib, "lightwave-home", "config", "flags")
	require.NoError(t, os.MkdirAll(bpHome, 0o755))

	stamp := `flags:
  - flag_key: new_flag
    default: false
    owner: v_qa-engineer
`
	require.NoError(t, os.WriteFile(filepath.Join(bpHome, "registry.yaml"), []byte(stamp), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(bpLib, "lightwave-home", "boilerplate.yml"), []byte("variables: []\n"), 0o644))

	printRoot := filepath.Join(home, ".lightwave")
	t.Setenv("HOME", home)
	t.Setenv("LW_BLUEPRINTS_DIR", bpLib)
	t.Setenv("LW_HOME_PRINT", printRoot)

	updated, err := homepolicy.SyncFlagsRegistry()
	require.NoError(t, err)
	require.True(t, updated)

	got, err := os.ReadFile(filepath.Join(printRoot, "config", "flags", "registry.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(got), "new_flag")
}

func TestSyncBaseline_Idempotent(t *testing.T) {
	home := t.TempDir()
	bpLib := filepath.Join(home, "bp")
	flagsDir := filepath.Join(bpLib, "lightwave-home", "config", "flags")
	require.NoError(t, os.MkdirAll(flagsDir, 0o755))
	reg := []byte("flags: []\n")
	require.NoError(t, os.WriteFile(filepath.Join(flagsDir, "registry.yaml"), reg, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(bpLib, "lightwave-home", "boilerplate.yml"), []byte("variables: []\n"), 0o644))

	t.Setenv("HOME", home)
	t.Setenv("LW_BLUEPRINTS_DIR", bpLib)
	t.Setenv("LW_HOME_PRINT", filepath.Join(home, ".lightwave"))

	first, err := homepolicy.SyncBaseline()
	require.NoError(t, err)
	require.Len(t, first.Updated, 1)

	second, err := homepolicy.SyncBaseline()
	require.NoError(t, err)
	require.Empty(t, second.Updated)
}
