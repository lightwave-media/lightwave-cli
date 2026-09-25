//nolint:testpackage // swaps the unexported fetchSecretByName seam
package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
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

	assert.Equal(t, nullticketsSentinel, nullticketsToken(t.Context(), false))
	assert.Equal(t, 1, *calls)
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestNullticketsTokenPrefersTheEnvironment(t *testing.T) {
	t.Setenv(nullticketsTokenKey, "from-env")
	calls := withSecretByName(t, nullticketsSentinel, nil)

	assert.Equal(t, "from-env", nullticketsToken(t.Context(), false))
	assert.Zero(t, *calls)
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestNullticketsTokenReadsNothingForADryRun(t *testing.T) {
	t.Setenv(nullticketsTokenKey, "")
	calls := withSecretByName(t, nullticketsSentinel, nil)

	assert.Empty(t, nullticketsToken(t.Context(), true))
	assert.Zero(t, *calls)
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestNullticketsTokenGoesOnWithoutABearerWhenSSMFails(t *testing.T) {
	t.Setenv(nullticketsTokenKey, "")
	calls := withSecretByName(t, "", errors.New("not in /lightwave/prod/: NULLTICKETS_API_TOKEN"))

	assert.Empty(t, nullticketsToken(t.Context(), false))
	assert.Equal(t, 1, *calls)
}
