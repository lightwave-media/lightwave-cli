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

		CREATE TABLE user_stories (
			pk TEXT PRIMARY KEY, slug TEXT, name TEXT, description TEXT,
			status TEXT, priority TEXT, user_type TEXT, story_points INTEGER,
			epic_ref TEXT, sprint_ref TEXT, active_state TEXT,
			created_at TEXT, updated_at TEXT
		);
		CREATE VIEW v_current_user_stories AS
			SELECT * FROM user_stories WHERE active_state = 'current';

		INSERT INTO user_stories VALUES
			('s-1', 'slice3-entities-stamped', 'Slice 3 entities', 'desc',
			 'ready', 'must_have', 'developer', 5,
			 'cineos-slice3-stamp', 'bl-s1-slice3-stamp', 'current',
			 '2026-08-14 20:11:38', '2026-08-14 20:11:38'),
			('s-2', 'formats-colour-enums', NULL, NULL,
			 'ready', 'should_have', NULL, NULL,
			 'cineos-slice3-stamp', NULL, 'current',
			 '2026-08-01 10:00:00', '2026-08-01 10:00:00'),
			('s-3', 'superseded-story', 'Old Revision', NULL,
			 'ready', 'low', NULL, NULL, 'rule-of-three', NULL, 'superseded',
			 '2026-07-01 10:00:00', '2026-07-01 10:00:00');

		CREATE TABLE sprints (
			pk TEXT PRIMARY KEY, slug TEXT, name TEXT, status TEXT,
			objectives TEXT, start_date TEXT, end_date TEXT, epic_ref TEXT,
			active_state TEXT, created_at TEXT, updated_at TEXT
		);
		CREATE VIEW v_current_sprints AS
			SELECT * FROM sprints WHERE active_state = 'current';

		-- 'sprint-001' is unnamed on purpose: 3 of 9 sprints in the real store
		-- have a NULL name, and scanning that into a string is a driver error
		-- that killed the whole listing.
		INSERT INTO sprints VALUES
			('sp-1', 'bl-s1-slice3-stamp', 'BL-S1 — Slice 3', 'active',
			 'stamp the entities', '2026-08-12', NULL, 'cineos-slice3-stamp',
			 'current', '2026-08-12 09:00:00', '2026-08-12 09:00:00'),
			('sp-2', 'sprint-001', NULL, 'planned',
			 NULL, NULL, NULL, NULL,
			 'current', '2026-07-01 09:00:00', '2026-07-01 09:00:00'),
			('sp-3', 'superseded-sprint', 'Old Revision', 'planned',
			 NULL, '2026-06-01', NULL, NULL, 'superseded',
			 '2026-06-01 09:00:00', '2026-06-01 09:00:00');
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
func TestListStoriesLocal(t *testing.T) {
	t.Setenv("LW_LOCAL_STORE", newFixtureStore(t))

	stories, err := db.ListStoriesLocal(t.Context(), db.StoryListOptions{Limit: 50})
	require.NoError(t, err, "list stories from local store")
	require.Len(t, stories, 2, "the superseded revision is excluded by the view")

	bySlug := map[string]db.Story{}
	for _, s := range stories {
		bySlug[s.ID] = s
	}

	stamped, ok := bySlug["slice3-entities-stamped"]
	require.True(t, ok)
	assert.Equal(t, "slice3-entities-stamped", stamped.ShortID, "slug is not truncated")
	require.NotNil(t, stamped.StoryPoints)
	assert.Equal(t, int16(5), *stamped.StoryPoints)
	require.NotNil(t, stamped.EpicID)
	assert.Equal(t, "cineos-slice3-stamp", *stamped.EpicID, "epic_ref is the join key, not a UUID")

	// A NULL column and an empty string are the same absence to the renderer.
	unnamed := bySlug["formats-colour-enums"]
	assert.Nil(t, unnamed.StoryPoints, "unpointed story reports no points, not zero")
	assert.Nil(t, unnamed.Description)
	assert.Nil(t, unnamed.SprintID)
	assert.Equal(t, "formats-colour-enums", unnamed.Name, "a nameless story falls back to its slug")
}

//nolint:paralleltest // see TestListEpicsLocal
func TestListStoriesLocalFilters(t *testing.T) {
	t.Setenv("LW_LOCAL_STORE", newFixtureStore(t))

	byEpic, err := db.ListStoriesLocal(t.Context(), db.StoryListOptions{EpicID: "cineos-slice3-stamp"})
	require.NoError(t, err)
	assert.Len(t, byEpic, 2, "both current stories belong to this epic")

	bySprint, err := db.ListStoriesLocal(t.Context(), db.StoryListOptions{SprintID: "bl-s1-slice3-stamp"})
	require.NoError(t, err)
	require.Len(t, bySprint, 1, "only one story is bound to that sprint")
	assert.Equal(t, "slice3-entities-stamped", bySprint[0].ID)
}

//nolint:paralleltest // see TestListEpicsLocal
func TestListSprintsLocal(t *testing.T) {
	t.Setenv("LW_LOCAL_STORE", newFixtureStore(t))

	sprints, err := db.ListSprintsLocal(t.Context(), db.SprintListOptions{Limit: 50})
	require.NoError(t, err, "list sprints from local store")
	require.Len(t, sprints, 2, "the superseded revision is excluded by the view")

	bySlug := map[string]db.Sprint{}
	for _, s := range sprints {
		bySlug[s.ID] = s
	}

	active, ok := bySlug["bl-s1-slice3-stamp"]
	require.True(t, ok)
	require.NotNil(t, active.StartDate)
	assert.Equal(t, 2026, active.StartDate.Year())
	assert.Nil(t, active.EndDate, "an open-ended sprint reports no end, not year 1")

	// The regression this fix exists for: `name` is nullable and 3 of 9 real
	// sprints have none. Scanning NULL into a string is a driver error that
	// killed the ENTIRE listing, not just the unnamed rows.
	unnamed, ok := bySlug["sprint-001"]
	require.True(t, ok, "an unnamed sprint is still listed")
	assert.Equal(t, "sprint-001", unnamed.Name, "a nameless sprint falls back to its slug")
	assert.Nil(t, unnamed.Objectives)
	assert.Nil(t, unnamed.StartDate)
}

//nolint:paralleltest // see TestListEpicsLocal
func TestListSprintsLocalStatusFilter(t *testing.T) {
	t.Setenv("LW_LOCAL_STORE", newFixtureStore(t))

	// This is what `lw sprint current` runs.
	active, err := db.ListSprintsLocal(t.Context(), db.SprintListOptions{Status: "active", Limit: 1})
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, "bl-s1-slice3-stamp", active[0].ID)

	all, err := db.ListSprintsLocal(t.Context(), db.SprintListOptions{Status: " , "})
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
