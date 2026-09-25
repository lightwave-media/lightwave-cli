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

const (
	tokenSentinel    = "sentinel-value-5e02"
	cloudflareSource = `terraform { source = "git::git@github.com:lightwave-media/lightwave-infrastructure-catalog.git//modules/cloudflare-dns-zone?ref=v0.6.0" }`
	awsSource        = `terraform { source = "git::git@github.com:lightwave-media/lightwave-infrastructure-catalog.git//modules/vpc?ref=v0.6.0" }`
	// github-actions-oidc names the key in an IAM policy but has no Cloudflare
	// resources, so it must not get the token.
	oidcBody = `terraform { source = "git::git@github.com:lightwave-media/lightwave-infrastructure-catalog.git//modules/github-actions-oidc?ref=v0.5.0" }
inputs = { ssm_resources = ["${local.ssm_prefix}/CLOUDFLARE_API_TOKEN"] }`
)

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

func writeUnitBody(t *testing.T, dir, body string) string {
	t.Helper()

	require.NoError(t, os.MkdirAll(dir, 0o755))
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
	dir := writeUnitBody(t, t.TempDir(), cloudflareSource)

	env, err := terragruntEnv(t.Context(), []string{dir}, false)

	require.NoError(t, err)
	assert.Equal(t, 1, *calls)
	assert.Contains(t, tokenValues(env), tokenSentinel)
	assert.Contains(t, env, "TF_IN_AUTOMATION=1")
	assert.Empty(t, os.Getenv(cloudflareTokenKey), "the token goes to terragrunt, never into lw's own environment")
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestTerragruntEnvReadsNothingForUnitsWithoutACloudflareModule(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "")
	calls := withFetchSecret(t, tokenSentinel, nil)
	root := t.TempDir()
	aws := writeUnitBody(t, filepath.Join(root, "vpc"), awsSource)
	oidc := writeUnitBody(t, filepath.Join(root, "github-actions-oidc"), oidcBody)

	env, err := terragruntEnv(t.Context(), []string{aws, oidc}, true)

	require.NoError(t, err)
	assert.Zero(t, *calls)
	assert.NotContains(t, tokenValues(env), tokenSentinel)
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestTerragruntEnvKeepsATokenTheCallerSupplied(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "caller-supplied")
	calls := withFetchSecret(t, tokenSentinel, nil)
	dir := writeUnitBody(t, t.TempDir(), cloudflareSource)

	env, err := terragruntEnv(t.Context(), []string{dir}, true)

	require.NoError(t, err)
	assert.Zero(t, *calls)
	assert.Equal(t, []string{"caller-supplied"}, tokenValues(env))
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestTerragruntEnvRefusesAMutatingRunWhenTheReadFails(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "")
	withFetchSecret(t, "", errors.New("AccessDeniedException"))
	dir := writeUnitBody(t, t.TempDir(), cloudflareSource)

	_, err := terragruntEnv(t.Context(), []string{dir}, true)

	require.ErrorContains(t, err, cloudflareTokenKey)
	require.ErrorContains(t, err, "AccessDeniedException")
}

//nolint:paralleltest // swaps a package-level seam and the environment
func TestTerragruntEnvLetsAPlanGoAheadWhenTheReadFails(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "")
	withFetchSecret(t, "", errors.New("AccessDeniedException"))
	dir := writeUnitBody(t, t.TempDir(), cloudflareSource)

	env, err := terragruntEnv(t.Context(), []string{dir}, false)

	require.NoError(t, err)
	assert.Equal(t, []string{""}, tokenValues(env), "only the inherited empty value, nothing fetched")
}

func TestUnitDirsSkipsTerragruntCaches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeUnitBody(t, filepath.Join(root, "a"), awsSource)
	writeUnitBody(t, filepath.Join(root, "a", ".terragrunt-cache", "x"), cloudflareSource)
	writeUnitBody(t, filepath.Join(root, "b", ".terragrunt-stack", "y"), cloudflareSource)

	assert.Equal(t, []string{filepath.Join(root, "a")}, unitDirs(root))
}

// fakeTerragrunt puts a terragrunt on PATH that records, as yes or no, whether
// it received the sentinel token. It compares; it never writes the value.
func fakeTerragrunt(t *testing.T) string {
	t.Helper()

	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "got-token")
	script := "#!/bin/sh\n" +
		`if [ "${CLOUDFLARE_API_TOKEN:-}" = "` + tokenSentinel + `" ]; then echo yes; else echo no; fi > '` + record + "'\n" +
		"echo 'No changes.'\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "terragrunt"), []byte(script), 0o755)) //nolint:gosec // a test executable
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	return record
}

//nolint:paralleltest // swaps a package-level seam, PATH and the environment
func TestPlanGivesTheTokenToTerragrunt(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "")
	withFetchSecret(t, tokenSentinel, nil)
	record := fakeTerragrunt(t)
	root := t.TempDir()
	writeUnitBody(t, filepath.Join(root, "prod", "us-west-2", "zone"), cloudflareSource)

	_, err := NewTerragruntRunner(root, "prod", "us-west-2").Plan(t.Context(), "zone")

	require.NoError(t, err)
	got, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.Equal(t, "yes\n", string(got))
}

//nolint:paralleltest // swaps a package-level seam, PATH and the environment
func TestRunAllApplyStopsBeforeTerragruntWhenTheReadFails(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "")
	withFetchSecret(t, "", errors.New("AccessDeniedException"))
	record := fakeTerragrunt(t)
	root := t.TempDir()
	writeUnitBody(t, filepath.Join(root, "prod", "us-west-2", "zone"), cloudflareSource)

	err := NewTerragruntRunner(root, "prod", "us-west-2").RunAll(t.Context(), "apply")

	require.ErrorContains(t, err, cloudflareTokenKey)
	assert.NoFileExists(t, record, "terragrunt never started, so no unit was applied")
}

//nolint:paralleltest // swaps a package-level seam, PATH and the environment
func TestRunAllValidateReadsNothing(t *testing.T) {
	t.Setenv(cloudflareTokenKey, "")
	calls := withFetchSecret(t, tokenSentinel, nil)
	fakeTerragrunt(t)
	root := t.TempDir()
	writeUnitBody(t, filepath.Join(root, "prod", "us-west-2", "zone"), cloudflareSource)

	require.NoError(t, NewTerragruntRunner(root, "prod", "us-west-2").RunAll(t.Context(), "validate"))

	assert.Zero(t, *calls, "validate configures no provider")
}
