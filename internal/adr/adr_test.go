package adr_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/adr"
)

// stampFrontmatterRequired and stampRequiredSections mirror
// policy/governance/spec_artifact_kinds.yaml's `adr` entry. They are asserted
// literally on purpose: `lw docs spec-lint` is the real validator, and a
// skeleton that fails it is worse than no scaffolder because it emits files
// that fail a gate the author did not run. If the stamp changes these, this
// test should fail and be updated deliberately.
var (
	stampFrontmatterRequired = []string{"kind", "status", "decided_at"}
	stampRequiredSections    = []string{"Context", "Decision", "Consequences"}
)

func newTree(t *testing.T) adr.Tree {
	t.Helper()

	return adr.Tree{
		Name:       "test",
		Dir:        t.TempDir(),
		IDPrefix:   "CORE",
		IDKey:      "adr_id",
		FilePrefix: "",
	}
}

func TestParseNumber(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		filename string
		want     int
		ok       bool
	}{
		// Both shapes occur in lightwave-core's spec/adr/ right now. A scanner
		// that knows only the bare form reads past ADR-0031 entirely.
		{"bare core form", "0047-worktree-single-root.md", 47, true},
		{"prefixed host form", "ADR-0031-agent-runner-registration.md", 31, true},
		{"leading zeros preserved", "0001-adopt-spec-docs-factory.md", 1, true},
		{"readme is not an ADR", "README.md", 0, false},
		{"index is not an ADR", "__index.yaml", 0, false},
		{"three digits is not the convention", "047-too-short.md", 0, false},
		{"number without a trailing dash", "0047.md", 0, false},
	}

	for _, testCase := range cases {
		tt := testCase
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := adr.ParseNumber(tt.filename)
			assert.Equal(t, tt.ok, ok, "recognised")
			assert.Equal(t, tt.want, got, "number")
		})
	}
}

func TestMaxReadsBothFilenameShapes(t *testing.T) {
	t.Parallel()

	tree := newTree(t)

	// ADR-0031 is deliberately the highest. A scanner that only understands
	// the bare form answers 5 here and hands out an id that already exists.
	for _, name := range []string{
		"0001-first.md",
		"0005-fifth.md",
		"ADR-0031-prefixed.md",
		"README.md",
		"__index.yaml",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(tree.Dir, name), []byte("x"), 0o644))
	}

	got, err := tree.Max()
	require.NoError(t, err, "scan the corpus")
	assert.Equal(t, 31, got, "highest id across both filename shapes")
}

func TestMaxOnEmptyCorpus(t *testing.T) {
	t.Parallel()

	tree := newTree(t)

	got, err := tree.Max()
	require.NoError(t, err, "an empty corpus is legal")
	assert.Equal(t, 0, got, "so the first reservation is 0001")
}

func TestSlugify(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Use Cargo workspace for lightwave-sys": "use-cargo-workspace-for-lightwave-sys",
		"Postgres IS the canonical store":       "postgres-is-the-canonical-store",
		"  leading and trailing  ":              "leading-and-trailing",
		"Punctuation: removed! (entirely)":      "punctuation-removed-entirely",
	}

	for in, want := range cases {
		in, want := in, want
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, want, adr.Slugify(in))
		})
	}
}

func TestReserveWritesAStampConformantSkeleton(t *testing.T) {
	t.Parallel()

	tree := newTree(t)
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)

	res, err := tree.Reserve("Postgres is the canonical store", "lightwave-core", "", now, false)
	require.NoError(t, err, "reserve into an empty corpus")

	assert.Equal(t, "CORE-0001", res.ID)
	assert.Equal(t, 1, res.Number)
	assert.Equal(t, "proposed", res.Status, "the adr_statuses enum default")
	assert.Equal(t, "0001-postgres-is-the-canonical-store.md", filepath.Base(res.Path))

	raw, err := os.ReadFile(res.Path)
	require.NoError(t, err, "the file exists on disk")

	body := string(raw)

	for _, key := range stampFrontmatterRequired {
		assert.Contains(t, body, key+":", "spec_artifact_kinds frontmatter_required: %s", key)
	}

	for _, section := range stampRequiredSections {
		assert.Contains(t, body, "## "+section, "spec_artifact_kinds required_sections: %s", section)
	}

	assert.Contains(t, body, `adr_id: "CORE-0001"`, "core uses adr_id, matching its 50+ siblings")
	assert.Contains(t, body, "decided_at: 2026-09-14", "the key the validator enforces")
	assert.Contains(t, body, "supersedes: null", "explicit null, not an absent key")
}

func TestReserveRecordsSupersedes(t *testing.T) {
	t.Parallel()

	tree := newTree(t)

	res, err := tree.Reserve("Replace the old thing", "lightwave-core", "CORE-0007", time.Now(), false)
	require.NoError(t, err)

	raw, err := os.ReadFile(res.Path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `supersedes: "CORE-0007"`)
}

func TestReserveHostTreeUsesItsOwnShape(t *testing.T) {
	t.Parallel()

	tree := adr.Tree{
		Name:       "host",
		Dir:        t.TempDir(),
		IDPrefix:   "ADR",
		IDKey:      "id",
		FilePrefix: "ADR-",
	}

	res, err := tree.Reserve("Local first index", "lightwave-harness", "", time.Now(), false)
	require.NoError(t, err)

	assert.Equal(t, "ADR-0001", res.ID)
	assert.Equal(t, "ADR-0001-local-first-index.md", filepath.Base(res.Path))

	raw, err := os.ReadFile(res.Path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `id: "ADR-0001"`, "host uses id, not adr_id")
	assert.NotContains(t, string(raw), "adr_id:", "writing core's key here would mismatch its siblings")
}

// TestConcurrentReserveNeverCollides is the regression test for the defect this
// package exists for.
//
// lightwave-core's spec/adr/ carries ELEVEN double-assigned numbers, and
// ~/.lightwave/specs/adr/ADR-0032 documents a renumber forced by one of them.
// Read-then-write is the cause: two sessions both read max=N and both write
// N+1. Running the reservation from many goroutines reproduces that directly —
// this test fails against any implementation that scans outside its lock.
func TestConcurrentReserveNeverCollides(t *testing.T) {
	t.Parallel()

	tree := newTree(t)

	const writers = 24

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		ids  = make([]string, 0, writers)
		errs = make([]error, 0)
	)

	wg.Add(writers)

	for i := range writers {
		go func(n int) {
			defer wg.Done()

			res, err := tree.Reserve(fmt.Sprintf("Decision number %d", n), "lightwave-core", "", time.Now(), false)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				errs = append(errs, err)

				return
			}

			ids = append(ids, res.ID)
		}(i)
	}

	wg.Wait()

	require.Empty(t, errs, "every concurrent reservation should succeed")
	require.Len(t, ids, writers, "one id per writer")

	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		assert.False(t, seen[id], "id %s was handed out twice", id)
		seen[id] = true
	}

	// The ids must also be the contiguous run 0001..N. A lock that serialised
	// writes but let the scan go stale would produce unique-but-sparse ids.
	for i := 1; i <= writers; i++ {
		assert.True(t, seen[fmt.Sprintf("CORE-%04d", i)], "CORE-%04d missing from the run", i)
	}

	entries, err := os.ReadDir(tree.Dir)
	require.NoError(t, err)

	written := 0

	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			written++
		}
	}

	assert.Equal(t, writers, written, "one file per reservation, none clobbered")
}

func TestDryRunConsumesNothing(t *testing.T) {
	t.Parallel()

	tree := newTree(t)

	first, err := tree.Reserve("Preview only", "lightwave-core", "", time.Now(), true)
	require.NoError(t, err)
	assert.True(t, first.DryRun)
	assert.Equal(t, "CORE-0001", first.ID)

	// A preview that consumed an id would hand the next caller 0002 and leave
	// 0001 permanently unused.
	second, err := tree.Reserve("Preview again", "lightwave-core", "", time.Now(), true)
	require.NoError(t, err)
	assert.Equal(t, "CORE-0001", second.ID, "a dry run must not advance the sequence")

	entries, err := os.ReadDir(tree.Dir)
	require.NoError(t, err)

	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".md", "a dry run must not write a file")
	}
}

func TestReserveRefusesAnEmptyTitle(t *testing.T) {
	t.Parallel()

	tree := newTree(t)

	_, err := tree.Reserve("   ", "lightwave-core", "", time.Now(), false)
	require.Error(t, err, "an untitled ADR is not a decision record")
}

func TestReserveRefusesATitleThatSlugifiesToNothing(t *testing.T) {
	t.Parallel()

	tree := newTree(t)

	// Without this guard the filename becomes `0001-.md`, which parses back to
	// a number and quietly poisons the next scan.
	_, err := tree.Reserve("!!! ???", "lightwave-core", "", time.Now(), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slugifies to nothing")
}

func TestReserveRefusesAMissingTree(t *testing.T) {
	t.Parallel()

	tree := adr.Tree{
		Name:     "core",
		Dir:      filepath.Join(t.TempDir(), "does-not-exist"),
		IDPrefix: "CORE",
		IDKey:    "adr_id",
	}

	_, err := tree.Reserve("Anything", "lightwave-core", "", time.Now(), false)
	require.Error(t, err, "creating the corpus is not this verb's job")
	assert.Contains(t, err.Error(), "no ADR tree at")
}

func TestTreeForRejectsAnUnknownTree(t *testing.T) {
	t.Parallel()

	_, err := adr.TreeFor("platform", "/root", "/home")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected core or host")
}

func TestTreeForRequiresATree(t *testing.T) {
	t.Parallel()

	// Defaulting would file the decision into the wrong repository — expensive
	// to notice, annoying to undo.
	_, err := adr.TreeFor("", "/root", "/home")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--tree is required")
}

func TestTreeForResolvesBothCorpora(t *testing.T) {
	t.Parallel()

	core, err := adr.TreeFor("core", "/root", "/home")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/root", "lightwave-core", "spec", "adr"), core.Dir)
	assert.Equal(t, "adr_id", core.IDKey)

	host, err := adr.TreeFor("host", "/root", "/home")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/home", ".lightwave", "specs", "adr"), host.Dir)
	assert.Equal(t, "id", host.IDKey)
}
