package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Story represents a createOS user story
type Story struct {
	ID          string    `db:"id"`
	Name        string    `db:"name"`
	Description *string   `db:"description"`
	Status      string    `db:"status"`
	Priority    string    `db:"priority"`
	UserType    *string   `db:"user_type"`
	StoryPoints *int16    `db:"story_points"`
	EpicID      *string   `db:"epic_id"`
	SprintID    *string   `db:"sprint_id"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
	// Computed
	ShortID string
}

// StoryListOptions for filtering stories
type StoryListOptions struct {
	Status   string
	EpicID   string
	SprintID string
	Limit    int
}

// ListStories queries user stories from PostgreSQL
func ListStories(ctx context.Context, pool *pgxpool.Pool, opts StoryListOptions) ([]Story, error) {
	query := `
		SELECT id, name, description, status, priority, user_type, story_points,
			epic_id, sprint_id, created_at, updated_at
		FROM createos_userstory
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

	if opts.SprintID != "" {
		query += fmt.Sprintf(" AND sprint_id::text LIKE $%d || '%%'", argNum)
		args = append(args, opts.SprintID)
	}

	query += " ORDER BY updated_at DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 50"
	}

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query stories: %w", err)
	}
	defer rows.Close()

	var stories []Story
	for rows.Next() {
		var s Story
		err := rows.Scan(
			&s.ID, &s.Name, &s.Description, &s.Status, &s.Priority, &s.UserType,
			&s.StoryPoints, &s.EpicID, &s.SprintID, &s.CreatedAt, &s.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan story: %w", err)
		}
		if len(s.ID) >= 8 {
			s.ShortID = s.ID[:8]
		}
		stories = append(stories, s)
	}

	return stories, nil
}

// StoryCreateOptions holds fields for creating a user story
type StoryCreateOptions struct {
	Name        string
	Description string
	Priority    string
	EpicID      string
	SprintID    string
	UserType    string
}

// CreateStory inserts a new user story into createos_userstory
func CreateStory(ctx context.Context, pool *pgxpool.Pool, opts StoryCreateOptions) (*Story, error) {
	id := uuid.New().String()
	now := time.Now()

	notionID := "cli-" + id[:8]
	query := `
		INSERT INTO createos_userstory (id, name, description, acceptance_criteria, status, priority, user_type,
			notion_id, current_interview_round, discovery_status, interview_transcript, personas,
			user_flows, edge_cases, technical_constraints, rbac_requirements, research_notes,
			epic_id, sprint_id, created_at, updated_at)
		VALUES ($1, $2, $3, '[]'::jsonb, 'draft', $4, $5,
			$6, '', 'not_started', '[]'::jsonb, '[]'::jsonb,
			'[]'::jsonb, '[]'::jsonb, '[]'::jsonb, '[]'::jsonb, '',
			$7, $8, $9, $9)
		RETURNING id, name, status, priority
	`

	var desc, userType, epicID, sprintID *string
	if opts.Description != "" {
		desc = &opts.Description
	}
	if opts.UserType != "" {
		userType = &opts.UserType
	}
	if opts.EpicID != "" {
		epicID = &opts.EpicID
	}
	if opts.SprintID != "" {
		sprintID = &opts.SprintID
	}

	var s Story
	err := pool.QueryRow(ctx, query,
		id, opts.Name, desc, opts.Priority, userType,
		notionID, epicID, sprintID, now,
	).Scan(&s.ID, &s.Name, &s.Status, &s.Priority)
	if err != nil {
		return nil, fmt.Errorf("failed to create story: %w", err)
	}

	if len(s.ID) >= 8 {
		s.ShortID = s.ID[:8]
	}
	return &s, nil
}

// GetStory finds a story by short ID prefix
func GetStory(ctx context.Context, pool *pgxpool.Pool, shortID string) (*Story, error) {
	query := `
		SELECT id, name, description, status, priority, user_type, story_points,
			epic_id, sprint_id, created_at, updated_at
		FROM createos_userstory
		WHERE id::text LIKE $1 || '%'
		LIMIT 2
	`
	rows, err := pool.Query(ctx, query, shortID)
	if err != nil {
		return nil, fmt.Errorf("failed to query story: %w", err)
	}
	defer rows.Close()

	var stories []Story
	for rows.Next() {
		var s Story
		err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Status, &s.Priority,
			&s.UserType, &s.StoryPoints, &s.EpicID, &s.SprintID, &s.CreatedAt, &s.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan story: %w", err)
		}
		if len(s.ID) >= 8 {
			s.ShortID = s.ID[:8]
		}
		stories = append(stories, s)
	}

	if len(stories) == 0 {
		return nil, fmt.Errorf("no story found matching '%s'", shortID)
	}
	if len(stories) > 1 {
		return nil, fmt.Errorf("ambiguous ID '%s' matches %d stories — use more characters", shortID, len(stories))
	}
	return &stories[0], nil
}

// StoryUpdateOptions holds fields for updating a story
type StoryUpdateOptions struct {
	Status   *string
	Name     *string
	Priority *string
}

// UpdateStory updates specified fields of a story
func UpdateStory(ctx context.Context, pool *pgxpool.Pool, storyID string, opts StoryUpdateOptions) (*Story, error) {
	story, err := GetStory(ctx, pool, storyID)
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

	args = append(args, story.ID)
	query := fmt.Sprintf("UPDATE createos_userstory SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "), argNum)

	_, err = pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update story: %w", err)
	}
	return story, nil
}
