package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Task represents a createOS task
type Task struct {
	ID            string     `db:"id" json:"id"`
	Title         string     `db:"title" json:"title"`
	Description   *string    `db:"description" json:"description,omitempty"`
	Status        string     `db:"status" json:"status"`
	Priority      string     `db:"priority" json:"priority"`
	TaskType      string     `db:"task_type" json:"task_type"`
	TaskCategory  string     `db:"task_category" json:"task_category"`
	AgentStatus   *string    `db:"agent_status" json:"agent_status,omitempty"`
	AssignedAgent *string    `db:"assigned_agent" json:"assigned_agent,omitempty"`
	EpicID        *string    `db:"epic_id" json:"epic_id,omitempty"`
	SprintID      *string    `db:"sprint_id" json:"sprint_id,omitempty"`
	DueDate       *time.Time `db:"due_date" json:"due_date,omitempty"`
	DoDate        *time.Time `db:"do_date" json:"do_date,omitempty"`
	BranchName    *string    `db:"branch_name" json:"branch_name,omitempty"`
	PRUrl         *string    `db:"pr_url" json:"pr_url,omitempty"`
	CreatedAt     time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at" json:"updated_at"`
	// Computed
	ShortID string `json:"short_id"`
}

// TaskListOptions for filtering tasks
type TaskListOptions struct {
	Status      string
	Priority    string
	TaskType    string
	Category    string
	AgentStatus string
	EpicID      string
	SprintID    string
	Limit       int
}

// ListTasks queries tasks from PostgreSQL (Tier 2 - direct DB)
func ListTasks(ctx context.Context, pool *pgxpool.Pool, opts TaskListOptions) ([]Task, error) {
	query := `
		SELECT
			id, title, description, status, priority,
			task_type, task_category, agent_status, assigned_agent,
			epic_id, sprint_id, due_date, do_date,
			branch_name, pr_url, created_at, updated_at
		FROM createos_task
		WHERE 1=1
	`
	var args []interface{}
	argNum := 1

	// Build WHERE clause dynamically
	if opts.Status != "" {
		// Support comma-separated statuses
		statuses := strings.Split(opts.Status, ",")
		placeholders := make([]string, len(statuses))
		for i, s := range statuses {
			placeholders[i] = fmt.Sprintf("$%d", argNum)
			args = append(args, strings.TrimSpace(s))
			argNum++
		}
		query += fmt.Sprintf(" AND status IN (%s)", strings.Join(placeholders, ", "))
	}

	if opts.Priority != "" {
		query += fmt.Sprintf(" AND priority = $%d", argNum)
		args = append(args, opts.Priority)
		argNum++
	}

	if opts.TaskType != "" {
		query += fmt.Sprintf(" AND task_type = $%d", argNum)
		args = append(args, opts.TaskType)
		argNum++
	}

	if opts.Category != "" {
		query += fmt.Sprintf(" AND task_category = $%d", argNum)
		args = append(args, opts.Category)
		argNum++
	}

	if opts.AgentStatus != "" {
		query += fmt.Sprintf(" AND agent_status = $%d", argNum)
		args = append(args, opts.AgentStatus)
		argNum++
	}

	if opts.EpicID != "" {
		query += fmt.Sprintf(" AND epic_id = $%d", argNum)
		args = append(args, opts.EpicID)
		argNum++
	}

	if opts.SprintID != "" {
		query += fmt.Sprintf(" AND sprint_id::text LIKE $%d || '%%'", argNum)
		args = append(args, opts.SprintID)
	}

	// Order by updated_at descending (most recent first)
	query += " ORDER BY updated_at DESC"

	// Limit results
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 50" // Default limit
	}

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		err := rows.Scan(
			&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
			&t.TaskType, &t.TaskCategory, &t.AgentStatus, &t.AssignedAgent,
			&t.EpicID, &t.SprintID, &t.DueDate, &t.DoDate,
			&t.BranchName, &t.PRUrl, &t.CreatedAt, &t.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}
		// Generate short ID (first 8 chars of UUID)
		if len(t.ID) >= 8 {
			t.ShortID = t.ID[:8]
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}

// GetTask retrieves a single task by ID or short ID
func GetTask(ctx context.Context, pool *pgxpool.Pool, taskID string) (*Task, error) {
	query := `
		SELECT
			id, title, description, status, priority,
			task_type, task_category, agent_status, assigned_agent,
			epic_id, sprint_id, due_date, do_date,
			branch_name, pr_url, created_at, updated_at
		FROM createos_task
		WHERE id::text LIKE $1 || '%'
		LIMIT 1
	`

	var t Task
	err := pool.QueryRow(ctx, query, taskID).Scan(
		&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&t.TaskType, &t.TaskCategory, &t.AgentStatus, &t.AssignedAgent,
		&t.EpicID, &t.SprintID, &t.DueDate, &t.DoDate,
		&t.BranchName, &t.PRUrl, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}

	if len(t.ID) >= 8 {
		t.ShortID = t.ID[:8]
	}

	return &t, nil
}

// GetTaskByNotionID finds a task by its notion_id field (used for external refs like "gh-52")
func GetTaskByNotionID(ctx context.Context, pool *pgxpool.Pool, notionID string) (*Task, error) {
	query := `
		SELECT
			id, title, description, status, priority,
			task_type, task_category, agent_status, assigned_agent,
			epic_id, sprint_id, due_date, do_date,
			branch_name, pr_url, created_at, updated_at
		FROM createos_task
		WHERE notion_id = $1
		LIMIT 1
	`

	var t Task
	err := pool.QueryRow(ctx, query, notionID).Scan(
		&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority,
		&t.TaskType, &t.TaskCategory, &t.AgentStatus, &t.AssignedAgent,
		&t.EpicID, &t.SprintID, &t.DueDate, &t.DoDate,
		&t.BranchName, &t.PRUrl, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("task not found by notion_id %s: %w", notionID, err)
	}

	if len(t.ID) >= 8 {
		t.ShortID = t.ID[:8]
	}

	return &t, nil
}

// StatusDisplay returns a human-readable status with color hint
func (t *Task) StatusDisplay() string {
	switch t.Status {
	case "on_hold":
		return "On Hold"
	case "approved":
		return "Approved"
	case "next_up":
		return "Next Up"
	case "future":
		return "Future"
	case "in_progress":
		return "In Progress"
	case "in_review":
		return "In Review"
	case "archived":
		return "Archived"
	case "cancelled":
		return "Cancelled"
	default:
		return t.Status
	}
}

// TaskCreateOptions holds fields for creating a task
type TaskCreateOptions struct {
	Title              string
	Description        string
	AcceptanceCriteria string
	Priority           string
	TaskType           string
	Category           string
	EpicID             string
	SprintID           string
	StoryID            string
	NotionID           string // external ref key (e.g. "gh-52" for GitHub Issue #52)
}

// CreateTask inserts a new task into createos_task
func CreateTask(ctx context.Context, pool *pgxpool.Pool, opts TaskCreateOptions) (*Task, error) {
	id := uuid.New().String()
	now := time.Now()

	notionID := opts.NotionID
	if notionID == "" {
		notionID = "cli-" + id[:8]
	}

	query := `
		INSERT INTO createos_task (id, title, description, acceptance_criteria, priority, task_type, task_category,
			epic_id, sprint_id, user_story_id, status, agent_status, assigned_agent,
			branch_name, pr_url, ai_summary, note, notion_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'approved', 'idle', '',
			'', '', '', '', $11, $12, $12)
		RETURNING id, title, status, priority, task_type
	`

	var epicID, sprintID, storyID *string
	if opts.EpicID != "" {
		epic, err := GetEpic(ctx, pool, opts.EpicID)
		if err != nil {
			return nil, fmt.Errorf("resolving epic: %w", err)
		}
		epicID = &epic.ID
	}
	if opts.SprintID != "" {
		sprint, err := GetSprint(ctx, pool, opts.SprintID)
		if err != nil {
			return nil, fmt.Errorf("resolving sprint: %w", err)
		}
		sprintID = &sprint.ID
	}
	if opts.StoryID != "" {
		story, err := GetStory(ctx, pool, opts.StoryID)
		if err != nil {
			return nil, fmt.Errorf("resolving story: %w", err)
		}
		storyID = &story.ID
	}

	description := opts.Description

	var t Task
	err := pool.QueryRow(ctx, query,
		id, opts.Title, description, opts.AcceptanceCriteria, opts.Priority, opts.TaskType, opts.Category,
		epicID, sprintID, storyID, notionID, now,
	).Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.TaskType)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	if len(t.ID) >= 8 {
		t.ShortID = t.ID[:8]
	}
	return &t, nil
}

// TaskUpdateOptions holds fields for updating a task
type TaskUpdateOptions struct {
	Status      *string
	Priority    *string
	Agent       *string
	Branch      *string
	PRUrl       *string
	Title       *string
	Description *string
	EpicID      *string
	SprintID    *string
	StoryID     *string
}

// UpdateTask updates specified fields of a task
func UpdateTask(ctx context.Context, pool *pgxpool.Pool, taskID string, opts TaskUpdateOptions) (*Task, error) {
	// First find the task by short ID prefix
	task, err := GetTask(ctx, pool, taskID)
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
	if opts.Priority != nil {
		setClauses = append(setClauses, fmt.Sprintf("priority = $%d", argNum))
		args = append(args, *opts.Priority)
		argNum++
	}
	if opts.Agent != nil {
		setClauses = append(setClauses, fmt.Sprintf("assigned_agent = $%d", argNum))
		args = append(args, *opts.Agent)
		argNum++
	}
	if opts.Branch != nil {
		setClauses = append(setClauses, fmt.Sprintf("branch_name = $%d", argNum))
		args = append(args, *opts.Branch)
		argNum++
	}
	if opts.PRUrl != nil {
		setClauses = append(setClauses, fmt.Sprintf("pr_url = $%d", argNum))
		args = append(args, *opts.PRUrl)
		argNum++
	}
	if opts.Title != nil {
		setClauses = append(setClauses, fmt.Sprintf("title = $%d", argNum))
		args = append(args, *opts.Title)
		argNum++
	}
	if opts.Description != nil {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argNum))
		args = append(args, *opts.Description)
		argNum++
	}
	if opts.EpicID != nil {
		if *opts.EpicID == "" {
			setClauses = append(setClauses, fmt.Sprintf("epic_id = $%d", argNum))
			args = append(args, nil)
		} else {
			epic, err := GetEpic(ctx, pool, *opts.EpicID)
			if err != nil {
				return nil, fmt.Errorf("resolving epic: %w", err)
			}
			setClauses = append(setClauses, fmt.Sprintf("epic_id = $%d", argNum))
			args = append(args, epic.ID)
		}
		argNum++
	}
	if opts.SprintID != nil {
		if *opts.SprintID == "" {
			setClauses = append(setClauses, fmt.Sprintf("sprint_id = $%d", argNum))
			args = append(args, nil)
		} else {
			sprint, err := GetSprint(ctx, pool, *opts.SprintID)
			if err != nil {
				return nil, fmt.Errorf("resolving sprint: %w", err)
			}
			setClauses = append(setClauses, fmt.Sprintf("sprint_id = $%d", argNum))
			args = append(args, sprint.ID)
		}
		argNum++
	}
	if opts.StoryID != nil {
		if *opts.StoryID == "" {
			setClauses = append(setClauses, fmt.Sprintf("user_story_id = $%d", argNum))
			args = append(args, nil)
		} else {
			story, err := GetStory(ctx, pool, *opts.StoryID)
			if err != nil {
				return nil, fmt.Errorf("resolving story: %w", err)
			}
			setClauses = append(setClauses, fmt.Sprintf("user_story_id = $%d", argNum))
			args = append(args, story.ID)
		}
		argNum++
	}

	if len(setClauses) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argNum))
	args = append(args, time.Now())
	argNum++

	args = append(args, task.ID)
	query := fmt.Sprintf("UPDATE createos_task SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "), argNum)

	_, err = pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update task: %w", err)
	}

	return task, nil
}

// UpdateTaskNotionID sets the notion_id field on a task (used for GitHub issue cross-reference).
func UpdateTaskNotionID(ctx context.Context, pool *pgxpool.Pool, taskID string, notionID string) (*Task, error) {
	task, err := GetTask(ctx, pool, taskID)
	if err != nil {
		return nil, err
	}

	_, err = pool.Exec(ctx,
		"UPDATE createos_task SET notion_id = $1, updated_at = $2 WHERE id = $3",
		notionID, time.Now(), task.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update notion_id: %w", err)
	}

	return task, nil
}

// TaskContext is a task with resolved epic and sprint names
type TaskContext struct {
	Task
	EpicName     *string
	EpicStatus   *string
	SprintName   *string
	SprintStatus *string
	SprintStart  *time.Time
	SprintEnd    *time.Time
	StoryName    *string
}

// GetTaskContext retrieves a task with joined epic/sprint/story data
func GetTaskContext(ctx context.Context, pool *pgxpool.Pool, taskID string) (*TaskContext, error) {
	query := `
		SELECT
			t.id, t.title, t.description, t.status, t.priority,
			t.task_type, t.task_category, t.agent_status, t.assigned_agent,
			t.epic_id, t.sprint_id, t.due_date, t.do_date,
			t.branch_name, t.pr_url, t.created_at, t.updated_at,
			e.name, e.status,
			s.name, s.status, s.start_date, s.end_date,
			us.name
		FROM createos_task t
		LEFT JOIN createos_epic e ON t.epic_id = e.id
		LEFT JOIN createos_sprint s ON t.sprint_id = s.id
		LEFT JOIN createos_userstory us ON t.user_story_id = us.id
		WHERE t.id::text LIKE $1 || '%'
		LIMIT 1
	`

	var tc TaskContext
	err := pool.QueryRow(ctx, query, taskID).Scan(
		&tc.ID, &tc.Title, &tc.Description, &tc.Status, &tc.Priority,
		&tc.TaskType, &tc.TaskCategory, &tc.AgentStatus, &tc.AssignedAgent,
		&tc.EpicID, &tc.SprintID, &tc.DueDate, &tc.DoDate,
		&tc.BranchName, &tc.PRUrl, &tc.CreatedAt, &tc.UpdatedAt,
		&tc.EpicName, &tc.EpicStatus,
		&tc.SprintName, &tc.SprintStatus, &tc.SprintStart, &tc.SprintEnd,
		&tc.StoryName,
	)
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}

	if len(tc.ID) >= 8 {
		tc.ShortID = tc.ID[:8]
	}

	return &tc, nil
}

// PriorityDisplay returns a human-readable priority
func (t *Task) PriorityDisplay() string {
	switch t.Priority {
	case "p1_urgent":
		return "P1 Urgent"
	case "p2_high":
		return "P2 High"
	case "p3_medium":
		return "P3 Medium"
	case "p4_low":
		return "P4 Low"
	default:
		return t.Priority
	}
}
