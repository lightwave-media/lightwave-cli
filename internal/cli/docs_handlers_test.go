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
