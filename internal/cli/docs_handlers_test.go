package cli_test

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDocsCheckStrictIsFlagNotSubcommand pins the fix for the one orphaned
// handler `lw check schema` reported.
//
// commands.yaml declares `docs check` with `--strict` among its flags — there
// is no `docs check strict` subcommand. Registering a "docs.check.strict" key
// in addition to "docs.check" therefore produced a handler the schema has no
// entry for, which the drift gate reports as an orphan. The handler already
// reads flagBool("strict"), so the flag is the whole interface.
func TestDocsCheckStrictIsFlagNotSubcommand(t *testing.T) {
	t.Parallel()

	_, ok := cli.LookupHandler("docs.check")
	require.True(t, ok, "docs.check must stay registered — it is the schema entry")

	_, orphan := cli.LookupHandler("docs.check.strict")
	assert.False(t, orphan,
		"docs.check.strict must not be registered: --strict is a flag on docs check, "+
			"and the extra key shows up as orphaned-handler drift in `lw check schema`")
}

// TestDocsSchemaVerbsAreRegistered pins the false positives found while auditing
// lightwave-cli#301.
//
// Every verb commands.yaml declares under `docs` must resolve in the handler
// registry, because that registry is what `lw check schema` counts. docs.sync
// and docs.spec-lint worked all along — but only through the hardcoded cobra
// tree in docs.go — so the drift gate reported them as unimplemented. Re-arming
// the gate would have failed CI for two commands that were fine.
//
// system-overview.md documents both as the way docs/ and spec/ work, so a drift
// report calling them missing actively misleads.
func TestDocsSchemaVerbsAreRegistered(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"docs.check",
		"docs.render",
		"docs.serve",
		"docs.spec-lint",
		"docs.sync",
	} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			_, ok := cli.LookupHandler(key)
			assert.True(t, ok,
				"%s is declared in commands.yaml; without a registry entry "+
					"`lw check schema` reports it as a missing handler even when the "+
					"cobra command works", key)
		})
	}
}
