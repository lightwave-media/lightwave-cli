//nolint:testpackage // exercises resolveCluster/resolveLogGroup, both unexported
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realCluster and realLogGroup are the names that exist in AWS today, created by
// prod/us-east-1/lightwave-platform (lightwave-infrastructure-live#72).
const (
	realCluster  = "lightwave-platform"
	envProd      = "prod"
	envStaging   = "staging"
	realLogGroup = "/ecs/" + realCluster
)

// #368: `lw deploy` derived its cluster as `platform-<env>`, the Django-era
// name. The cluster that exists is `lightwave-platform`, so run, status, logs
// and rollback — all four share the helper — failed with
// ClusterNotFoundException against a cluster that no longer exists.
//
// The lesson these pin is that a naming convention cannot be corrected once the
// thing it names stops following it. Configuration can, so these assert the
// precedence rather than a formula.

// TestNoHandlerDerivesTheDjangoEraClusterName is the class guard.
//
// #368 was filed against `lw deploy` and fixed there, and the identical
// derivation survived in `lw check ecs` — a live, help-listed verb that went on
// querying a cluster that does not exist while `lw deploy status` against the
// same environment worked. The deploy package comment even cited "`lw check
// ecs` conventions" as precedent for the name, so the fix had a pointer to the
// other site and still missed it.
//
// Per-site fixes leave siblings behind. This fails if a third appears.
func TestNoHandlerDerivesTheDjangoEraClusterName(t *testing.T) {
	t.Parallel()

	root := repoRootForClusterScan(t)

	var offenders []string

	for _, rel := range trackedGoSources(t, root) {
		src, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // a git-tracked path in our own repo
		if err != nil {
			continue // tracked but absent from the worktree
		}

		for _, line := range strings.Split(string(src), "\n") {
			// Comments recording the history are the point of the fix, and the
			// test names the string on purpose.
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "//") {
				continue
			}

			if strings.Contains(line, `"platform-" + env`) || strings.Contains(line, `"platform-"+env`) {
				offenders = append(offenders, rel+": "+strings.TrimSpace(line))
			}
		}
	}

	assert.Empty(t, offenders,
		"the cluster is resolved, not derived — use deployClusterFor. "+
			"A derived name cannot be corrected once the thing it names stops "+
			"following the convention (#368): %v", offenders)
}

func repoRootForClusterScan(t *testing.T) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel").Output()
	require.NoError(t, err, "git rev-parse --show-toplevel")

	return strings.TrimSpace(string(out))
}

// trackedGoSources lists the .go files git tracks. git rather than a filesystem
// walk, for the reason #404 had to be reverted: a walk descends into nested
// worktrees and lints another session's checkout.
func trackedGoSources(t *testing.T, root string) []string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", root, "ls-files", "-z", "*.go").Output()
	require.NoError(t, err, "git ls-files")

	var files []string

	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" && !strings.HasSuffix(f, "deploy_target_test.go") {
			files = append(files, f)
		}
	}

	require.NotEmpty(t, files, "git tracks no .go files — the scan would vacuously pass")

	return files
}

func TestResolveCluster(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dc   config.DeployConfig
		env  string
		want string
	}{
		{
			name: "per-env map wins over the single value",
			dc: config.DeployConfig{
				Clusters: map[string]string{envProd: realCluster, envStaging: "lw-staging"},
				Cluster:  "fallback-cluster",
			},
			env:  envStaging,
			want: "lw-staging",
		},
		{
			name: "single value when the env has no entry",
			dc: config.DeployConfig{
				Clusters: map[string]string{envProd: realCluster},
				Cluster:  "fallback-cluster",
			},
			env:  envStaging,
			want: "fallback-cluster",
		},
		{
			name: "an empty map entry does not shadow the single value",
			dc: config.DeployConfig{
				Clusters: map[string]string{envProd: ""},
				Cluster:  realCluster,
			},
			env:  envProd,
			want: realCluster,
		},
		{
			name: "nothing configured falls back to the env name, not to platform-<env>",
			dc:   config.DeployConfig{},
			env:  envProd,
			want: envProd,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := resolveCluster(tc.dc, tc.env)
			assert.Equal(t, tc.want, got)
			assert.NotEqual(t, "platform-"+tc.env, got,
				"the Django-era convention must not come back (#368)")
		})
	}
}

func TestResolveLogGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dc   config.DeployConfig
		env  string
		want string
	}{
		{
			name: "per-env map wins",
			dc: config.DeployConfig{
				LogGroups: map[string]string{envProd: realLogGroup},
				LogGroup:  "/ecs/other",
			},
			env:  envProd,
			want: realLogGroup,
		},
		{
			name: "single value when the env has no entry",
			dc:   config.DeployConfig{LogGroup: realLogGroup},
			env:  envProd,
			want: realLogGroup,
		},
		{
			name: "derives from the resolved cluster, with no service suffix",
			dc:   config.DeployConfig{Cluster: realCluster},
			env:  envProd,
			want: realLogGroup,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := resolveLogGroup(tc.dc, tc.env, realCluster)
			assert.Equal(t, tc.want, got)
			assert.NotContains(t, got, "-lightwave-platform",
				"the old /ecs/<cluster>-<service> shape appended the service; the real group does not")
		})
	}
}

// TestDeployDefaultTargetsTheClusterThatExists pins the shipped default.
//
// Without this, the default could drift back to something plausible-looking and
// nothing would notice until a deploy verb failed against AWS — the exact
// failure mode #368 reports, which took a live ClusterNotFoundException to find.
func TestDeployDefaultTargetsTheClusterThatExists(t *testing.T) {
	t.Parallel()

	cfg := config.Get()
	if cfg == nil {
		t.Skip("config not loaded")
	}

	assert.Equal(t, realCluster, cfg.Deploy.Cluster,
		"the default must name the cluster that prod/us-east-1/lightwave-platform creates")
}
