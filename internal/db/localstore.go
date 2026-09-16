// Package db holds the CLI's data access: the Postgres platform pool and the
// local-first SQLite store that backs the desktop surface.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Pure-Go driver: the CLI ships via GoReleaser cross-compile, and a cgo
	// SQLite driver would require a C toolchain per target.
	_ "modernc.org/sqlite"
)

// The local-first store, per lightwave-core ADR-0004 ("the local core is
// primary ... fully functional offline"). createOS opens this same file
// READ_ONLY; `lw` is the writer side of that contract.
//
// Until this file existed, `lw` had no SQLite driver at all — only pgx — so the
// agile verbs queried `createos_*` tables belonging to the Django-era platform
// that was archived at tag `legacy/django-era-platform` (2026-06-03). Nothing
// generates those tables any more, so every one of those queries failed with
// `relation "createos_epic" does not exist`. See createOS#92.
const localStoreEnv = "LW_LOCAL_STORE"

// Artifacts are versioned in this store; the v_current_* views expose the
// current revision of each. Querying the base tables would return superseded
// rows alongside live ones.
const currentEpicsView = "v_current_epics"
const currentStoriesView = "v_current_user_stories"
const currentSprintsView = "v_current_sprints"

// LocalStorePath resolves the local-first store, honouring LW_LOCAL_STORE so
// tests can point at a fixture without touching the operator's real store.
func LocalStorePath() (string, error) {
	if p := os.Getenv(localStoreEnv); p != "" {
		return p, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(home, ".lightwave", "index", "lightwave.db"), nil
}

// OpenLocalStore opens the local-first store. A missing file is reported as
// such rather than surfacing as an empty result set, because "no epics" and
// "no store" are different problems with different fixes.
func OpenLocalStore() (*sql.DB, error) {
	path, err := LocalStorePath()
	if err != nil {
		return nil, err
	}

	if _, statErr := os.Stat(path); statErr != nil {
		return nil, missingStoreError(path)
	}

	handle, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	return handle, nil
}

// missingStoreError explains a missing store rather than forwarding a bare stat
// failure.
//
// #463: the store was renamed to `lightwave.db.retired-<date>` and every agile
// read verb began failing with
//
//	local-first store not found at …/lightwave.db: stat …: no such file or directory
//
// which states the problem twice and none of what the reader needs next. Three
// things were knowable at the point of failure and none were offered: that
// LW_LOCAL_STORE redirects the lookup, that a retired copy was sitting in the
// same directory, and that createOS reads this same path — so whether to
// restore it is not lw's decision alone (localstore.go's own header records
// that contract, and createOS reads the path from both Rust and TypeScript).
//
// The retired-sibling scan is deliberately narrow: the exact `.retired-*`
// suffix, in the same directory, nothing else. A broad guess at where a store
// "might" be is how a wrong path becomes a confident answer — the failure mode
// this package has already shipped twice.
func missingStoreError(path string) error {
	msg := "local-first store not found at " + path

	// Glob's only error is a malformed pattern, and the pattern is a literal
	// suffix on a path we were just given. No match and a bad pattern both mean
	// "nothing to offer", which is the same branch.
	if retired, _ := filepath.Glob(path + ".retired-*"); len(retired) > 0 {
		return fmt.Errorf("%s\n  a retired copy is beside it: %s\n"+
			"  read it with: %s=%s\n"+
			"  createOS reads this same path, so restoring it is not lw's call alone",
			msg, filepath.Base(retired[0]), localStoreEnv, retired[0])
	}

	return fmt.Errorf("%s\n  point %s at a store to use a different one", msg, localStoreEnv)
}

// listLocal runs one query against the local-first store and scans every row.
//
// Epics, stories and sprints differ only in their query and their scanner —
// open, iterate, check rows.Err, close is identical for all three. Written out
// per entity it was three byte-identical bodies, which `dupl` flagged and which
// means a fix to the iteration (a missing rows.Err, a leaked handle) has to be
// made three times.
//
// `kind` appears only in error text, so a failure names the entity rather than
// reading "query from local store".
func listLocal[T any](
	ctx context.Context,
	kind string,
	query string,
	args []any,
	scan func(*sql.Rows) (T, error),
) ([]T, error) {
	handle, err := OpenLocalStore()
	if err != nil {
		return nil, err
	}

	defer func() { _ = handle.Close() }()

	rows, err := handle.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query %s from local store: %w", kind, err)
	}

	defer func() { _ = rows.Close() }()

	var out []T

	for rows.Next() {
		item, scanErr := scan(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		out = append(out, item)
	}

	// Checked separately from the loop: rows.Next() returns false both at the
	// end of a healthy result set and on a mid-iteration failure, so without
	// this a truncated read looks like a short list.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s rows: %w", kind, err)
	}

	return out, nil
}

// ListEpicsLocal reads epics from the local-first store.
//
// `slug` is the identifier users and agents actually type, and it is what
// tasks.epic_ref points at, so it serves as the display ID; the store's `pk` is
// an internal UUID that appears in no other surface.
func ListEpicsLocal(ctx context.Context, opts EpicListOptions) ([]Epic, error) {
	query, args := buildEpicQuery(opts)

	return listLocal(ctx, "epics", query, args, scanEpic)
}

func buildEpicQuery(opts EpicListOptions) (string, []any) {
	query := `
		SELECT e.slug, e.name, e.status, COALESCE(e.priority, ''),
		       COALESCE(e.created_at, ''), COALESCE(e.updated_at, ''),
		       (SELECT COUNT(*) FROM tasks t WHERE t.epic_ref = e.slug) AS task_count
		FROM ` + currentEpicsView + ` e`

	var args []any

	// A filter of "," or "  " parses to zero statuses; strings.Repeat with a
	// negative count panics, so the emptiness test must be on the parsed
	// statuses rather than on the raw flag.
	if statuses := splitStatuses(opts.Status); len(statuses) > 0 {
		query += " WHERE e.status IN (?" + strings.Repeat(", ?", len(statuses)-1) + ")"

		for _, s := range statuses {
			args = append(args, s)
		}
	}

	query += " ORDER BY e.name"

	if opts.Limit > 0 {
		query += " LIMIT ?"

		args = append(args, opts.Limit)
	}

	return query, args
}

func scanEpic(rows *sql.Rows) (Epic, error) {
	var (
		epic                 Epic
		priority             string
		createdAt, updatedAt string
	)

	if err := rows.Scan(&epic.ID, &epic.Name, &epic.Status, &priority,
		&createdAt, &updatedAt, &epic.TaskCount); err != nil {
		return Epic{}, fmt.Errorf("scan epic row: %w", err)
	}

	if priority != "" {
		epic.Priority = &priority
	}

	// The Postgres path derived ShortID as ID[:8] because its ID was a UUID.
	// Here the ID is already the slug — short, meaningful, and the value
	// tasks.epic_ref joins on — so truncating it would destroy the identifier
	// ("cineos-slice3-stamp" -> "cineos-s").
	epic.ShortID = epic.ID
	epic.CreatedAt = parseStoreTime(createdAt)
	epic.UpdatedAt = parseStoreTime(updatedAt)

	// GithubRepo stays nil: the stamp has no github_repo field, so the local
	// print has no column for it. The table renders "-".
	return epic, nil
}

// ListStoriesLocal reads user stories from the local-first store.
//
// Same identifier reasoning as epics: `slug` is what a person types and what
// other rows reference (`sprint_ref`, `epic_ref`), so it is the display ID.
func ListStoriesLocal(ctx context.Context, opts StoryListOptions) ([]Story, error) {
	query, args := buildStoryQuery(opts)

	return listLocal(ctx, "stories", query, args, scanLocalStory)
}

func buildStoryQuery(opts StoryListOptions) (string, []any) {
	// Same nullable-name guard as sprints. No current story is unnamed, but the
	// column allows it and the failure mode is a driver error that kills the
	// entire listing rather than one row. ListEpicsLocal has the same latent
	// shape and is left alone — merged code, no observed nulls.
	query := `
		SELECT s.slug, COALESCE(NULLIF(s.name, ''), s.slug), s.description,
		       s.status, COALESCE(s.priority, ''),
		       s.user_type, s.story_points, s.epic_ref, s.sprint_ref,
		       COALESCE(s.created_at, ''), COALESCE(s.updated_at, '')
		FROM ` + currentStoriesView + ` s`

	var (
		where []string
		args  []any
	)

	if statuses := splitStatuses(opts.Status); len(statuses) > 0 {
		where = append(where, "s.status IN (?"+strings.Repeat(", ?", len(statuses)-1)+")")

		for _, s := range statuses {
			args = append(args, s)
		}
	}

	// The Postgres path filtered on a UUID epic_id; here the join key is the
	// slug, which is also what the caller has in hand.
	if opts.EpicID != "" {
		where = append(where, "s.epic_ref = ?")
		args = append(args, opts.EpicID)
	}

	if opts.SprintID != "" {
		where = append(where, "s.sprint_ref = ?")
		args = append(args, opts.SprintID)
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY s.name"

	if opts.Limit > 0 {
		query += " LIMIT ?"

		args = append(args, opts.Limit)
	}

	return query, args
}

func scanLocalStory(rows *sql.Rows) (Story, error) {
	var (
		story                                     Story
		description, userType, epicRef, sprintRef sql.NullString
		points                                    sql.NullInt64
		priority, createdAt, updatedAt            string
	)

	if err := rows.Scan(&story.ID, &story.Name, &description, &story.Status, &priority,
		&userType, &points, &epicRef, &sprintRef, &createdAt, &updatedAt); err != nil {
		return Story{}, fmt.Errorf("scan story row: %w", err)
	}

	story.ShortID = story.ID
	story.Priority = priority
	story.Description = nullableString(description)
	story.UserType = nullableString(userType)
	story.EpicID = nullableString(epicRef)
	story.SprintID = nullableString(sprintRef)

	if points.Valid {
		narrowed := int16(points.Int64)
		story.StoryPoints = &narrowed
	}

	story.CreatedAt = parseStoreTime(createdAt)
	story.UpdatedAt = parseStoreTime(updatedAt)

	return story, nil
}

// ListSprintsLocal reads sprints from the local-first store.
func ListSprintsLocal(ctx context.Context, opts SprintListOptions) ([]Sprint, error) {
	query, args := buildSprintQuery(opts)

	return listLocal(ctx, "sprints", query, args, scanLocalSprint)
}

func buildSprintQuery(opts SprintListOptions) (string, []any) {
	// `name` is nullable in this store and 3 of 9 current sprints have none —
	// scanning NULL into a string is a driver error, so `sprint list` died on
	// the whole listing because of three rows. Falling back to the slug shows
	// the identifier the row is known by rather than an empty cell.
	query := `
		SELECT sp.slug, COALESCE(NULLIF(sp.name, ''), sp.slug), sp.status,
		       sp.objectives, sp.start_date, sp.end_date,
		       sp.epic_ref, COALESCE(sp.created_at, ''), COALESCE(sp.updated_at, '')
		FROM ` + currentSprintsView + ` sp`

	var (
		where []string
		args  []any
	)

	if statuses := splitStatuses(opts.Status); len(statuses) > 0 {
		where = append(where, "sp.status IN (?"+strings.Repeat(", ?", len(statuses)-1)+")")

		for _, s := range statuses {
			args = append(args, s)
		}
	}

	if opts.EpicID != "" {
		where = append(where, "sp.epic_ref = ?")
		args = append(args, opts.EpicID)
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	// Newest first: `sprint current` wants the live one, and an undated sprint
	// sorts last rather than masking a dated one.
	query += " ORDER BY COALESCE(sp.start_date, '') DESC, sp.slug"

	if opts.Limit > 0 {
		query += " LIMIT ?"

		args = append(args, opts.Limit)
	}

	return query, args
}

func scanLocalSprint(rows *sql.Rows) (Sprint, error) {
	var (
		sprint               Sprint
		objectives, epicRef  sql.NullString
		startDate, endDate   sql.NullString
		createdAt, updatedAt string
	)

	if err := rows.Scan(&sprint.ID, &sprint.Name, &sprint.Status, &objectives,
		&startDate, &endDate, &epicRef, &createdAt, &updatedAt); err != nil {
		return Sprint{}, fmt.Errorf("scan sprint row: %w", err)
	}

	sprint.ShortID = sprint.ID
	sprint.Objectives = nullableString(objectives)
	sprint.EpicID = nullableString(epicRef)
	sprint.StartDate = nullableStoreTime(startDate)
	sprint.EndDate = nullableStoreTime(endDate)
	sprint.CreatedAt = parseStoreTime(createdAt)
	sprint.UpdatedAt = parseStoreTime(updatedAt)

	return sprint, nil
}

// A NULL column and an empty string are the same absence to these callers —
// both render as "-" — so they collapse to nil rather than to a pointer at "".
func nullableString(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}

	out := value.String

	return &out
}

// A date the store cannot parse is reported as absent rather than as the zero
// time, which would render as year 1 in a sprint table.
func nullableStoreTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}

	parsed := parseStoreTime(value.String)
	if parsed.IsZero() {
		return nil
	}

	return &parsed
}

func splitStatuses(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

// SQLite has no date type, so the local print stores timestamps as TEXT (a
// known fidelity gap tracked in createOS#92). A value that matches no layout
// yields the zero time rather than failing the listing.
func parseStoreTime(raw string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed
		}
	}

	return time.Time{}
}
