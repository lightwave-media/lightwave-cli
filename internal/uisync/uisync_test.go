package uisync_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/uisync"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fixedNow = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

// writeFile is a helper for building fixture trees.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func fixtureRepos(t *testing.T) (uiRepo, siteDir string) {
	t.Helper()
	uiRepo, siteDir = t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(uiRepo, "src", "components", "base", "button", "button.tsx"), "v2 button\n")
	writeFile(t, filepath.Join(uiRepo, "src", "components", "base", "button", "index.ts"), "export\n")

	return uiRepo, siteDir
}

func TestAddCopiesAndPins(t *testing.T) {
	t.Parallel()
	uiRepo, siteDir := fixtureRepos(t)

	copied, err := uisync.Add(uiRepo, siteDir, "Button", "2.0.0", false, false, fixedNow)
	require.NoError(t, err)
	assert.Len(t, copied, 2)

	lock, err := uisync.ReadLock(siteDir)
	require.NoError(t, err)

	pin, ok := lock.Find("component", "Button")
	require.True(t, ok, "pin must be recorded")
	assert.Equal(t, "2.0.0", pin.LightwaveUIVersion)
	assert.Equal(t, "2026-06-12T12:00:00Z", pin.SyncedAt)
	assert.Equal(t, "2.0.0", lock.LightwaveUIVersion)
}

func TestAddRefusesExistingWithoutForce(t *testing.T) {
	t.Parallel()
	uiRepo, siteDir := fixtureRepos(t)

	_, err := uisync.Add(uiRepo, siteDir, "Button", "2.0.0", false, false, fixedNow)
	require.NoError(t, err)

	_, err = uisync.Add(uiRepo, siteDir, "Button", "2.1.0", false, false, fixedNow)
	require.Error(t, err, "re-add over existing copy must refuse — that is the clobbering failure mode")
	assert.Contains(t, err.Error(), "lw ui sync")

	_, err = uisync.Add(uiRepo, siteDir, "Button", "2.1.0", true, false, fixedNow)
	require.NoError(t, err, "--force overrides")
}

func TestResolveComponentDirForms(t *testing.T) {
	t.Parallel()
	uiRepo := t.TempDir()
	// Real lightwave-ui v8 layout: file under a subcategory dir.
	writeFile(t, filepath.Join(uiRepo, "src", "components", "base", "buttons", "button.tsx"), "x")

	got, err := uisync.ResolveComponentDir(uiRepo, "Button")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("base", "buttons"), got, "PascalCase name resolves via the file's parent dir")

	got, err = uisync.ResolveComponentDir(uiRepo, "base/buttons")
	require.NoError(t, err)
	assert.Equal(t, "base/buttons", got, "explicit path passes through")

	_, err = uisync.ResolveComponentDir(uiRepo, "Nope")
	require.Error(t, err)
}

func TestSyncThreeWay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		base         string // content at pinned version ("" = no base available)
		local        string
		upstream     string
		wantOutcome  uisync.FileOutcome
		wantLocal    string // expected local content after sync
		wantUpstream bool   // .upstream file written
	}{
		{
			name: "unchanged", base: "same\n", local: "same\n", upstream: "same\n",
			wantOutcome: uisync.OutcomeUnchanged, wantLocal: "same\n",
		},
		{
			name: "fast-forward", base: "old\n", local: "old\n", upstream: "new\n",
			wantOutcome: uisync.OutcomeFastForward, wantLocal: "new\n",
		},
		{
			name: "local kept", base: "old\n", local: "customized\n", upstream: "old\n",
			wantOutcome: uisync.OutcomeLocalKept, wantLocal: "customized\n",
		},
		{
			name: "conflict", base: "old\n", local: "customized\n", upstream: "new\n",
			wantOutcome: uisync.OutcomeConflict, wantLocal: "customized\n", wantUpstream: true,
		},
		{
			name: "no base treats divergence as conflict", base: "", local: "customized\n", upstream: "new\n",
			wantOutcome: uisync.OutcomeConflict, wantLocal: "customized\n", wantUpstream: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			uiRepo, siteDir := t.TempDir(), t.TempDir()
			rel := filepath.Join("src", "components", "base", "button", "button.tsx")
			writeFile(t, filepath.Join(uiRepo, rel), tt.upstream)
			writeFile(t, filepath.Join(siteDir, rel), tt.local)

			base := func(version, relPath string) ([]byte, bool, error) {
				if tt.base == "" {
					return nil, false, nil
				}

				return []byte(tt.base), true, nil
			}

			pin := uisync.Pin{Kind: "component", Name: "Button", LightwaveUIVersion: "1.0.0", SyncedAt: "2026-01-01T00:00:00Z"}

			report, err := uisync.SyncComponent(uiRepo, siteDir, pin, filepath.Join("base", "button"), "2.0.0", base, false, fixedNow)
			require.NoError(t, err)
			require.Len(t, report.Files, 1)
			assert.Equal(t, tt.wantOutcome, report.Files[0].Outcome)

			localAfter, err := os.ReadFile(filepath.Join(siteDir, rel))
			require.NoError(t, err)
			assert.Equal(t, tt.wantLocal, string(localAfter), "local content must never be clobbered")

			_, err = os.Stat(filepath.Join(siteDir, rel) + ".upstream")
			if tt.wantUpstream {
				assert.NoError(t, err, ".upstream must be written on conflict")
			} else {
				assert.True(t, os.IsNotExist(err), ".upstream must not exist")
			}
		})
	}
}

func TestSyncPinAdvancesOnlyWhenClean(t *testing.T) {
	t.Parallel()
	uiRepo, siteDir := t.TempDir(), t.TempDir()
	rel := filepath.Join("src", "components", "base", "button", "button.tsx")
	writeFile(t, filepath.Join(uiRepo, rel), "new\n")
	writeFile(t, filepath.Join(siteDir, rel), "customized\n")

	lock := &uisync.Lock{}
	lock.Upsert(uisync.Pin{Kind: "component", Name: "Button", LightwaveUIVersion: "1.0.0", SyncedAt: "2026-01-01T00:00:00Z"})
	require.NoError(t, uisync.WriteLock(siteDir, lock))

	conflictBase := func(string, string) ([]byte, bool, error) { return []byte("old\n"), true, nil }
	pin, _ := lock.Find("component", "Button")

	report, err := uisync.SyncComponent(uiRepo, siteDir, pin, filepath.Join("base", "button"), "2.0.0", conflictBase, false, fixedNow)
	require.NoError(t, err)
	require.Equal(t, 1, report.Conflicts)

	after, err := uisync.ReadLock(siteDir)
	require.NoError(t, err)
	pinAfter, _ := after.Find("component", "Button")
	assert.Equal(t, "1.0.0", pinAfter.LightwaveUIVersion, "pin must not advance past a conflicted sync")

	// Resolve: accept upstream, re-sync — now the pin advances.
	writeFile(t, filepath.Join(siteDir, rel), "new\n")
	require.NoError(t, os.Remove(filepath.Join(siteDir, rel)+".upstream"))

	_, err = uisync.SyncComponent(uiRepo, siteDir, pinAfter, filepath.Join("base", "button"), "2.0.0", conflictBase, false, fixedNow)
	require.NoError(t, err)

	final, err := uisync.ReadLock(siteDir)
	require.NoError(t, err)
	pinFinal, _ := final.Find("component", "Button")
	assert.Equal(t, "2.0.0", pinFinal.LightwaveUIVersion)
}

func TestGitBaseExtractsTaggedContent(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}

	run("init", "-q")
	writeFile(t, filepath.Join(repo, "src", "f.tsx"), "v1 content\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	run("tag", "v1.0.0")

	base := uisync.GitBase(repo)

	content, ok, err := base("1.0.0", "src/f.tsx")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "v1 content\n", string(content))

	_, ok, err = base("9.9.9", "src/f.tsx")
	require.NoError(t, err, "missing tag must degrade to ok=false, not error")
	assert.False(t, ok)

	_, ok, err = base("1.0.0", "src/missing.tsx")
	require.NoError(t, err, "missing path at tag must degrade to ok=false")
	assert.False(t, ok)
}
