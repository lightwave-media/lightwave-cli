//nolint:testpackage // builds the hook environment from the unexported selector list
package gitfixture

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFixturesLeaveTheHookRepoUntouched reproduces the leak this package
// exists for. It runs every suite that builds a git fixture, both the Go
// packages and the shell proofs under scripts/, the way the pre-push hook
// does: GIT_DIR and GIT_INDEX_FILE (what git exports to a hook) and GIT_CONFIG
// (what `git config` honours) all aimed at a scratch "real" repo. Then it
// asserts that repo comes out exactly as it went in.
func TestFixturesLeaveTheHookRepoUntouched(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	packages := fixturePackages(t, root)
	scripts := fixtureScripts(t, root)
	// A discovery that matched nothing would run nothing and pass.
	require.Contains(t, packages, "./internal/git")
	require.Contains(t, scripts, "scripts/lw-current-test.sh")

	real := seedRepo(t)
	before := snapshot(t, real)

	hook := append(withoutRepoSelectors(),
		"GIT_DIR="+filepath.Join(real, ".git"),
		"GIT_INDEX_FILE="+filepath.Join(real, ".git", "index"),
		"GIT_CONFIG="+filepath.Join(real, ".git", "config"),
	)

	suites := make([][]string, 0, 1+len(scripts))
	suites = append(suites, append([]string{"go", "test", "-count=1"}, packages...))

	for _, script := range scripts {
		suites = append(suites, []string{"bash", script})
	}

	var failed []string

	for _, argv := range suites {
		cmd := exec.CommandContext(t.Context(), argv[0], argv[1:]...) //nolint:gosec // argv built from our own tree
		cmd.Dir = root
		cmd.Env = hook

		if out, err := cmd.CombinedOutput(); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v\n%s", strings.Join(argv, " "), err, out))
		}
	}

	assert.Equal(t, before, snapshot(t, real), "a fixture suite wrote into the repo the hook runs in")
	// A nested run that died before building anything would also leave the
	// repo untouched, so the suites must actually have passed.
	assert.Empty(t, failed, "fixture suites failed under the hook environment")
}

// TestEnvKeepsTheHookRepoOutOfReach is the refusal half, pinned directly
// because the suite run above cannot pin it alone: a package that calls
// Isolate would pass even if Env stripped nothing. With a hook's GIT_DIR and a
// GIT_CONFIG in the process, git under the raw environment reaches that repo
// (the control), and git under Env must not.
func TestEnvKeepsTheHookRepoOutOfReach(t *testing.T) {
	hookRepo := seedRepo(t)
	git(t, hookRepo, "config", "lw.probe", "leaked")
	t.Setenv("GIT_DIR", filepath.Join(hookRepo, ".git"))
	t.Setenv("GIT_CONFIG", filepath.Join(hookRepo, ".git", "config"))

	outsideAnyRepo := t.TempDir()
	run := func(env []string, args ...string) error {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = outsideAnyRepo
		cmd.Env = env

		return cmd.Run()
	}

	for selector, args := range map[string][]string{
		"GIT_DIR":    {"rev-parse", "--git-dir"},
		"GIT_CONFIG": {"config", "--get", "lw.probe"},
	} {
		require.NoError(t, run(os.Environ(), args...), "control: %s reaches the hook's repo", selector)
		require.Error(t, run(Env(), args...), "Env must keep %s from reaching the hook's repo", selector)
	}
}

// repoState is everything a leaked fixture has been seen to change.
type repoState struct {
	Config, Refs, Status string
}

func snapshot(t *testing.T, repo string) repoState {
	t.Helper()

	config, err := os.ReadFile(filepath.Join(repo, ".git", "config"))
	require.NoError(t, err)

	return repoState{
		Config: string(config),
		Refs:   git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)"),
		Status: git(t, repo, "status", "--porcelain"),
	}
}

func seedRepo(t *testing.T) string {
	t.Helper()

	repo := filepath.Join(t.TempDir(), "real")
	git(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o600))
	git(t, repo, "add", "seed.txt")
	git(t, repo, "commit", "-qm", "seed")

	return repo
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = Env()

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)

	return string(out)
}

// withoutRepoSelectors is the current environment minus the selectors, so a
// hook that invoked this test cannot point the nested run at a third repo.
func withoutRepoSelectors() []string {
	var env []string

	for _, kv := range os.Environ() {
		if !isRepoSelector(kv) {
			env = append(env, kv)
		}
	}

	return env
}

func moduleRoot(t *testing.T) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "go", "env", "GOMOD").Output()
	require.NoError(t, err)

	return filepath.Dir(strings.TrimSpace(string(out)))
}

// fixturePackages lists every package whose tests run `git init`. It reads
// the tree rather than a maintained list, so a fixture added next month is
// covered the day it lands. This package is left out; including it would
// make the nested run start this test again.
func fixturePackages(t *testing.T, root string) []string {
	t.Helper()

	self, err := os.Getwd()
	require.NoError(t, err)

	found := map[string]bool{}

	walkSources(t, root, "_test.go", func(path, src string) {
		dir := filepath.Dir(path)
		if dir != self && strings.Contains(src, `"git"`) && strings.Contains(src, `"init"`) {
			found["./"+rel(t, root, dir)] = true
		}
	})

	packages := make([]string, 0, len(found))
	for pkg := range found {
		packages = append(packages, pkg)
	}

	return packages
}

// fixtureScripts lists the shell proofs under scripts/ that run `git init`.
func fixtureScripts(t *testing.T, root string) []string {
	t.Helper()

	var scripts []string

	walkSources(t, filepath.Join(root, "scripts"), "test.sh", func(path, src string) {
		if strings.Contains(src, "git init") {
			scripts = append(scripts, rel(t, root, path))
		}
	})

	return scripts
}

func walkSources(t *testing.T, root, suffix string, visit func(path, src string)) {
	t.Helper()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && path != root {
			return filepath.SkipDir
		}

		if d.IsDir() || !strings.HasSuffix(path, suffix) {
			return nil
		}

		src, err := os.ReadFile(path) //nolint:gosec // a source file in our own tree
		if err != nil {
			return err
		}

		visit(path, string(src))

		return nil
	})
	require.NoError(t, err)
}

func rel(t *testing.T, root, path string) string {
	t.Helper()

	r, err := filepath.Rel(root, path)
	require.NoError(t, err)

	return filepath.ToSlash(r)
}
