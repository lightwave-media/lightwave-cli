//nolint:testpackage // drives the unexported lock probe directly
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #444: `lw check locks` could not return 0 anywhere. It ran git at
// paths.lightwave_root (~/dev, not a repository), merged git's "Not a git
// repository" warning into stdout, discarded the error, and counted the
// resulting text as a diff — so it reported uv.lock and pnpm-lock.yaml as dirty
// on every machine, naming two files that exist nowhere in the fleet root.
//
// The known-GOOD fixture is therefore the load-bearing one here. A check that
// only ever fires is indistinguishable from a working check until someone
// checks whether it can ever stay silent, and nothing did for as long as this
// command has existed.

// lockRepo is a git repo with the requested lock files committed.
func lockRepo(t *testing.T, names ...string) string {
	t.Helper()

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}

	run("init", "--initial-branch=main")
	run("config", "user.email", "locks@test")
	run("config", "user.name", "Locks Test")

	// A repo with no lock files is one of the fixtures, and git refuses an
	// empty commit, so every fixture carries one unrelated tracked file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# fixture\n"), 0o600))

	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("committed\n"), 0o600))
	}

	run("add", "-A")
	run("commit", "-m", "lock files")

	return dir
}

func TestLockDriftStaysSilentOnACleanRepo(t *testing.T) {
	t.Parallel()

	repo := lockRepo(t, "uv.lock", "pnpm-lock.yaml")

	present, dirty, err := lockDrift(t.Context(), repo, false)
	require.NoError(t, err)

	assert.Equal(t, []string{"uv.lock", "pnpm-lock.yaml"}, present,
		"both lock files are here and both were examined")
	assert.Empty(t, dirty,
		"a clean repo must return no violations — the whole defect was that this outcome was unreachable")
}

func TestLockDriftFiresOnAnUncommittedChange(t *testing.T) {
	t.Parallel()

	repo := lockRepo(t, "uv.lock", "pnpm-lock.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "uv.lock"), []byte("drifted\n"), 0o600))

	_, dirty, err := lockDrift(t.Context(), repo, false)
	require.NoError(t, err)

	assert.Equal(t, []string{"uv.lock"}, dirty,
		"the changed file, and only the changed file")
}

// TestLockDriftIgnoresLockFilesTheRepoDoesNotHave — the fleet root case that
// produced the phantom violations. lightwave-cli is a Go repo and carries
// neither lock file; the honest answer is "nothing to drift", not a finding.
func TestLockDriftIgnoresLockFilesTheRepoDoesNotHave(t *testing.T) {
	t.Parallel()

	repo := lockRepo(t, "uv.lock")

	present, dirty, err := lockDrift(t.Context(), repo, false)
	require.NoError(t, err)

	assert.Equal(t, []string{"uv.lock"}, present, "pnpm-lock.yaml is absent and must not be reported at all")
	assert.Empty(t, dirty)

	bare := lockRepo(t)

	present, dirty, err = lockDrift(t.Context(), bare, false)
	require.NoError(t, err)
	assert.Empty(t, present, "a repo with no lock files examines none")
	assert.Empty(t, dirty, "and finds no violation, rather than inventing two")
}

// TestLockDriftFailsOutsideARepository is the rejection path, and it is the one
// the old implementation silently converted into a violation. git must be
// allowed to refuse, and the refusal must reach the caller as an error.
func TestLockDriftFailsOutsideARepository(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "uv.lock"), []byte("x\n"), 0o600))

	_, _, err := lockDrift(t.Context(), outside, false)
	require.Error(t, err, "git cannot answer outside a work tree, and silence about that is how the phantom drift happened")
	assert.Contains(t, err.Error(), "exit 2", "a tool error is exit 2, never a violation")

	_, err = repoRootFrom(t.Context(), outside)
	require.Error(t, err)
	assert.Contains(t, err.Error(), outside, "the error must name the directory it tried")
}

// TestStatusIsDirtySeparatesTheIndexFromTheWorkingTree pins --staged, which was
// declared in the stamp and wired to nothing. Both directions matter: a flag
// that never narrows is as misleading as one that never widens.
func TestStatusIsDirtySeparatesTheIndexFromTheWorkingTree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		porcelain      string
		wantAny        bool
		wantStagedOnly bool
	}{
		{name: "clean", porcelain: "", wantAny: false, wantStagedOnly: false},
		{name: "staged modification", porcelain: "M  uv.lock", wantAny: true, wantStagedOnly: true},
		{name: "unstaged modification", porcelain: " M uv.lock", wantAny: true, wantStagedOnly: false},
		{name: "staged and then modified again", porcelain: "MM uv.lock", wantAny: true, wantStagedOnly: true},
		{name: "untracked is uncommitted but not staged", porcelain: "?? uv.lock", wantAny: true, wantStagedOnly: false},
		{name: "staged addition", porcelain: "A  uv.lock", wantAny: true, wantStagedOnly: true},
	}

	for _, testCase := range tests {
		tt := testCase
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantAny, statusIsDirty(tt.porcelain, false),
				"default counts anything uncommitted: %q", tt.porcelain)
			assert.Equal(t, tt.wantStagedOnly, statusIsDirty(tt.porcelain, true),
				"--staged counts only the index column: %q", tt.porcelain)
		})
	}
}

// TestStagedOnlyIgnoresAWorkingTreeChange drives the same distinction through
// real git rather than a hand-written porcelain string, so a wrong assumption
// about git's output format cannot pass both tests.
func TestStagedOnlyIgnoresAWorkingTreeChange(t *testing.T) {
	t.Parallel()

	repo := lockRepo(t, "uv.lock")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "uv.lock"), []byte("drifted\n"), 0o600))

	_, dirty, err := lockDrift(t.Context(), repo, true)
	require.NoError(t, err)
	assert.Empty(t, dirty, "--staged must not report a change that was never staged")

	cmd := exec.CommandContext(t.Context(), "git", "add", "uv.lock")
	cmd.Dir = repo
	require.NoError(t, cmd.Run())

	_, dirty, err = lockDrift(t.Context(), repo, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"uv.lock"}, dirty, "once staged, --staged must report it")
}
