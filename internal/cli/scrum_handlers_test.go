//nolint:testpackage // exercises the unexported handler directly
package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScrumSyncHandler_IsDecommissioned(t *testing.T) {
	t.Parallel()

	err := scrumSyncHandler(context.Background(), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decommissioned")
}
