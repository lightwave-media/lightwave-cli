package maintenance_test

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/maintenance"
)

// stampFixture writes a minimal but real-shaped pair of home stamps.
func stampFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, "lightwave-core", "src", "schemas", "data", "meta")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	write(t, filepath.Join(dir, "homedir.yaml"), `
_meta:
  version: "3.10.0"
example:
  dirs:
    - path: "observability"
      nested_dirs:
        - path: "archive"
    - path: "artefacts"
    - path: "forgejo"
    - path: "skills"
    - path: "secrets"
`)
	write(t, filepath.Join(dir, "homedir_zones.yaml"), `
_meta:
  version: "1.7.0"
example:
  version: "1.7.0"
  zones:
    RUNTIME:
      wipe_on_reset: true
      dirs: [observability, artefacts, forgejo]
    AUTHORED:
      wipe_on_reset: false
      dirs: [skills]
    RETAINED:
      wipe_on_reset: false
      dirs: [secrets]
`)

	return root
}

func write(t *testing.T, path, body string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()

	dir := filepath.Join(parts...)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	return dir
}

func TestLoadShapeReadsBothHalvesOfTheStamp(t *testing.T) {
	t.Parallel()

	shape, err := maintenance.LoadShape(stampFixture(t))
	require.NoError(t, err)

	assert.True(t, shape.TopLevel["observability"])
	assert.Equal(t, "RUNTIME", shape.Zone["observability"])
	assert.True(t, shape.Wipeable["forgejo"])
	assert.False(t, shape.Wipeable["skills"], "AUTHORED is wipe_on_reset: false")
	assert.Equal(t, "1.7.0", shape.ZonesVersion)
}

// TestLoadShapeRefusesAStampItCannotRead is the defect class this whole surface
// exists to catch, aimed at the surface itself.
//
// The TypeScript detector this ports from matched declared dirs with
// `^ {4}- path: "..."`. A re-indent, or a move to a block list, silently yields
// an empty declared-set — and then every directory on the machine reads as
// undeclared, or, with the comparison the other way, the print reads clean.
func TestLoadShapeRefusesAStampItCannotRead(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "lightwave-core", "src", "schemas", "data", "meta")
	write(t, filepath.Join(dir, "homedir.yaml"), "_meta:\n  version: \"3.10.0\"\n")
	write(t, filepath.Join(dir, "homedir_zones.yaml"), "example:\n  zones: {}\n")

	_, err := maintenance.LoadShape(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreadable, not empty")
}

func TestLoadShapeRefusesAMissingStamp(t *testing.T) {
	t.Parallel()

	_, err := maintenance.LoadShape(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read home stamp")
}

// TestShapeReportsLockstepDrift covers the claim homedir_zones.yaml makes about
// itself — every dir it names is declared in homedir.yaml, and the two move in
// lockstep. Nothing had ever checked it in either direction.
func TestShapeReportsLockstepDrift(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "lightwave-core", "src", "schemas", "data", "meta")
	write(t, filepath.Join(dir, "homedir.yaml"), `
example:
  dirs:
    - path: "observability"
    - path: "unzoned"
`)
	write(t, filepath.Join(dir, "homedir_zones.yaml"), `
example:
  version: "9.9.9"
  zones:
    RUNTIME:
      wipe_on_reset: true
      dirs: [observability, ghost]
`)

	shape, err := maintenance.LoadShape(root)
	require.NoError(t, err)

	assert.Equal(t, []string{"unzoned"}, shape.Unclassified(),
		"declared but no zone classifies it — so no wipe policy exists for it")
	assert.Equal(t, []string{"ghost"}, shape.Phantom(),
		"classified but homedir.yaml never declared it")
}

// TestScanDoesNotCallABareRepoDirectoryEmpty pins the bug a live run caught.
//
// A bare git repo keeps refs/heads, refs/tags and objects/pack as empty
// directories by design and breaks without them. On this host forgejo/ holds 20
// of them, which produced 60 of 74 empty-dir findings — and forgejo/ is a
// wipe_on_reset zone, so `prune-empty --yes` would have stripped the refs
// directories out of every repository on the server.
func TestScanDoesNotCallABareRepoDirectoryEmpty(t *testing.T) {
	t.Parallel()

	shape, err := maintenance.LoadShape(stampFixture(t))
	require.NoError(t, err)

	print := t.TempDir()
	repo := mkdir(t, print, "forgejo", "data", "repos", "example.git")
	mkdir(t, repo, "refs", "heads")
	mkdir(t, repo, "objects", "pack")

	findings := maintenance.Scan(print, shape, time.Now())

	for _, dir := range findings.EmptyDirs {
		assert.NotContains(t, dir, ".git/",
			"a bare repo's interior belongs to git, not to this scan")
	}

	// The directory HOLDING the bare repo is not empty either. Judging emptiness
	// on the filtered list rather than the real one is what reported a
	// 20-repository directory as empty.
	assert.NotContains(t, findings.EmptyDirs, filepath.Join("forgejo", "data", "repos"),
		"a directory full of bare repos is not empty")
}

// TestScanFlagsOnlyAppendOnlyLogs pins the second live-run bug.
//
// Before this, any large file under observability/ counted. On this host that
// flagged a 15MB git bundle in observability/archive/ — the rescued-commits
// backup for the retired null* clones, which exists nowhere else — and rotating
// it would have gzipped it and truncated the original to zero.
func TestScanFlagsOnlyAppendOnlyLogs(t *testing.T) {
	t.Parallel()

	shape, err := maintenance.LoadShape(stampFixture(t))
	require.NoError(t, err)

	print := t.TempDir()
	obs := mkdir(t, print, "observability")
	big := strings.Repeat("x", maintenance.MaxLogBytes+1)

	write(t, filepath.Join(obs, "reuse-check.jsonl"), big)
	write(t, filepath.Join(obs, "archive", "rescued.bundle"), big)
	write(t, filepath.Join(obs, "launchd", "agent.stdout.log"), big)
	write(t, filepath.Join(obs, "snapshot.tar.gz"), big)

	findings := maintenance.Scan(print, shape, time.Now())

	paths := make([]string, 0, len(findings.OversizeLogFiles))
	for _, f := range findings.OversizeLogFiles {
		paths = append(paths, f.Path)
	}

	assert.Contains(t, paths, "observability/reuse-check.jsonl")
	assert.Contains(t, paths, filepath.Join("observability", "launchd", "agent.stdout.log"))
	assert.NotContains(t, paths, filepath.Join("observability", "archive", "rescued.bundle"),
		"the archive is where rotation WRITES; rotating it would eat its own output")
	assert.NotContains(t, paths, "observability/snapshot.tar.gz",
		"a large file is not the same claim as a log needing rotation")
}

func TestScanFindsUndeclaredEntriesAndBrokenLinks(t *testing.T) {
	t.Parallel()

	shape, err := maintenance.LoadShape(stampFixture(t))
	require.NoError(t, err)

	print := t.TempDir()
	mkdir(t, print, "observability")
	mkdir(t, print, "not-in-the-stamp")
	write(t, filepath.Join(print, "index.md"), "allowed at the root\n")
	write(t, filepath.Join(print, "stray.txt"), "not allowed\n")
	require.NoError(t, os.Symlink(
		filepath.Join(print, "gone"), filepath.Join(print, "observability", "dangling")))

	findings := maintenance.Scan(print, shape, time.Now())

	assert.Contains(t, findings.UndeclaredTopLevel, "not-in-the-stamp/")
	assert.Contains(t, findings.UndeclaredTopLevel, "stray.txt")
	assert.NotContains(t, findings.UndeclaredTopLevel, "index.md",
		"the baseline render keeps index.md at the root")
	assert.Equal(t, []string{filepath.Join("observability", "dangling")}, findings.BrokenSymlinks)
}

func TestScanStalenessIsMeasuredAgainstThePassedClock(t *testing.T) {
	t.Parallel()

	shape, err := maintenance.LoadShape(stampFixture(t))
	require.NoError(t, err)

	print := t.TempDir()
	fresh := mkdir(t, print, "artefacts", "fresh")
	old := mkdir(t, print, "artefacts", "old")
	named := mkdir(t, print, "artefacts", "job.stale-2026-01-01")

	now := time.Now()
	past := now.AddDate(0, 0, -(maintenance.StaleArtefactDays + 1))
	require.NoError(t, os.Chtimes(old, past, past))
	require.NoError(t, os.Chtimes(fresh, now, now))
	require.NoError(t, os.Chtimes(named, now, now))

	findings := maintenance.Scan(print, shape, now)

	assert.Contains(t, findings.StaleArtefacts, "artefacts/old")
	assert.Contains(t, findings.StaleArtefacts, "artefacts/job.stale-2026-01-01",
		"an explicitly stale-named artefact is stale at any age")
	assert.NotContains(t, findings.StaleArtefacts, "artefacts/fresh")
}

// TestPlanPruneProtectsEverythingWithoutAWipePolicy is the safety property.
func TestPlanPruneProtectsEverythingWithoutAWipePolicy(t *testing.T) {
	t.Parallel()

	shape, err := maintenance.LoadShape(stampFixture(t))
	require.NoError(t, err)

	eligible, protected := maintenance.PlanPrune([]string{
		"observability/handoffs",
		"skills/journey-first/evals",
		"secrets/org-sync",
		"worktrees",
	}, shape)

	assert.Equal(t, []string{"observability/handoffs"}, eligible)
	assert.Equal(t, []string{
		"skills/journey-first/evals",
		"secrets/org-sync",
		"worktrees",
	}, protected, "AUTHORED, RETAINED and unclassified are all left alone")
}

func TestRotateArchivesBeforeTruncatingAndKeepsTheInode(t *testing.T) {
	t.Parallel()

	print := t.TempDir()
	obs := mkdir(t, print, "observability")
	log := filepath.Join(obs, "ledger.jsonl")
	write(t, log, "line one\nline two\n")

	before, err := os.Stat(log)
	require.NoError(t, err)

	rotations := maintenance.PlanRotations(
		[]maintenance.OversizeFile{{Path: "observability/ledger.jsonl", Bytes: before.Size()}},
		time.Date(2026, 9, 15, 23, 54, 36, 0, time.UTC))
	require.Len(t, rotations, 1)

	require.NoError(t, maintenance.Rotate(print, rotations[0]))

	// The archive holds every byte.
	assert.Equal(t, "line one\nline two\n", readGzip(t, filepath.Join(print, rotations[0].Archive)))

	// The live log is emptied, not replaced. Every daemon here holds it open in
	// append mode; a rename would leave them writing to an unnamed inode and the
	// rotation would silently discard everything until each one restarted.
	after, err := os.Stat(log)
	require.NoError(t, err, "the log still exists at the same path")
	assert.Zero(t, after.Size())
	assert.True(t, os.SameFile(before, after), "same inode — open writers keep working")
}

// TestRotateRefusesToOverwriteAnExistingArchive: losing one of two archives to a
// clobber is worse than a failed rotation, so the collision is an error.
func TestRotateRefusesToOverwriteAnExistingArchive(t *testing.T) {
	t.Parallel()

	print := t.TempDir()
	mkdir(t, print, "observability")
	write(t, filepath.Join(print, "observability", "a.jsonl"), "payload\n")

	rotation := maintenance.Rotation{
		Source:  "observability/a.jsonl",
		Archive: maintenance.ArchiveDir + "/a.jsonl.gz",
	}
	write(t, filepath.Join(print, rotation.Archive), "an earlier archive\n")

	err := maintenance.Rotate(print, rotation)
	require.Error(t, err)

	kept, readErr := os.ReadFile(filepath.Join(print, rotation.Archive))
	require.NoError(t, readErr)
	assert.Equal(t, "an earlier archive\n", string(kept), "the existing archive is untouched")

	live, readErr := os.ReadFile(filepath.Join(print, rotation.Source))
	require.NoError(t, readErr)
	assert.Equal(t, "payload\n", string(live), "and the log was not truncated either")
}

// TestPlanRotationsKeepsThePathNotJustTheBasename: two logs of the same name in
// different directories must not compete for one archive name.
func TestPlanRotationsKeepsThePathNotJustTheBasename(t *testing.T) {
	t.Parallel()

	rotations := maintenance.PlanRotations([]maintenance.OversizeFile{
		{Path: "observability/out.log"},
		{Path: "observability/launchd/out.log"},
	}, time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC))

	require.Len(t, rotations, 2)
	assert.NotEqual(t, rotations[0].Archive, rotations[1].Archive)
	assert.Contains(t, rotations[1].Archive, "launchd/out.log")
}

func TestPruneRefusesANonEmptyDirectory(t *testing.T) {
	t.Parallel()

	print := t.TempDir()
	dir := mkdir(t, print, "observability", "handoffs")
	write(t, filepath.Join(dir, "arrived.json"), "{}\n")

	err := maintenance.Prune(print, "observability/handoffs")
	require.Error(t, err, "a directory filled between the scan and the prune keeps its contents")

	_, statErr := os.Stat(filepath.Join(dir, "arrived.json"))
	require.NoError(t, statErr)
}

func readGzip(t *testing.T, path string) string {
	t.Helper()

	f, err := os.Open(path)
	require.NoError(t, err)

	defer f.Close()

	zr, err := gzip.NewReader(f)
	require.NoError(t, err)

	defer zr.Close()

	body, err := io.ReadAll(zr)
	require.NoError(t, err)

	return string(body)
}
