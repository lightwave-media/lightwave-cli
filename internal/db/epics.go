package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Epic represents a createOS epic
type Epic struct {
	ID         string     `db:"id"`
	Name       string     `db:"name"`
	Status     string     `db:"status"`
	Priority   *string    `db:"priority"`
	GithubRepo *string    `db:"github_repo"`
	StartDate  *time.Time `db:"start_date"`
	TargetDate *time.Time `db:"target_date"`
	CreatedAt  time.Time  `db:"created_at"`
	UpdatedAt  time.Time  `db:"updated_at"`
	TaskCount  int        `db:"task_count"`
	// Computed
	ShortID string
}

// EpicListOptions for filtering epics
type EpicListOptions struct {
	Status string
	Limit  int
}

// ListEpics queries epics from PostgreSQL with task count
func ListEpics(ctx context.Context, pool *pgxpool.Pool, opts EpicListOptions) ([]Epic, error) {
	query := `
		SELECT e.id, e.name, e.status, e.priority, e.github_repo,
			e.start_date, e.target_date, e.created_at, e.updated_at,
			COALESCE((SELECT COUNT(*) FROM createos_task t WHERE t.epic_id = e.id), 0) AS task_count
		FROM createos_epic e
		WHERE 1=1
	`
	var args []interface{}
	argNum := 1

	if opts.Status != "" {
		statuses := strings.Split(opts.Status, ",")
		placeholders := make([]string, len(statuses))
		for i, s := range statuses {
			placeholders[i] = fmt.Sprintf("$%d", argNum)
			args = append(args, strings.TrimSpace(s))
			argNum++
		}
		query += fmt.Sprintf(" AND e.status IN (%s)", strings.Join(placeholders, ", "))
	}

	query += " ORDER BY e.updated_at DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 50"
	}

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query epics: %w", err)
	}
	defer rows.Close()

	var epics []Epic
	for rows.Next() {
		var e Epic
		err := rows.Scan(
			&e.ID, &e.Name, &e.Status, &e.Priority, &e.GithubRepo,
			&e.StartDate, &e.TargetDate, &e.CreatedAt, &e.UpdatedAt,
			&e.TaskCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan epic: %w", err)
		}
		if len(e.ID) >= 8 {
			e.ShortID = e.ID[:8]
		}
		epics = append(epics, e)
	}

	return epics, nil
}

// GetEpic finds an epic by short ID prefix
func GetEpic(ctx context.Context, pool *pgxpool.Pool, shortID string) (*Epic, error) {
	query := `
		SELECT e.id, e.name, e.status, e.priority, e.github_repo,
			e.start_date, e.target_date, e.created_at, e.updated_at,
			COALESCE((SELECT COUNT(*) FROM createos_task t WHERE t.epic_id = e.id), 0) AS task_count
		FROM createos_epic e
		WHERE e.id::text LIKE $1 || '%'
		LIMIT 2
	`
	rows, err := pool.Query(ctx, query, shortID)
	if err != nil {
		return nil, fmt.Errorf("failed to query epic: %w", err)
	}
	defer rows.Close()

	var epics []Epic
	for rows.Next() {
		var e Epic
		err := rows.Scan(&e.ID, &e.Name, &e.Status, &e.Priority, &e.GithubRepo,
			&e.StartDate, &e.TargetDate, &e.CreatedAt, &e.UpdatedAt, &e.TaskCount)
		if err != nil {
			return nil, fmt.Errorf("failed to scan epic: %w", err)
		}
		if len(e.ID) >= 8 {
			e.ShortID = e.ID[:8]
		}
		epics = append(epics, e)
	}

	if len(epics) == 0 {
		return nil, fmt.Errorf("no epic found matching '%s'", shortID)
	}
	if len(epics) > 1 {
		return nil, fmt.Errorf("ambiguous ID '%s' matches %d epics — use more characters", shortID, len(epics))
	}
	return &epics[0], nil
}

// EpicUpdateOptions holds fields for updating an epic
type EpicUpdateOptions struct {
	Status   *string
	Name     *string
	Priority *string
}

// UpdateEpic updates specified fields of an epic
func UpdateEpic(ctx context.Context, pool *pgxpool.Pool, epicID string, opts EpicUpdateOptions) (*Epic, error) {
	epic, err := GetEpic(ctx, pool, epicID)
	if err != nil {
		return nil, err
	}

	setClauses := []string{}
	args := []interface{}{}
	argNum := 1

	if opts.Status != nil {
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", argNum))
		args = append(args, *opts.Status)
		argNum++
	}
	if opts.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argNum))
		args = append(args, *opts.Name)
		argNum++
	}
	if opts.Priority != nil {
		setClauses = append(setClauses, fmt.Sprintf("priority = $%d", argNum))
		args = append(args, *opts.Priority)
		argNum++
	}

	if len(setClauses) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argNum))
	args = append(args, time.Now())
	argNum++

	args = append(args, epic.ID)
	query := fmt.Sprintf("UPDATE createos_epic SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "), argNum)

	_, err = pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update epic: %w", err)
	}
	return epic, nil
}
