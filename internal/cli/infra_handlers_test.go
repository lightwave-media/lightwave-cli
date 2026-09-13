//nolint:testpackage // exercises unexported filterUnits and infraStatusHandler
package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #367. --env/--region were read by the handler and declared in no schema, so
// the dispatcher never registered them and their defaults (prod / us-east-1)
// were the only reachable values. Now that they are stamped they are filters
// over a listing that covers the whole repo, so the property to pin is that an
// ABSENT filter widens rather than narrows.

func TestFilterUnits(t *testing.T) {
	t.Parallel()

	all := []string{
		"dev/us-east-1/sandbox",
		"prod/us-east-1/rds",
		"prod/us-east-1/vpc",
		"prod/us-west-2/cineos-io",
	}

	cases := map[string]struct {
		env, region string
		want        []string
	}{
		// The regression: no flags must mean everything, not prod/us-east-1.
		"no filters shows every tree": {"", "", all},

		"env only":    {envProd, "", []string{"prod/us-east-1/rds", "prod/us-east-1/vpc", "prod/us-west-2/cineos-io"}},
		"region only": {"", "us-west-2", []string{"prod/us-west-2/cineos-io"}},
		"both":        {envProd, "us-west-2", []string{"prod/us-west-2/cineos-io"}},

		// A filter that matches nothing must return nothing rather than falling
		// back to everything — silently ignoring an unmatched filter would put
		// the caller back where #367 started, reading a listing that does not
		// answer the question they asked.
		"unmatched env":    {"staging", "", nil},
		"unmatched region": {envProd, "eu-west-1", nil},
	}

	for name, testCase := range cases {
		tt := testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := filterUnits(all, tt.env, tt.region)
			if tt.want == nil {
				assert.Empty(t, got)

				return
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

// TestFilterUnits_IgnoresShallowPaths guards against a path with no region
// segment being matched on its env alone and reported as if it were a unit.
func TestFilterUnits_IgnoresShallowPaths(t *testing.T) {
	t.Parallel()

	assert.Empty(t, filterUnits([]string{envProd}, envProd, ""),
		"a path with no <region> segment is not a unit")
}

// TestInfraStatusDoesNotPointAtADecommissionedCommand is the rejection path and
// the more interesting half of #367.
//
// The handler demanded `<domain-id>`, which the stamp does not declare — so the
// dispatcher passed none and every call died on the usage line, a usage error
// for an argument no caller could have supplied, for a command that refuses
// regardless. It then sent the reader to `lw aws ecs status`, and `aws` is
// decommissioned. A pointer to a plausible-sounding dead command is worse than
// admitting the gap, because the reader spends their time on the wrong thing.
func TestInfraStatusDoesNotPointAtADecommissionedCommand(t *testing.T) {
	t.Parallel()

	err := infraStatusHandler(t.Context(), nil, map[string]any{})
	require.Error(t, err, "an unimplemented verb must not report success")

	msg := err.Error()

	assert.NotContains(t, msg, "usage:",
		"it must not demand an argument the stamp does not declare")
	assert.NotContains(t, msg, "lw aws",
		"aws is decommissioned; naming it sends the reader to an offline command")
	assert.Contains(t, msg, "lw deploy status",
		"the error must name a command that actually ships")

	// The decommission list is the authority, not this test's memory of it.
	_, awsOffline := DecommissionedCommands["aws"]
	assert.True(t, awsOffline,
		"if aws is brought back online, the guard above stops being meaningful "+
			"and this test should be revisited rather than deleted")
}
