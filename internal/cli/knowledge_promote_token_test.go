//nolint:testpackage // swaps the unexported fetchSecretByName seam
package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const nullticketsSentinel = "sentinel-value-a81d"

func withSecretByName(t *testing.T, value string, err error) *int {
	t.Helper()

	calls := 0
	prev := fetchSecretByName
	t.Cleanup(func() { fetchSecretByName = prev })

	fetchSecretByName = func(_ context.Context, key string) (string, error) {
		calls++
		assert.Equal(t, nullticketsTokenKey, key)

		return value, err
	}

	return &calls
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestNullticketsTokenReadsByNameWhenTheSessionHasNone(t *testing.T) {
	t.Setenv(nullticketsTokenKey, "")
	calls := withSecretByName(t, nullticketsSentinel, nil)

	token, err := nullticketsToken(t.Context(), false)
	require.NoError(t, err)
	assert.Equal(t, nullticketsSentinel, token)
	assert.Equal(t, 1, *calls)
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestNullticketsTokenPrefersTheEnvironment(t *testing.T) {
	t.Setenv(nullticketsTokenKey, "from-env")
	calls := withSecretByName(t, nullticketsSentinel, nil)

	token, err := nullticketsToken(t.Context(), false)
	require.NoError(t, err)
	assert.Equal(t, "from-env", token)
	assert.Zero(t, *calls)
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestNullticketsTokenReadsNothingForADryRun(t *testing.T) {
	t.Setenv(nullticketsTokenKey, "")
	calls := withSecretByName(t, nullticketsSentinel, nil)

	token, err := nullticketsToken(t.Context(), true)
	require.NoError(t, err)
	assert.Empty(t, token)
	assert.Zero(t, *calls)
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestNullticketsTokenRefusesByNameWhenSSMFails(t *testing.T) {
	t.Setenv(nullticketsTokenKey, "")
	calls := withSecretByName(t, "", errors.New("AccessDeniedException"))

	token, err := nullticketsToken(t.Context(), false)

	require.ErrorContains(t, err, nullticketsTokenKey)
	require.ErrorContains(t, err, "AccessDeniedException")
	assert.Empty(t, token)
	assert.Equal(t, 1, *calls)
}
