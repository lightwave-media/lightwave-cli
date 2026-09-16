package worktreelock_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/worktreelock"
)

// isolateHome points the lock directory at a temp tree.
//
// Not optional: this package writes into the real ~/.lightwave otherwise, and
// a test that leaves fixture locks there would make live worktrees look held
// by sessions that never existed — the exact failure that made every CI run on
// this host look like reviewer failures once before.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("LW_HOME_PRINT", t.TempDir())
}

//nolint:paralleltest // isolateHome uses t.Setenv, which Go forbids alongside t.Parallel
func TestNoLockMeansNoHolder(t *testing.T) {
	isolateHome(t)

	assert.Nil(t, worktreelock.Holder(filepath.Join(t.TempDir(), "never-claimed")))
}

//nolint:paralleltest // isolateHome uses t.Setenv, which Go forbids alongside t.Parallel
func TestClaimIsVisibleToHolder(t *testing.T) {
	isolateHome(t)

	path := filepath.Join(t.TempDir(), "wt")
	require.NoError(t, worktreelock.Claim(path, "session-abc", "fix/thing"))

	holder := worktreelock.Holder(path)
	require.NotNil(t, holder, "a claim this process just made must read as live")
	assert.Equal(t, "session-abc", holder.SessionID)
	assert.Equal(t, os.Getpid(), holder.PID)
	assert.Equal(t, "fix/thing", holder.Branch)
}

//nolint:paralleltest // isolateHome uses t.Setenv, which Go forbids alongside t.Parallel
func TestReleaseClearsTheClaim(t *testing.T) {
	isolateHome(t)

	path := filepath.Join(t.TempDir(), "wt")
	require.NoError(t, worktreelock.Claim(path, "session-abc", "main"))
	require.NoError(t, worktreelock.Release(path))
	assert.Nil(t, worktreelock.Holder(path))

	// Releasing twice is how cleanup paths behave; it must not error.
	assert.NoError(t, worktreelock.Release(path))
}

//nolint:paralleltest // isolateHome uses t.Setenv, which Go forbids alongside t.Parallel
func TestADeadProcessDoesNotHoldAWorktree(t *testing.T) {
	isolateHome(t)

	path := filepath.Join(t.TempDir(), "wt")
	require.NoError(t, worktreelock.Claim(path, "session-abc", "main"))

	// pid 0x7FFFFFFF will not exist. A crashed session must not hold a
	// worktree hostage forever — that would make the lock a leak instead of a
	// signal, and teach whoever hits it to delete locks by hand.
	writeLock(t, path, worktreelock.Lock{
		SessionID: "session-abc",
		PID:       0x7FFFFFFF,
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
	})

	assert.Nil(t, worktreelock.Holder(path), "a lock whose pid is gone must not read as live")
}

//nolint:paralleltest // isolateHome uses t.Setenv, which Go forbids alongside t.Parallel
func TestAStaleTimestampDoesNotHoldAWorktree(t *testing.T) {
	isolateHome(t)

	path := filepath.Join(t.TempDir(), "wt")

	// A live pid — this process — but untouched for longer than the TTL. Pids
	// get recycled and long-lived shells are not active work, so time has to
	// be able to expire a claim on its own.
	writeLock(t, path, worktreelock.Lock{
		SessionID: "session-abc",
		PID:       os.Getpid(),
		TS:        time.Now().Add(-2 * worktreelock.TTL).UTC().Format(time.RFC3339Nano),
	})

	assert.Nil(t, worktreelock.Holder(path), "a claim past TTL must expire even with a live pid")
}

//nolint:paralleltest // isolateHome uses t.Setenv, which Go forbids alongside t.Parallel
func TestAFreshTimestampWithALivePidDoesHold(t *testing.T) {
	isolateHome(t)

	path := filepath.Join(t.TempDir(), "wt")
	writeLock(t, path, worktreelock.Lock{
		SessionID: "session-abc",
		PID:       os.Getpid(),
		TS:        time.Now().Add(-worktreelock.TTL / 2).UTC().Format(time.RFC3339Nano),
	})

	// The known-GOOD control. Without it the three refusals above would pass
	// on a Holder() that returns nil unconditionally.
	holder := worktreelock.Holder(path)
	require.NotNil(t, holder)
	assert.Greater(t, holder.Age(), time.Duration(0))
}

//nolint:paralleltest // isolateHome uses t.Setenv, which Go forbids alongside t.Parallel
func TestACorruptLockVouchesForNothing(t *testing.T) {
	isolateHome(t)

	path := filepath.Join(t.TempDir(), "wt")
	require.NoError(t, os.MkdirAll(filepath.Dir(worktreelock.LockPath(path)), 0o755))
	require.NoError(t, os.WriteFile(worktreelock.LockPath(path), []byte("{not json"), 0o600))

	assert.Nil(t, worktreelock.Holder(path), "an unreadable lock must not be treated as a live holder")
}

//nolint:paralleltest // isolateHome uses t.Setenv, which Go forbids alongside t.Parallel
func TestTwoWorktreesSharingABasenameGetDistinctLocks(t *testing.T) {
	isolateHome(t)

	// Two repos routinely carry a worktree with the same branch-derived name.
	// Keying by basename alone would let one session's lock vouch for the
	// other's worktree — and, worse, let a release on one clear the other.
	a := filepath.Join(t.TempDir(), "repo-a", "fix-thing")
	b := filepath.Join(t.TempDir(), "repo-b", "fix-thing")

	assert.NotEqual(t, worktreelock.LockPath(a), worktreelock.LockPath(b))

	require.NoError(t, worktreelock.Claim(a, "session-a", "fix/thing"))
	assert.Nil(t, worktreelock.Holder(b), "claiming one must not vouch for the other")
}

func writeLock(t *testing.T, worktreePath string, lock worktreelock.Lock) {
	t.Helper()

	body, err := json.Marshal(lock)
	require.NoError(t, err)

	p := worktreelock.LockPath(worktreePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, body, 0o600))
}
