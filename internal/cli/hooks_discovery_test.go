//nolint:testpackage // exercises unexported hook-discovery helpers
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveHooksDir pins lightwave-cli#300.
//
// git honours an absolute core.hooksPath as-is. Joining it onto the checkout
// produced <checkout>/<abs-path>, which never exists, so every hook stat'd as
// missing and `lw git doctor` failed every worktree of a repo that sets an
// absolute hooksPath — while the hooks themselves demonstrably ran.
func TestResolveHooksDir(t *testing.T) {
	t.Parallel()

	const checkout = "/repo/.worktrees/wt"

	tests := []struct {
		name      string
		hooksPath string
		want      string
	}{
		{
			name:      "absolute hooksPath is honoured as-is",
			hooksPath: "/repo/dev/hooks",
			want:      "/repo/dev/hooks",
		},
		{
			name:      "relative hooksPath joins against the checkout",
			hooksPath: "dev/hooks",
			want:      filepath.Join(checkout, "dev", "hooks"),
		},
		{
			name:      "unset hooksPath falls back to .git/hooks",
			hooksPath: "",
			want:      filepath.Join(checkout, ".git", "hooks"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, resolveHooksDir(checkout, tt.hooksPath))
		})
	}
}

// TestListInstalledHooksAbsolutePath is the end-to-end form of #300: a hooks
// directory that lives outside the checkout (as it does for every linked
// worktree) must still be found.
func TestListInstalledHooksAbsolutePath(t *testing.T) {
	t.Parallel()

	// Hooks live here; the "worktree" below is a sibling, not a parent.
	hooksDir := filepath.Join(t.TempDir(), "dev", "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o755))

	for _, h := range []string{"pre-commit", "pre-push", "commit-msg"} {
		require.NoError(t, os.WriteFile(filepath.Join(hooksDir, h), []byte("#!/bin/sh\n"), 0o755))
	}

	worktree := t.TempDir()

	got := listInstalledHooks(worktree, hooksDir)
	assert.ElementsMatch(t, []string{"pre-commit", "pre-push", "commit-msg"}, got,
		"absolute hooksPath must resolve outside the worktree; joining it onto "+
			"the worktree is what made git doctor report a false 'missing hook'")
}

// TestHooksSearchRootsCoversWorkspaceRoot pins lightwave-cli#307.
//
// The roots used to be ~/dev/lightwave-media (the umbrella workspace that was
// dismantled when repos went flat), ~/dev/lightwave-sys and ~/.brain. The first
// no longer exists, so doctor scanned nothing that mattered and reported a clean
// fleet — while `lw hooks install`, which resolves from cwd, saw the repos fine.
func TestHooksSearchRootsCoversWorkspaceRoot(t *testing.T) {
	t.Parallel()

	workspace := filepath.Join(t.TempDir(), "dev")

	roots := hooksSearchRoots(workspace)

	assert.Contains(t, roots, workspace,
		"the flat workspace root must be scanned — it is where every repo lives")
	assert.NotContains(t, roots, filepath.Join(workspace, "lightwave-media"),
		"the dismantled umbrella workspace must not be a search root")
}

// TestHooksDoctorAndInstallAgreeOnRepos pins the #307 acceptance criterion: the
// audit verb and the install verb must see the same repo. They disagreed
// because install resolves from cwd while doctor scanned a hardcoded root that
// no longer existed.
func TestHooksDoctorAndInstallAgreeOnRepos(t *testing.T) {
	t.Parallel()

	workspace := filepath.Join(t.TempDir(), "dev")
	repo := filepath.Join(workspace, "lightwave-example")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(repo, ".pre-commit-config.yaml"), []byte("repos: []\n"), 0o644))

	// What `lw hooks install` resolves from inside the repo.
	installTarget, err := nearestRepoRoot(repo)
	require.NoError(t, err)
	require.True(t, hasPreCommitConfig(installTarget), "install would accept this repo")

	// What `lw hooks doctor` discovers by scanning.
	assert.Contains(t, discoverRepos(workspace), installTarget,
		"doctor must discover the repo install would act on; when the two disagree "+
			"doctor reports a false all-clear (#307)")
}

// TestHooksWorkspaceRootHonoursEnvOverride pins that the resolver follows
// LW_LIGHTWAVE_ROOT, so doctor and the rest of `lw` agree on the workspace.
func TestHooksWorkspaceRootHonoursEnvOverride(t *testing.T) { //nolint:paralleltest // global viper + config cache
	want := t.TempDir()
	t.Setenv("LW_LIGHTWAVE_ROOT", want)
	config.Reset()
	t.Cleanup(config.Reset)

	assert.Equal(t, want, hooksWorkspaceRoot())
}
