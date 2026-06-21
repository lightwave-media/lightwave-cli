//go:build integration

package cli_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Operator flow (sandboxed):
//
//	lw version → lw home sync → lw release flag _ --list
//
// Requires ~/dev/lightwave-core checkout; skips in CI without fleet layout.
func TestOperatorFlow_VersionHomeSyncReleaseFlags(t *testing.T) {
	fleet := fleetRoot(t)
	coreBlueprints := filepath.Join(fleet, "lightwave-core", "src", "boilerplate", "blueprints")
	if _, err := os.Stat(coreBlueprints); err != nil {
		t.Skipf("fleet layout missing at %s", coreBlueprints)
	}

	bin := buildLW(t)
	home := t.TempDir()
	lwHome := filepath.Join(home, ".lightwave")
	require.NoError(t, os.MkdirAll(lwHome, 0o755))

	env := []string{
		"HOME=" + home,
		"LW_HOME_PRINT=" + lwHome,
		"PATH=" + minimalPath(t),
	}

	type step struct {
		name   string
		args   []string
		wantIn []string
	}

	steps := []step{
		{
			name:   "version",
			args:   []string{"version"},
			wantIn: []string{"lw version"},
		},
		{
			name:   "home_sync",
			args:   []string{"home", "sync"},
			wantIn: []string{"home sync:"},
		},
		{
			name: "release_flag_list",
			args: []string{"release", "flag", "_", "--list"},
			wantIn: []string{
				"autonomous_release_merge",
				"lw_voice_commands",
				"autonomous_qa_release_pass",
			},
		},
	}

	results := make([]string, 0, len(steps))

	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			out, err := runLW(t, bin, fleet, env, s.args...)
			require.NoError(t, err, "stderr+stdout:\n%s", out)

			for _, needle := range s.wantIn {
				require.Contains(t, out, needle)
			}

			results = append(results, s.name+": PASS")
		})
	}

	t.Logf("operator flow results:\n  %s", strings.Join(results, "\n  "))
}

func buildLW(t *testing.T) string {
	t.Helper()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	out := filepath.Join(t.TempDir(), "lw")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/lw")
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), stderr.String())

	return out
}

func fleetRoot(t *testing.T) string {
	t.Helper()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	return filepath.Join(home, "dev")
}

func minimalPath(t *testing.T) string {
	t.Helper()

	// Keep /usr/bin for basic tools; drop mise shims so we exercise dev lw only.
	return "/usr/bin:/bin:/usr/sbin:/sbin"
}

func runLW(t *testing.T, bin, fleet string, extraEnv []string, args ...string) (string, error) {
	t.Helper()

	env := append(os.Environ(), extraEnv...)
	env = append(env,
		"LW_LIGHTWAVE_ROOT="+fleet,
		"LW_CLI_DEV_DOMAINS=1",
	)

	cmd := exec.Command(bin, args...)
	cmd.Env = env

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	err := cmd.Run()

	return combined.String(), err
}
