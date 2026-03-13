package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sprint represents a createOS sprint
type Sprint struct {
	ID        string     `db:"id"`
	Name      string     `db:"name"`
	Status    string     `db:"status"`
	Objectives *string   `db:"objectives"`
	StartDate *time.Time `db:"start_date"`
	EndDate   *time.Time `db:"end_date"`
	EpicID    *string    `db:"epic_id"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt time.Time  `db:"updated_at"`
	// Computed
	ShortID string
}

// SprintListOptions for filtering sprints
type SprintListOptions struct {
	Status string
	EpicID string
	Limit  int
}

// ListSprints queries sprints from PostgreSQL
func ListSprints(ctx context.Context, pool *pgxpool.Pool, opts SprintListOptions) ([]Sprint, error) {
	query := `
		SELECT id, name, status, objectives, start_date, end_date, epic_id, created_at, updated_at
		FROM createos_sprint
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
		query += fmt.Sprintf(" AND status IN (%s)", strings.Join(placeholders, ", "))
	}

	if opts.EpicID != "" {
		query += fmt.Sprintf(" AND epic_id::text LIKE $%d || '%%'", argNum)
		args = append(args, opts.EpicID)
		argNum++
	}

	query += " ORDER BY created_at DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 50"
	}

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query sprints: %w", err)
	}
	defer rows.Close()

	var sprints []Sprint
	for rows.Next() {
		var s Sprint
		err := rows.Scan(
			&s.ID, &s.Name, &s.Status, &s.Objectives,
			&s.StartDate, &s.EndDate, &s.EpicID,
			&s.CreatedAt, &s.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan sprint: %w", err)
		}
		if len(s.ID) >= 8 {
			s.ShortID = s.ID[:8]
		}
		sprints = append(sprints, s)
	}

	return sprints, nil
}

// SprintCreateOptions holds fields for creating a sprint
type SprintCreateOptions struct {
	Name       string
	Objectives string
	EpicID     string
	StartDate  string
	EndDate    string
	Status     string
}

// CreateSprint inserts a new sprint into createos_sprint
func CreateSprint(ctx context.Context, pool *pgxpool.Pool, opts SprintCreateOptions) (*Sprint, error) {
	id := uuid.New().String()
	now := time.Now()

	query := `
		INSERT INTO createos_sprint (id, name, status, objectives, start_date, end_date, epic_id, notion_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		RETURNING id, name, status
	`

	objectives := opts.Objectives
	notionID := "cli-" + id[:8]
	var epicID *string
	if opts.EpicID != "" {
		epicID = &opts.EpicID
	}
	var startDate, endDate *time.Time
	if opts.StartDate != "" {
		t, err := time.Parse("2006-01-02", opts.StartDate)
		if err != nil {
			return nil, fmt.Errorf("invalid start-date format (use YYYY-MM-DD): %w", err)
		}
		startDate = &t
	}
	if opts.EndDate != "" {
		t, err := time.Parse("2006-01-02", opts.EndDate)
		if err != nil {
			return nil, fmt.Errorf("invalid end-date format (use YYYY-MM-DD): %w", err)
		}
		endDate = &t
	}

	var s Sprint
	err := pool.QueryRow(ctx, query,
		id, opts.Name, opts.Status, objectives, startDate, endDate, epicID, notionID, now,
	).Scan(&s.ID, &s.Name, &s.Status)
	if err != nil {
		return nil, fmt.Errorf("failed to create sprint: %w", err)
	}

	if len(s.ID) >= 8 {
		s.ShortID = s.ID[:8]
	}
	return &s, nil
}
