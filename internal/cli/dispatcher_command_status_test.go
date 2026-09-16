//nolint:testpackage // needs attachDomainCommand and findChild, both unexported
package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/sst"
)

// attachCheckSchema drives the dispatcher against a REAL registered handler,
// so `_status` is the only variable between the cases.
//
// A synthetic key would have to be registered, and the registry has no
// unregister — a leftover test key would show up in RegisteredKeys() and make
// `lw check schema` report a permanent orphan. Reusing `check.schema` keeps the
// registry untouched.
func attachCheckSchema(t *testing.T, status string) (*cobra.Command, int) {
	t.Helper()

	for _, key := range []string{"check.schema", "check.git"} {
		_, ok := LookupHandler(key)
		require.True(t, ok, "the fixture depends on %s existing", key)
	}

	parent := &cobra.Command{Use: "check"}

	// A published sibling, so the fixture is a real domain rather than a
	// childless parent. cobra only refuses an unknown subcommand on a command
	// that has subcommands — without this the refusal assertion below passes
	// vacuously, which is the shape of test that proves nothing.
	sibling := sst.CLICommand{Name: "git"}
	require.Equal(t, 1, attachDomainCommand(parent, "check", "", &sibling))

	cmd := sst.CLICommand{Name: "schema", Status: status}

	return parent, attachDomainCommand(parent, "check", "", &cmd)
}

// TestInDevelopmentCommandStaysHiddenOnceItsHandlerExists is the half of this
// mechanism that is not free.
//
// Before a handler is registered, the LookupHandler miss hides the command
// anyway — so the status looks like it works while doing nothing. The moment
// the handler lands, which is the whole point of declaring the verb early, a
// release binary would start listing an unproven command.
//
//nolint:paralleltest // reads LW_CLI_DEV_DOMAINS via t.Setenv
func TestInDevelopmentCommandStaysHiddenOnceItsHandlerExists(t *testing.T) {
	t.Setenv("LW_CLI_DEV_DOMAINS", "")

	parent, attached := attachCheckSchema(t, sst.StatusInDevelopment)

	assert.Zero(t, attached, "a registered handler does not publish an in_development verb")
	assert.Nil(t, findChild(parent, "schema"))

	// The refusal a user actually meets. An absent child is the tree shape;
	// this is cobra resolving the invocation and declining it, which is the
	// thing the status is supposed to produce.
	_, _, err := parent.Find([]string{"schema"})
	require.Error(t, err, "`lw check schema` does not resolve while the verb is in_development")
	assert.Contains(t, err.Error(), "unknown command")
}

// TestPublishedCommandWithTheSameHandlerIsAttached is the control. Without it
// the test above passes for any reason at all, including the handler having
// gone missing.
//
//nolint:paralleltest // reads LW_CLI_DEV_DOMAINS via t.Setenv
func TestPublishedCommandWithTheSameHandlerIsAttached(t *testing.T) {
	t.Setenv("LW_CLI_DEV_DOMAINS", "")

	parent, attached := attachCheckSchema(t, "")

	require.Equal(t, 1, attached, "same command, no status — it ships")
	assert.NotNil(t, findChild(parent, "schema"))
}

// TestDevDomainsEnabledRevealsTheInDevelopmentCommand — the escape hatch is
// what makes building the verb locally possible at all.
//
//nolint:paralleltest // reads LW_CLI_DEV_DOMAINS via t.Setenv
func TestDevDomainsEnabledRevealsTheInDevelopmentCommand(t *testing.T) {
	t.Setenv("LW_CLI_DEV_DOMAINS", "1")

	parent, attached := attachCheckSchema(t, sst.StatusInDevelopment)

	require.Equal(t, 1, attached)
	assert.NotNil(t, findChild(parent, "schema"))
}
