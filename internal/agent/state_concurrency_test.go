package agent_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #345: `Agent.Save` derived its temp file from the agent ID alone —
// `<id>.json.tmp` — so two concurrent saves of the SAME agent wrote the same
// path. The first rename consumed it and the second failed with
//
//	rename …/<id>.json.tmp …/<id>.json: no such file or directory
//
// Two writers exist by design: Spawn's reaper goroutine saves when the child
// exits, and any caller polling RefreshStatus saves the moment it observes the
// pid gone. Both notice the same exit in the same instant. TestSpawn_QuickExit
// hit that window roughly once in every few dozen CI runs for a fortnight,
// never locally, and -race never said a word — the shared state was the
// filesystem, not memory.
//
// So this drives the collision head-on instead of waiting for the timing to
// line up again. Against the old implementation it fails immediately and with
// CI's exact message; against the fix every writer succeeds and the surviving
// file is whole.

// stateDir pins HOME to a temp dir so the records land somewhere disposable.
func stateDir(t *testing.T) string {
	t.Helper()

	t.Setenv("HOME", t.TempDir())

	dir, err := agent.StateDir()
	require.NoError(t, err)

	return dir
}

//nolint:paralleltest // t.Setenv is incompatible with t.Parallel
func TestSaveSurvivesConcurrentWritesOfTheSameAgent(t *testing.T) {
	dir := stateDir(t)

	const writers = 24

	id := agent.NewID()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for i := range writers {
		wg.Add(1)

		// Each writer holds its OWN Agent value, exactly as production does:
		// the reaper reloads the record while the caller keeps the one it
		// spawned. Nothing here shares memory — the contention is on disk.
		go func(n int) {
			defer wg.Done()

			// Varying payload size, so a torn write would show up as a wrong
			// length rather than coincidentally-identical bytes.
			record := &agent.Agent{
				ID:          id,
				Status:      agent.StatusRunning,
				ContextPath: strings.Repeat("x", n*64),
			}

			if err := record.Save(); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	require.Emptyf(t, errs, "%d of %d concurrent saves failed; first: %v", len(errs), writers, firstErr(errs))

	// Last writer wins, which was always the semantic — but the survivor has
	// to be a complete record, not a half-written one.
	data, err := os.ReadFile(filepath.Join(dir, id+".json")) //nolint:gosec // generated id inside t.TempDir
	require.NoError(t, err, "reading the saved record")

	var got agent.Agent

	require.NoError(t, json.Unmarshal(data, &got),
		"the surviving record is not valid JSON, so a partial write was renamed into place")
	assert.Equal(t, id, got.ID)

	// And no temp files are left behind. Load skips anything not ending in
	// .json so litter would not break it, but a state dir that grows a file
	// per failed write is its own slow problem.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	for _, e := range entries {
		assert.False(t, strings.HasSuffix(e.Name(), ".tmp"), "temp file left behind: %s", e.Name())
	}
}

func firstErr(errs []error) error {
	if len(errs) == 0 {
		return nil
	}

	return errs[0]
}

// TestLoadIgnoresAStrayTempFile — the temp names changed shape, and Load
// discriminates by suffix. If a temp file ever became loadable, `lw agent list`
// would report phantom agents mid-write, and a crashed write would leave one
// behind permanently.
//
//nolint:paralleltest // t.Setenv is incompatible with t.Parallel
func TestLoadIgnoresAStrayTempFile(t *testing.T) {
	dir := stateDir(t)

	id := agent.NewID()
	record := &agent.Agent{ID: id, Status: agent.StatusRunning}

	require.NoError(t, record.Save())

	// A temp file in the shape Save now produces, left as though a write died
	// between create and rename.
	stray := filepath.Join(dir, id+".json.987654.tmp")
	require.NoError(t, os.WriteFile(stray, []byte("{ not json"), 0o600))

	loaded, err := agent.Load(id)
	require.NoError(t, err, "Load must still find the real record beside a stray temp file")
	assert.Equal(t, id, loaded.ID)
}

// TestLoadRefusesATempFileAsAnAgent is the other direction, and the one that
// matters: with ONLY a temp file on disk, Load must report the agent as absent.
// If it ever resolved one, `lw agent list` would surface a half-written record
// as a live agent, and a crashed write would leave a permanent phantom.
//
//nolint:paralleltest // t.Setenv is incompatible with t.Parallel
func TestLoadRefusesATempFileAsAnAgent(t *testing.T) {
	dir := stateDir(t)

	id := agent.NewID()
	stray := filepath.Join(dir, id+".json.987654.tmp")

	require.NoError(t, os.WriteFile(stray, []byte("{ not json"), 0o600))

	_, err := agent.Load(id)
	require.Error(t, err, "a temp file is not an agent record and must not resolve as one")
	assert.Contains(t, err.Error(), "not found", "the error must say the agent is absent, not that its JSON is bad")
}

// TestSaveReportsAnUnwritableStateDir — Save's own rejection path. An
// unreported write failure is how a record silently stops updating while
// `lw agent status` keeps printing the stale one.
//
//nolint:paralleltest // t.Setenv is incompatible with t.Parallel
func TestSaveReportsAnUnwritableStateDir(t *testing.T) {
	// HOME as a regular FILE, so StateDir's MkdirAll cannot succeed. Chmod
	// would be the obvious lever and is not deterministic — it does nothing
	// when the tests happen to run as root.
	home := filepath.Join(t.TempDir(), "home-is-a-file")
	require.NoError(t, os.WriteFile(home, []byte("not a directory\n"), 0o600))

	t.Setenv("HOME", home)

	record := &agent.Agent{ID: agent.NewID(), Status: agent.StatusRunning}

	require.Error(t, record.Save(), "Save must report a state dir it cannot write to")
}
