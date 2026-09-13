package db_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/lightwave-media/lightwave-cli/internal/db"
)

// Mirrors the shape of the real local-first store: versioned base table plus
// the v_current_* view that exposes the live revision. Querying the base table
// instead of the view would return superseded rows, so the fixture keeps both.
func newFixtureStore(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "lightwave.db")

	handle, err := sql.Open("sqlite", path)
	require.NoError(t, err, "open fixture store")

	defer func() { _ = handle.Close() }()

	_, err = handle.ExecContext(t.Context(), `
		CREATE TABLE epics (
			pk TEXT PRIMARY KEY, slug TEXT, name TEXT, status TEXT,
			priority TEXT, active_state TEXT, created_at TEXT, updated_at TEXT
		);
		CREATE VIEW v_current_epics AS
			SELECT * FROM epics WHERE active_state = 'current';
		CREATE TABLE tasks (pk TEXT PRIMARY KEY, epic_ref TEXT);

		INSERT INTO epics VALUES
			('uuid-1', 'cineos-slice3-stamp', 'cineOS Slice 3', 'in_progress',
			 'p1', 'current', '2026-08-14 20:11:38', '2026-08-14 20:11:38'),
			('uuid-2', 'rule-of-three', 'Work-tree Rule of 3', 'planned',
			 'high', 'current', '2026-08-01 10:00:00', '2026-08-01 10:00:00'),
			('uuid-3', 'superseded-epic', 'Old Revision', 'planned',
			 'low', 'superseded', '2026-07-01 10:00:00', '2026-07-01 10:00:00');

		INSERT INTO tasks VALUES ('t1', 'cineos-slice3-stamp'),
		                         ('t2', 'cineos-slice3-stamp'),
		                         ('t3', 'rule-of-three');
	`)
	require.NoError(t, err, "seed fixture store")
	return path
}

// Not parallel: t.Setenv and t.Parallel are mutually exclusive in Go, and the
// store location is resolved from the environment by design so tests never
// touch the operator's real ~/.lightwave/index/lightwave.db.
//
//nolint:paralleltest
func TestListEpicsLocal(t *testing.T) {
	t.Setenv("LW_LOCAL_STORE", newFixtureStore(t))

	epics, err := db.ListEpicsLocal(t.Context(), db.EpicListOptions{Limit: 50})
	require.NoError(t, err, "list epics from local store")

	// The superseded revision must not appear: the query reads the view, not
	// the base table.
	require.Len(t, epics, 2, "only current revisions are listed")

	bySlug := map[string]db.Epic{}
	for _, e := range epics {
		bySlug[e.ID] = e
	}

	cineos, ok := bySlug["cineos-slice3-stamp"]
	require.True(t, ok, "cineos-slice3-stamp present")

	// The join is tasks.epic_ref -> epics.slug, not -> epics.pk. Joining on pk
	// would silently yield zero for every epic.
	assert.Equal(t, 2, cineos.TaskCount, "task count joins epic_ref on slug")
	assert.Equal(t, 1, bySlug["rule-of-three"].TaskCount)

	// The slug IS the short id. The Postgres path truncated to 8 chars because
	// its identifier was a UUID; doing that here would render "cineos-s".
	assert.Equal(t, "cineos-slice3-stamp", cineos.ShortID, "slug is not truncated")

	// No github_repo column exists in the stamp, so the local print has none.
	assert.Nil(t, cineos.GithubRepo, "github repo is absent, not fabricated")

	require.NotNil(t, cineos.Priority)
	assert.Equal(t, "p1", *cineos.Priority)
	assert.Equal(t, 2026, cineos.CreatedAt.Year(), "TEXT timestamp parsed")
}

//nolint:paralleltest // see TestListEpicsLocal
func TestListEpicsLocalStatusFilter(t *testing.T) {
	t.Setenv("LW_LOCAL_STORE", newFixtureStore(t))

	epics, err := db.ListEpicsLocal(t.Context(), db.EpicListOptions{Status: "in_progress"})
	require.NoError(t, err)
	require.Len(t, epics, 1)
	assert.Equal(t, "cineos-slice3-stamp", epics[0].ID)

	// A filter that parses to zero statuses must not build "IN ()" — and must
	// not panic on strings.Repeat with a negative count.
	all, err := db.ListEpicsLocal(t.Context(), db.EpicListOptions{Status: " , "})
	require.NoError(t, err, "whitespace-only filter is treated as no filter")
	assert.Len(t, all, 2)
}

//nolint:paralleltest // see TestListEpicsLocal
func TestListEpicsLocalMissingStore(t *testing.T) {
	t.Setenv("LW_LOCAL_STORE", filepath.Join(t.TempDir(), "absent.db"))

	_, err := db.ListEpicsLocal(t.Context(), db.EpicListOptions{})
	// "No store" and "no epics" are different problems with different fixes,
	// so the missing file must surface rather than read as an empty listing.
	require.Error(t, err, "a missing store is an error, not an empty result")
	assert.Contains(t, err.Error(), "local-first store not found")
}
