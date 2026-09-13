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
		return nil, fmt.Errorf("local-first store not found at %s: %w", path, statErr)
	}

	handle, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	return handle, nil
}

// ListEpicsLocal reads epics from the local-first store.
//
// `slug` is the identifier users and agents actually type, and it is what
// tasks.epic_ref points at, so it serves as the display ID; the store's `pk` is
// an internal UUID that appears in no other surface.
func ListEpicsLocal(ctx context.Context, opts EpicListOptions) ([]Epic, error) {
	handle, err := OpenLocalStore()
	if err != nil {
		return nil, err
	}

	defer func() { _ = handle.Close() }()

	query, args := buildEpicQuery(opts)

	rows, err := handle.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query epics from local store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var epics []Epic

	for rows.Next() {
		epic, scanErr := scanEpic(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		epics = append(epics, epic)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate epic rows: %w", err)
	}

	return epics, nil
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
