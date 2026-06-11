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
		filepath.Join("/root", "src", "boilerplate", "blueprints"),
		blueprint.BlueprintsDir("/root"))
}

func TestArgs(t *testing.T) {
	t.Parallel()

	got := blueprint.Args(&blueprint.RenderOptions{
		BlueprintPath: "/lib/react-component",
		OutputFolder:  "/out",
		Vars:          []string{"category=marketing", "component_name=Hero"},
		VarFiles:      []string{"vars.yml"},
		NoHooks:       true,
	})

	want := []string{
		"--template-url", "/lib/react-component",
		"--output-folder", "/out",
		"--non-interactive",
		"--var", "category=marketing",
		"--var", "component_name=Hero",
		"--var-file", "vars.yml",
		"--no-hooks",
	}
	assert.Equal(t, want, got)
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

// TestRender is the end-to-end smoke: a minimal blueprint through the real
// boilerplate engine into a tmp dir. Skips when the engine isn't installed,
// so the suite stays portable (CI runners without boilerplate).
func TestRender(t *testing.T) {
	t.Parallel()

	if _, err := blueprint.EnginePath(); err != nil {
		t.Skip("boilerplate engine not installed; skipping integration smoke")
	}

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
