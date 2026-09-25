package blueprint_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Not parallel: t.Setenv forbids it.
//
//nolint:paralleltest // env mutation cannot run in parallel
func TestBlueprintsDir_EnvOverride(t *testing.T) {
	t.Setenv(blueprint.EnvBlueprintsDir, "/custom/lib")
	assert.Equal(t, "/custom/lib", blueprint.BlueprintsDir("/anything"))
}

//nolint:paralleltest // env mutation cannot run in parallel
func TestBlueprintsDir_Default(t *testing.T) {
	t.Setenv(blueprint.EnvBlueprintsDir, "")
	assert.Equal(t,
		filepath.Join("/root", "lightwave-core", "src", "boilerplate", "blueprints"),
		blueprint.BlueprintsDir("/root"))
}

func TestResolve(t *testing.T) {
	t.Parallel()

	lib := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(lib, "good"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(lib, "good", "boilerplate.yml"), []byte("variables: []\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(lib, "no-manifest"), 0o755))

	t.Run("resolves a blueprint with a manifest", func(t *testing.T) {
		t.Parallel()

		path, err := blueprint.Resolve(lib, "good")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(lib, "good"), path)
	})

	t.Run("errors when blueprint dir lacks a manifest", func(t *testing.T) {
		t.Parallel()

		_, err := blueprint.Resolve(lib, "no-manifest")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("errors on empty name", func(t *testing.T) {
		t.Parallel()

		_, err := blueprint.Resolve(lib, "")
		require.Error(t, err)
	})

	t.Run("errors when the library is missing", func(t *testing.T) {
		t.Parallel()

		_, err := blueprint.Resolve(filepath.Join(lib, "nope"), "good")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "library not found")
	})
}

// A manifest's `validations:` must hold for values given with --var, not only
// for defaults. The linked engine skips them for command-line values, so
// before this a runbook_slug of "../escape" rendered (lightwave-core#654).
func TestRender_EnforcesManifestValidationsOnGivenVars(t *testing.T) {
	t.Parallel()

	lib := t.TempDir()
	bp := filepath.Join(lib, "strict")
	require.NoError(t, os.MkdirAll(bp, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bp, "boilerplate.yml"), []byte(`variables:
  - name: slug
    type: string
    validations:
      - required
      - 'regex("^[a-z0-9]+(-[a-z0-9]+)*$")'
  - name: tools
    type: list
    validations:
      - required
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(bp, "{{ .slug }}.txt"), []byte("{{ .tools }}\n"), 0o644))

	render := func(vars ...string) (string, error) {
		out := t.TempDir()

		return out, blueprint.Render(t.Context(), &blueprint.RenderOptions{BlueprintPath: bp, OutputFolder: out, Vars: vars})
	}

	for _, bad := range [][]string{
		{"slug=../escape", `tools=["git"]`},
		{"slug=Not_Kebab", `tools=["git"]`},
		{"slug=ok", "tools=[]"},
	} {
		out, err := render(bad...)
		require.Error(t, err, "%v rendered", bad)
		assert.Contains(t, err.Error(), "invalid variable value")

		entries, readErr := os.ReadDir(out)
		require.NoError(t, readErr)
		assert.Empty(t, entries, "%v was refused but still wrote files", bad)
	}

	out, err := render("slug=rotate-key", `tools=["git"]`)
	require.NoError(t, err, "valid values must still render")
	assert.FileExists(t, filepath.Join(out, "rotate-key.txt"))
}

// TestRender is the end-to-end smoke: a minimal blueprint through the real
// boilerplate engine into a tmp dir.
//
// This used to skip when no `boilerplate` binary was installed — which is the
// normal state on a CI runner, so the only end-to-end render test was skipping
// exactly where it mattered, and a skip is indistinguishable from a pass. The
// engine is now a linked library, so it is always present and the test always
// runs. Do not reintroduce a skip here.
func TestRender(t *testing.T) {
	t.Parallel()

	lib := t.TempDir()
	bp := filepath.Join(lib, "mini")
	require.NoError(t, os.MkdirAll(bp, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bp, "boilerplate.yml"),
		[]byte("variables:\n  - name: who\n    type: string\n    default: world\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(bp, "greeting.txt"),
		[]byte("Hello {{ .who }}!\n"), 0o644))

	out := t.TempDir()

	path, err := blueprint.Resolve(lib, "mini")
	require.NoError(t, err)

	err = blueprint.Render(context.Background(), &blueprint.RenderOptions{
		BlueprintPath: path,
		OutputFolder:  out,
		Vars:          []string{"who=LightWave"},
	})
	require.NoError(t, err, "render should succeed against the real engine")

	got, err := os.ReadFile(filepath.Join(out, "greeting.txt"))
	require.NoError(t, err, "blueprint should have generated greeting.txt")
	assert.Equal(t, "Hello LightWave!\n", string(got))
}
