package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/testutil"
)

// RunHandler swaps os.Stdout globally, so every test here is serial.

//nolint:paralleltest // RunHandler swaps os.Stdout globally.
func TestDeployRunDryRunImageRollout(t *testing.T) {
	out, err := testutil.RunHandler(t, "deploy.run",
		[]string{"prod"},
		map[string]any{"service": "lightwave-platform", "image": "ecr/lightwave-platform:abc123", "dry-run": true})

	require.NoError(t, err)
	assert.Contains(t, out, "ecr/lightwave-platform:abc123")
	assert.Contains(t, out, "lightwave-platform")
}

//nolint:paralleltest // t.Setenv forbids t.Parallel; we pin LW_DEPLOY_IMAGE empty for determinism.
func TestDeployRunDryRunForceRedeploy(t *testing.T) {
	t.Setenv("LW_DEPLOY_IMAGE", "")

	out, err := testutil.RunHandler(t, "deploy.run",
		[]string{"prod"},
		map[string]any{"service": "lightwave-platform", "dry-run": true})

	require.NoError(t, err)
	assert.Contains(t, out, "force new deployment")
}

//nolint:paralleltest // t.Setenv forbids t.Parallel; RunHandler swaps os.Stdout.
func TestDeployRunDryRunImageFromEnv(t *testing.T) {
	t.Setenv("LW_DEPLOY_IMAGE", "ecr/lightwave-platform:from-env")

	out, err := testutil.RunHandler(t, "deploy.run",
		[]string{"prod"},
		map[string]any{"service": "lightwave-platform", "dry-run": true})

	require.NoError(t, err)
	assert.Contains(t, out, "from-env")
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally.
func TestDeployRunRequiresService(t *testing.T) {
	_, err := testutil.RunHandler(t, "deploy.run", []string{"prod"}, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--service is required")
}

//nolint:paralleltest // RunHandler swaps os.Stdout globally.
func TestDeployRunRequiresEnv(t *testing.T) {
	_, err := testutil.RunHandler(t, "deploy.run", []string{}, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage")
}
