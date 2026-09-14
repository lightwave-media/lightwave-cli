//nolint:testpackage // reads the unexported scaffoldCmd
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `lw scaffold --help` told readers the blueprint library resolves to
//
//	<lightwave_root>/src/boilerplate/blueprints
//
// The code has always used `<lightwave_root>/lightwave-core/src/boilerplate/
// blueprints`. The `lightwave-core` segment is easy to drop because the repos
// live flat under ~/dev, so lightwave_root is the WORKSPACE and not the stamp
// repo — the same dissolved-path confusion as #387, in a help string rather
// than in code.
//
// It is only documentation, which is exactly why it went unnoticed: nothing
// fails. A reader following it points --blueprints-dir or $LW_BLUEPRINTS_DIR at
// a directory that does not exist, gets an empty library, and concludes the
// blueprints are missing rather than that the help is wrong. The same wrong
// path also sat in BlueprintsDir's own doc comment, one line above the code
// that gets it right.
//
// So the help text is derived from the function rather than restated beside it.
//
//nolint:paralleltest // t.Setenv is incompatible with t.Parallel
func TestScaffoldHelpMatchesBlueprintsDir(t *testing.T) {
	// A sentinel root: the suffix after it is the shape the help must describe.
	const root = "/SENTINEL"

	t.Setenv(blueprint.EnvBlueprintsDir, "")

	resolved := blueprint.BlueprintsDir(root)
	require.True(t, strings.HasPrefix(resolved, root),
		"BlueprintsDir should build on the root it was given, got %q", resolved)

	suffix := strings.TrimPrefix(resolved, root)

	assert.Contains(t, scaffoldCmd.Long, suffix,
		"`lw scaffold --help` describes the blueprint library as a path the code "+
			"never builds. Resolution is %q, so the help must say "+
			"<lightwave_root>%s — a reader who follows the help points at a "+
			"directory that does not exist and concludes the library is missing",
		resolved, suffix)
}

// TestBlueprintsDirKeepsTheCoreSegment states the invariant the help depends on,
// so a change to BlueprintsDir fails here rather than silently rewriting what
// the test above considers correct.
//
// Without it the pair is self-referential: drop `lightwave-core` from the
// function and the derived-help test would happily agree with the new wrong
// answer.
//
//nolint:paralleltest // t.Setenv is incompatible with t.Parallel
func TestBlueprintsDirKeepsTheCoreSegment(t *testing.T) {
	t.Setenv(blueprint.EnvBlueprintsDir, "")

	assert.Contains(t, blueprint.BlueprintsDir("/SENTINEL"), "/lightwave-core/",
		"the repos live flat under ~/dev, so the workspace root is not the stamp "+
			"repo; dropping this segment points the library at nothing")
}

// TestBlueprintsDirEnvOverrideWins is the other direction — the documented
// escape hatch has to actually work. #351 and the schema-drift task both shipped
// a documented override that could not be reached.
//
//nolint:paralleltest // t.Setenv is incompatible with t.Parallel
func TestBlueprintsDirEnvOverrideWins(t *testing.T) {
	t.Setenv(blueprint.EnvBlueprintsDir, "/elsewhere/blueprints")

	assert.Equal(t, "/elsewhere/blueprints", blueprint.BlueprintsDir("/SENTINEL"),
		"$"+blueprint.EnvBlueprintsDir+" is documented as taking precedence")
}

// TestResolveRejectsAnUnreachableLibrary is the rejection path, and it is the
// one a reader hits after following the wrong help text.
//
// A library dir that does not exist must fail loudly and NAME the directory it
// tried plus the env var that overrides it — otherwise the symptom of a bad
// path is an empty blueprint list, which reads as "the blueprints are missing"
// rather than "you are looking in the wrong place". That misreading is the
// whole cost of the documentation bug above.
func TestResolveRejectsAnUnreachableLibrary(t *testing.T) {
	t.Parallel()

	_, err := blueprint.Resolve(filepath.Join(t.TempDir(), "no-such-library"), "repo-release")
	require.Error(t, err, "an unreachable library must not resolve")

	msg := err.Error()
	assert.Contains(t, msg, "no-such-library", "the error must name the directory it tried")
	assert.Contains(t, msg, blueprint.EnvBlueprintsDir, "and the override that fixes it")
}

// TestResolveRejectsAMissingBlueprint — a name with no manifest is not a
// blueprint, and must not resolve to a directory that happens to exist.
func TestResolveRejectsAMissingBlueprint(t *testing.T) {
	t.Parallel()

	lib := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(lib, "looks-like-one"), 0o755))

	_, err := blueprint.Resolve(lib, "looks-like-one")
	require.Error(t, err, "a directory without a manifest is not a blueprint")

	_, err = blueprint.Resolve(lib, "")
	require.Error(t, err, "an empty name must not resolve to the library root")
}
