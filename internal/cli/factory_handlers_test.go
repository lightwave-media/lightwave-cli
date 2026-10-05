//nolint:testpackage // exercises the unexported manifest step dispatcher
package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A manifest must not be able to run the board sync that `lw scrum sync` no
// longer offers.
func TestDispatchFactoryStep_BoardSyncKindsAreDecommissioned(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"scrum-sync", "project-board-hygiene"} {
		err := dispatchFactoryStep(context.Background(), &manifestStep{ID: "s1", Kind: kind}, nil)
		require.Errorf(t, err, "manifest kind %q must refuse to run", kind)
		assert.Contains(t, err.Error(), "decommissioned")
	}
}
