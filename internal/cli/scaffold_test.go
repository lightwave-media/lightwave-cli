//nolint:testpackage // drives the unexported scaffold cobra command directly
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeBlueprintLibrary builds a minimal boilerplate library on disk:
//
//	<root>/blueprints/__index.yaml   slug -> dir
//	<root>/blueprints/mini/          the blueprint itself
//	<root>/templates/__index.yaml    family -> slug -> path
//
// Returns the blueprints/ dir, which is what --blueprints-dir takes.
func writeBlueprintLibrary(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	bpDir := filepath.Join(root, "blueprints")
	mini := filepath.Join(bpDir, "mini")

	require.NoError(t, os.MkdirAll(mini, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "templates"), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(mini, "boilerplate.yml"),
		[]byte("variables:\n  - name: component_name\n    type: string\n    default: Hero\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(mini, "Component.tsx"),
		[]byte("export const {{ .component_name }} = () => null;\n"), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(bpDir, "__index.yaml"),
		[]byte("blueprints:\n  mini: mini\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "templates", "__index.yaml"),
		[]byte("templates: {}\n"), 0o600))

	return bpDir
}

func resetScaffoldFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		scaffoldVars, scaffoldVarFiles = nil, nil
		scaffoldOutput, scaffoldBlueprints = "", ""
		scaffoldNoHooks, scaffoldForce, scaffoldList = false, false, false
	})
}

// `lw scaffold` was demoted from VerifiedCommands because nothing tested it.
// --blueprints-dir bypasses the config singleton, and the boilerplate engine is
// now linked in rather than found on PATH (#355), so the whole path is
// reachable in-process: resolve a blueprint, render it, write real files.
//
//nolint:paralleltest // mutates the shared scaffold* flag globals
func TestScaffoldCmd_RendersBlueprintIntoOutputFolder(t *testing.T) {
	resetScaffoldFlags(t)

	lib := writeBlueprintLibrary(t)
	out := t.TempDir()

	scaffoldBlueprints = lib
	scaffoldOutput = out
	scaffoldVars = []string{"component_name=Banner"}

	require.NoError(t, runScaffold(&cobra.Command{}, []string{"mini"}), "lw scaffold mini")

	got, err := os.ReadFile(filepath.Join(out, "Component.tsx"))
	require.NoError(t, err, "blueprint should have rendered a component")
	assert.Equal(t, "export const Banner = () => null;\n", string(got),
		"--var must reach the engine, not just the blueprint default")
}

// The collision guard is the reason Render stages before committing: a
// blueprint must not silently clobber files in an existing tree.
//
//nolint:paralleltest // mutates the shared scaffold* flag globals
func TestScaffoldCmd_RefusesToClobberWithoutForce(t *testing.T) {
	resetScaffoldFlags(t)

	lib := writeBlueprintLibrary(t)
	out := t.TempDir()

	existing := filepath.Join(out, "Component.tsx")
	require.NoError(t, os.WriteFile(existing, []byte("DO NOT LOSE ME\n"), 0o600))

	scaffoldBlueprints = lib
	scaffoldOutput = out

	err := runScaffold(&cobra.Command{}, []string{"mini"})
	require.Error(t, err, "must refuse to overwrite an existing file")
	assert.Contains(t, err.Error(), "refusing to overwrite")

	survived, readErr := os.ReadFile(existing)
	require.NoError(t, readErr)
	assert.Equal(t, "DO NOT LOSE ME\n", string(survived), "the existing file must be untouched")

	// ...and --force goes through.
	scaffoldForce = true
	require.NoError(t, runScaffold(&cobra.Command{}, []string{"mini"}), "--force should overwrite")

	overwritten, readErr := os.ReadFile(existing)
	require.NoError(t, readErr)
	assert.Contains(t, string(overwritten), "export const Hero")
}

// --blueprints-dir is documented as overriding the library location. The list
// path ignored it and read the config's lightwave root instead, so --list could
// describe a different library than the one a render would use.
//
//nolint:paralleltest // mutates the shared scaffold* flag globals
func TestScaffoldList_HonorsBlueprintsDirOverride(t *testing.T) {
	resetScaffoldFlags(t)

	lib := writeBlueprintLibrary(t)

	entries, err := listCatalog(lib)
	require.NoError(t, err, "listing must use the overridden library, not config")
	require.Len(t, entries, 1)
	assert.Equal(t, "mini", entries[0].Slug)
	assert.Equal(t, "blueprint", entries[0].Kind)
}

//nolint:paralleltest // mutates the shared scaffold* flag globals
func TestScaffoldCmd_UnknownBlueprintErrors(t *testing.T) {
	resetScaffoldFlags(t)

	scaffoldBlueprints = writeBlueprintLibrary(t)
	scaffoldOutput = t.TempDir()

	err := runScaffold(&cobra.Command{}, []string{"does-not-exist"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does-not-exist")
}
