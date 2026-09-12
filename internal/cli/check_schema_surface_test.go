//nolint:testpackage // exercises cobraSurfaceKeys, which is unexported
package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// The third drift direction (#337): commands that are invocable but absent from
// the stamp. The registry-only check could not see them — `lw check schema`
// reported "✓ no drift" at 100% coverage while 32 working commands were
// unstamped, because they were attached with AddCommand and never entered the
// handler registry.
//
// These tests drive cobraSurfaceKeys against hand-built trees rather than the
// live surface. The live tree is assembled once per process and depends on a
// lightwave-core checkout, so asserting against it would make the tests skip on
// exactly the machines that need them — the failure mode #387 was about.

func newCmd(name string, runnable bool) *cobra.Command {
	c := &cobra.Command{Use: name}
	if runnable {
		c.RunE = func(*cobra.Command, []string) error { return nil }
	}

	return c
}

func TestCobraSurfaceKeys_CollectsNestedLeaves(t *testing.T) {
	t.Parallel()

	root := newCmd("lw", false)

	db := newCmd("db", false)
	db.AddCommand(newCmd("migrate", true), newCmd("shell", true))

	config := newCmd("config", false)
	harness := newCmd("harness", false)
	harness.AddCommand(newCmd("render", true), newCmd("apply", true))
	config.AddCommand(harness, newCmd("show", true))

	root.AddCommand(db, config, newCmd("health", true))

	assert.Equal(t, []string{
		"config.harness.apply",
		"config.harness.render",
		"config.show",
		"db.migrate",
		"db.shell",
		"health",
	}, cobraSurfaceKeys(root), "three levels deep, sorted, leaves only")
}

// TestCobraSurfaceKeys_SkipsRunnableGroups pins the correction that took the
// first measurement from 75 to 32.
//
// cobra reports a group as Runnable() when it has a Run that prints help, so a
// naive Runnable() test counted every bare domain — `lw db`, `lw deploy` — as an
// unstamped command. The stamp models those as domains, never as commands, so
// the reported cure would have been to invent entries that must not exist. A
// gate whose remedy is wrong is worse than no gate.
func TestCobraSurfaceKeys_SkipsRunnableGroups(t *testing.T) {
	t.Parallel()

	root := newCmd("lw", false)

	// Runnable AND carrying children — the shape that produced the 43 phantoms.
	db := newCmd("db", true)
	db.AddCommand(newCmd("migrate", true))
	root.AddCommand(db)

	got := cobraSurfaceKeys(root)

	assert.Equal(t, []string{"db.migrate"}, got)
	assert.NotContains(t, got, "db", "a group is a domain, not a command")
}

func TestCobraSurfaceKeys_SkipsGeneratedAndHidden(t *testing.T) {
	t.Parallel()

	root := newCmd("lw", false)
	root.AddCommand(newCmd("help", true), newCmd("completion", true), newCmd("real", true))

	hidden := newCmd("secret", true)
	hidden.Hidden = true
	root.AddCommand(hidden)

	assert.Equal(t, []string{"real"}, cobraSurfaceKeys(root),
		"cobra's generated commands and hidden ones are not lw surface")
}

func TestCobraSurfaceKeys_NilRootIsEmptyNotPanic(t *testing.T) {
	t.Parallel()

	assert.Empty(t, cobraSurfaceKeys(nil))
}

// TestUnstampedBaselineIsRatchetNotThreshold documents the intent in an
// executable form: the constant exists to catch GROWTH, so it must sit at the
// measured count, not at zero (which would block every PR until 32 commands are
// stamped) and not far above it (which would let new drift in unnoticed).
func TestUnstampedBaselineIsRatchetNotThreshold(t *testing.T) {
	t.Parallel()

	assert.Positive(t, unstampedBaseline,
		"zero would make the gate's first act to fail every PR, and the reliable "+
			"outcome of that is the gate being switched off")
	assert.LessOrEqual(t, unstampedBaseline, 40,
		"the baseline is a debt marker; if it climbs, the ratchet stopped ratcheting")
}
