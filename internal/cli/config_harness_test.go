//nolint:testpackage // tests pure helpers in the cobra glue package
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureCodexShellEnvironmentAddsMissingKeyBeforeNextSection(t *testing.T) {
	t.Parallel()

	current := `personality = "pragmatic"

[shell_environment_policy]
inherit = "core"

[shell_environment_policy.set]
AWS_PROFILE = "old"
PATH = "/usr/bin"

[hooks.state]
trusted_hash = "sha256:abc"
`
	next := ensureCodexShellEnvironment(current, map[string]string{
		"AWS_PROFILE":       "lightwave-admin",
		"LW_BLUEPRINTS_DIR": "/Users/joelschaeffer/dev/lightwave-core/src/boilerplate/blueprints",
		"PATH":              "/Users/joelschaeffer/.local/share/mise/shims:/usr/bin",
	})

	assert.Contains(t, next, "AWS_PROFILE = \"lightwave-admin\"")
	assert.Contains(t, next, "LW_BLUEPRINTS_DIR = \"/Users/joelschaeffer/dev/lightwave-core/src/boilerplate/blueprints\"")
	assert.Contains(t, next, "PATH = \"/Users/joelschaeffer/.local/share/mise/shims:/usr/bin\"")
	assert.Contains(t, next, "LW_BLUEPRINTS_DIR = \"/Users/joelschaeffer/dev/lightwave-core/src/boilerplate/blueprints\"\n\n[hooks.state]")
}

func TestEnsureCodexShellEnvironmentCreatesSection(t *testing.T) {
	t.Parallel()

	next := ensureCodexShellEnvironment("personality = \"pragmatic\"\n", map[string]string{
		"AWS_PROFILE": "lightwave-admin",
	})

	assert.Contains(t, next, "\n[shell_environment_policy.set]\nAWS_PROFILE = \"lightwave-admin\"\n")
}

func TestValidateHarnessPrint(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}

	write("README.md", "# Harness\n")
	write("harnesses.yaml", "_meta:\n  version: \"0.1.0\"\n")
	write("lightwave/runtime.yaml", "runtime:\n  root: /tmp\n")
	write("claude/settings.fragment.json", `{"env":{"AWS_PROFILE":"lightwave-admin"}}`)
	write("pi/settings.fragment.json", `{"skills":[]}`)
	write("codex/config.toml.fragment", `[shell_environment_policy.set]
AWS_PROFILE = "lightwave-admin"
LW_BLUEPRINTS_DIR = "/tmp/blueprints"
PATH = "/tmp/mise-shims:/usr/bin"
`)

	require.NoError(t, validateHarnessPrint(root))
}

//nolint:paralleltest // mutates package-level cobra flag targets
func TestApplyCodexHarnessWritesCodexConfig(t *testing.T) {
	root := t.TempDir()
	fragmentPath := filepath.Join(root, "codex", "config.toml.fragment")
	require.NoError(t, os.MkdirAll(filepath.Dir(fragmentPath), 0o755))
	require.NoError(t, os.WriteFile(fragmentPath, []byte(`[shell_environment_policy.set]
AWS_PROFILE = "lightwave-admin"
LW_BLUEPRINTS_DIR = "/tmp/blueprints"
PATH = "/tmp/mise-shims:/usr/bin"
`), 0o644))

	configPath := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`personality = "pragmatic"

[shell_environment_policy.set]
PATH = "/usr/bin"
`), 0o600))

	oldRoot := harnessRoot
	oldConfig := harnessCodexConfig
	oldDryRun := harnessDryRun
	t.Cleanup(func() {
		harnessRoot = oldRoot
		harnessCodexConfig = oldConfig
		harnessDryRun = oldDryRun
	})
	harnessRoot = root
	harnessCodexConfig = configPath
	harnessDryRun = false

	require.NoError(t, applyCodexHarness())

	info, err := os.Stat(configPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	body, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(body), `AWS_PROFILE = "lightwave-admin"`)
	assert.Contains(t, string(body), `LW_BLUEPRINTS_DIR = "/tmp/blueprints"`)
	assert.Contains(t, string(body), `PATH = "/tmp/mise-shims:/usr/bin"`)
}
