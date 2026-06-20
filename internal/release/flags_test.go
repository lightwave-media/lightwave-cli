package release_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/release"
	"github.com/stretchr/testify/require"
)

func writeRegistry(t *testing.T, dir string) {
	t.Helper()

	flagsDir := filepath.Join(dir, "config", "flags")
	require.NoError(t, os.MkdirAll(flagsDir, 0o755))

	reg := `flags:
  - flag_key: lw_voice_commands
    description: test
    default: false
    owner: v_release-engineer
  - flag_key: autonomous_release_merge
    description: test
    default: false
    owner: v_release-engineer
  - flag_key: autonomous_release_pr_merge
    description: test
    default: false
    owner: v_release-engineer
  - flag_key: release_merge_hold
    description: test
    default: false
    owner: v_cto
  - flag_key: autonomous_qa_release_pass
    description: test
    default: false
    owner: v_qa-engineer
`
	require.NoError(t, os.WriteFile(filepath.Join(flagsDir, "registry.yaml"), []byte(reg), 0o644))
}

func pinFlags(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	writeRegistry(t, filepath.Join(home, ".lightwave"))

	bpLib := filepath.Join(home, "bp")
	writeRegistry(t, filepath.Join(bpLib, "lightwave-home"))
	require.NoError(t, os.WriteFile(
		filepath.Join(bpLib, "lightwave-home", "boilerplate.yml"),
		[]byte("variables: []\n"), 0o644))

	t.Setenv("HOME", home)
	t.Setenv("LW_BLUEPRINTS_DIR", bpLib)
	t.Setenv("LW_FLAGS_REGISTRY", filepath.Join(home, ".lightwave", "config", "flags", "registry.yaml"))
	t.Setenv("LW_FLAGS_PRINT", filepath.Join(home, ".lightwave", "config", "flags.toml"))

	return home
}

func TestIsEnabled_DefaultOff(t *testing.T) { //nolint:paralleltest // pinFlags uses t.Setenv
	pinFlags(t)

	on, err := release.IsEnabled("lw_voice_commands")
	require.NoError(t, err)
	require.False(t, on)
}

func TestSetFlag_Persists(t *testing.T) { //nolint:paralleltest // pinFlags uses t.Setenv
	pinFlags(t)

	require.NoError(t, release.SetFlag("lw_voice_commands", true))

	on, err := release.IsEnabled("lw_voice_commands")
	require.NoError(t, err)
	require.True(t, on)
}

func TestEnvOverride(t *testing.T) {
	pinFlags(t)
	t.Setenv("LW_FEATURE_LW_VOICE_COMMANDS", "1")

	on, err := release.IsEnabled("lw_voice_commands")
	require.NoError(t, err)
	require.True(t, on)
}

func TestGateVoice_BlockedWhenOff(t *testing.T) { //nolint:paralleltest // pinFlags uses t.Setenv
	pinFlags(t)

	require.Error(t, release.GateVoice())
}
