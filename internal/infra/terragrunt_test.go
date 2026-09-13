package infra_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/infra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #367. `lw infra list` walked the filesystem from <root>/<env>/<region> with
// env and region pinned to prod/us-east-1, because the flags that would have
// changed them were read by the handler and declared in no schema. It answered
// "these are the units" while omitting more than half of them.
//
// The replacement enumerates from `git ls-files`, which is what these pin: not
// "it lists things", but that it lists exactly what this repository tracks.

func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func writeUnit(t *testing.T, root, rel string) {
	t.Helper()

	dir := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "terragrunt.hcl"), []byte("# unit\n"), 0o600))
}

// newInfraRepo builds a repo shaped like lightwave-infrastructure-live: two
// regions of tracked units, plus the two kinds of noise a filesystem walk picks
// up and git does not.
func newInfraRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	git(t, root, "init", "-b", "main")
	git(t, root, "config", "user.email", "test@test.com")
	git(t, root, "config", "user.name", "Test")

	for _, rel := range []string{
		"prod/us-east-1/vpc",
		"prod/us-east-1/rds",
		"prod/us-west-2/cineos-io",
		"dev/us-east-1/sandbox",
	} {
		writeUnit(t, root, rel)
	}

	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "units")

	// Generated cache, and another session's checkout. Both exist in the live
	// repo -- it carries 20 terragrunt.hcl files under .claude/worktrees -- and
	// neither is tracked.
	writeUnit(t, root, "prod/us-east-1/vpc/.terragrunt-cache/xyz/abc")
	writeUnit(t, root, ".claude/worktrees/other-session/prod/us-east-1/ghost")

	return root
}

func TestListUnits_SeesEveryRegion(t *testing.T) {
	t.Parallel()

	root := newInfraRepo(t)

	units, err := infra.NewTerragruntRunner(root, "prod", "us-east-1").ListUnits(t.Context())
	require.NoError(t, err)

	// The whole point of #367: the runner is constructed for prod/us-east-1 and
	// must still report the other trees.
	assert.Equal(t, []string{
		"dev/us-east-1/sandbox",
		"prod/us-east-1/rds",
		"prod/us-east-1/vpc",
		"prod/us-west-2/cineos-io",
	}, units)
}

// TestListUnits_ExcludesUntrackedNoise is the reason it shells to git rather
// than walking. A walk reports the cache directory and another session's
// worktree as units of this repo -- the failure #404 had to be reverted for,
// except here the nested checkout is already present in the live repo.
func TestListUnits_ExcludesUntrackedNoise(t *testing.T) {
	t.Parallel()

	root := newInfraRepo(t)

	units, err := infra.NewTerragruntRunner(root, "prod", "us-east-1").ListUnits(t.Context())
	require.NoError(t, err)

	for _, u := range units {
		assert.NotContains(t, u, ".terragrunt-cache",
			"generated cache must not be reported as a unit")
		assert.NotContains(t, u, ".claude/worktrees",
			"another session's checkout must not be reported as a unit of this one")
	}
}

// TestListUnits_RejectsANonRepo is the rejection path. Pointed at a directory
// git does not track, it must fail loudly: an empty list reads as "this
// infrastructure has no units", which is the plausible-wrong answer the whole
// issue is about.
func TestListUnits_RejectsANonRepo(t *testing.T) {
	t.Parallel()

	_, err := infra.NewTerragruntRunner(t.TempDir(), "prod", "us-east-1").ListUnits(t.Context())
	require.Error(t, err, "a directory git does not track must not read as zero units")
}

func TestResolveUnitDir(t *testing.T) {
	t.Parallel()

	root := newInfraRepo(t)
	runner := infra.NewTerragruntRunner(root, "prod", "us-east-1")

	t.Run("repo-root path wins", func(t *testing.T) {
		t.Parallel()

		// The form ListUnits now prints has to be accepted, or its output is
		// unusable as plan/apply input.
		assert.Equal(t, filepath.Join(root, "prod/us-west-2/cineos-io"),
			runner.ResolveUnitDir("prod/us-west-2/cineos-io"))
	})

	t.Run("bare unit falls back to env/region", func(t *testing.T) {
		t.Parallel()

		// Pre-#367 scripts pass a bare unit name; breaking them was not worth it.
		assert.Equal(t, filepath.Join(root, "prod/us-east-1/vpc"),
			runner.ResolveUnitDir("vpc"))
	})

	t.Run("unknown path still resolves under env/region", func(t *testing.T) {
		t.Parallel()

		// Resolution does not validate: the caller's Stat reports a missing unit
		// with the full path, which is a better message than one from here.
		assert.Equal(t, filepath.Join(root, "prod/us-east-1/nope"),
			runner.ResolveUnitDir("nope"))
	})
}
