//nolint:testpackage // swaps the unexported fetchSecret seam
package infra

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tokenSentinel = "sentinel-value-5e02"

// withFetchSecret swaps the SSM read for a fake and counts its calls.
func withFetchSecret(t *testing.T, value string, err error) *int {
	t.Helper()

	calls := 0
	prev := fetchSecret
	t.Cleanup(func() { fetchSecret = prev })

	fetchSecret = func(_ context.Context, key string) (string, error) {
		calls++
		assert.Equal(t, cloudflareTokenKey, key)

		return value, err
	}

	return &calls
}

func unitDir(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "terragrunt.hcl"), []byte(body), 0o600))

	return dir
}

func tokenValues(env []string) []string {
	var values []string

	for _, kv := range env {
		if key, value, _ := strings.Cut(kv, "="); key == cloudflareTokenKey {
			values = append(values, value)
		}
	}

	return values
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestTerragruntEnvReadsTheTokenByNameForACloudflareUnit(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "")
	calls := withFetchSecret(t, tokenSentinel, nil)
	dir := unitDir(t, `terraform { source = "git::...//modules/cloudflare-dns-zone?ref=v0.6.0" }`)

	env := terragruntEnv(t.Context(), dir, false)

	assert.Equal(t, 1, *calls)
	assert.Contains(t, tokenValues(env), tokenSentinel)
	assert.Contains(t, env, "TF_IN_AUTOMATION=1")
	assert.Empty(t, os.Getenv(cloudflareTokenKey), "the token goes to terragrunt, never into lw's own environment")
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestTerragruntEnvReadsNothingForAnAWSOnlyUnit(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "")
	calls := withFetchSecret(t, tokenSentinel, nil)
	dir := unitDir(t, `terraform { source = "git::...//modules/vpc?ref=v0.6.0" }`)

	env := terragruntEnv(t.Context(), dir, false)

	assert.Zero(t, *calls)
	assert.NotContains(t, tokenValues(env), tokenSentinel)
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestTerragruntEnvKeepsATokenTheCallerSupplied(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "caller-supplied")
	calls := withFetchSecret(t, tokenSentinel, nil)

	env := terragruntEnv(t.Context(), t.TempDir(), true)

	assert.Zero(t, *calls)
	assert.Equal(t, []string{"caller-supplied"}, tokenValues(env))
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestTerragruntEnvReadsTheTokenForRunAll(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "")
	calls := withFetchSecret(t, tokenSentinel, nil)

	env := terragruntEnv(t.Context(), t.TempDir(), true)

	assert.Equal(t, 1, *calls)
	assert.Contains(t, tokenValues(env), tokenSentinel)
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestTerragruntEnvGoesAheadWithoutTheTokenWhenSSMFails(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "")
	withFetchSecret(t, "", errors.New("AccessDeniedException"))
	dir := unitDir(t, "# Cloudflare unit\n")

	env := terragruntEnv(t.Context(), dir, false)

	assert.Equal(t, []string{""}, tokenValues(env), "only the inherited empty value, nothing fetched")
	assert.Contains(t, env, "TF_IN_AUTOMATION=1")
}
