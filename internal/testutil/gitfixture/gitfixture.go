// Package gitfixture is the environment for every git call that BUILDS a test
// fixture. It imports only the standard library so any package's tests can use
// it, including internal/git and internal/cli, which internal/testutil cannot
// serve without an import cycle.
//
// dev/hooks/pre-push runs `mise run ci`, so the suite also runs inside git's
// hook environment. Git exports GIT_DIR and GIT_INDEX_FILE to a hook, and they
// outrank cmd.Dir and -C, so a fixture's `git init`, `git commit` and
// `git config` land in the repo being pushed instead of the temp dir.
// ~/dev/lightwave-cli/.git/config, which every worktree shares, picked up
// `test <test@example.com>` on 2026-09-16 at 13:34:47 and then
// `Proof <proof@test>` (scripts/lw-current-test.sh) at 13:46:55. Each became
// the author of every later commit there that did not override it.
//
// Stripping the selectors was not enough, because `git config` also honours
// GIT_CONFIG. Git never exports it, but any other caller can. So identity is
// passed as environment and never written: a variable reaches only the process
// it is passed to, so it cannot outlive the test in somebody's repo.
package gitfixture

import (
	"os"
	"strings"
)

// Identity is the author and committer of every fixture commit. The .invalid
// TLD (RFC 2606) makes a leaked commit recognisable on sight.
const (
	Name  = "fixture"
	Email = "fixture@lightwave.invalid"
)

// repoSelectors are the variables that pin git to a repository, index, object
// store or config file other than the one cmd.Dir names. GIT_CONFIG_PARAMETERS
// carries an outer `git -c` into the fixture, so it goes too.
var repoSelectors = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_PREFIX", "GIT_COMMON_DIR",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_CONFIG", "GIT_CONFIG_PARAMETERS",
}

var identity = []string{
	"GIT_AUTHOR_NAME=" + Name, "GIT_AUTHOR_EMAIL=" + Email,
	"GIT_COMMITTER_NAME=" + Name, "GIT_COMMITTER_EMAIL=" + Email,
}

// Env returns the current environment with the repo selectors removed and the
// fixture identity added. The selectors must be absent, not blank: git treats
// an empty GIT_WORK_TREE as set and refuses to run without a GIT_DIR.
func Env() []string {
	env := make([]string, 0, len(os.Environ())+len(identity))

	for _, kv := range os.Environ() {
		if !isRepoSelector(kv) {
			env = append(env, kv)
		}
	}

	return append(env, identity...)
}

// Isolate gives the whole test process what Env gives one command. Call it
// from TestMain in a package whose tests drive PRODUCTION code that shells out
// to git. That code passes the process environment through, so under a hook
// it acts on the hook's repo (installHooks wrote core.hooksPath there), and in
// CI, which has no global identity, its commits have no author.
func Isolate() {
	for _, kv := range os.Environ() {
		if isRepoSelector(kv) {
			name, _, _ := strings.Cut(kv, "=")
			_ = os.Unsetenv(name)
		}
	}

	for _, kv := range identity {
		name, value, _ := strings.Cut(kv, "=")
		_ = os.Setenv(name, value)
	}
}

func isRepoSelector(kv string) bool {
	for _, name := range repoSelectors {
		if strings.HasPrefix(kv, name+"=") {
			return true
		}
	}

	return false
}
