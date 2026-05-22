package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/db"
)

// cleanupTimeout caps how long fixture cleanups can wait on the DB.
// 5 seconds is generous for a local Postgres; tests should never
// linger this long on a delete.
const cleanupTimeout = 5 * time.Second

// EpicOpt lets call sites override defaults: `MakeEpic(t, pool,
// WithEpicStatus("completed"))`. Composable; later options override
// earlier ones.
type EpicOpt func(*db.EpicCreateOptions)

// WithEpicName overrides the auto-generated test-epic-<rand> name.
func WithEpicName(name string) EpicOpt {
	return func(o *db.EpicCreateOptions) { o.Name = name }
}

// WithEpicStatus overrides the default "active" status.
func WithEpicStatus(status string) EpicOpt {
	return func(o *db.EpicCreateOptions) { o.Status = status }
}

// WithEpicPriority overrides the default "p3_medium" priority.
func WithEpicPriority(priority string) EpicOpt {
	return func(o *db.EpicCreateOptions) { o.Priority = priority }
}

// MakeEpic creates an Epic row with sensible defaults and registers a
// cleanup that deletes it when the test exits. Returns the created
// row (with ShortID populated) so the test can chain assertions or
// build child Sprints/Stories that reference it.
func MakeEpic(t *testing.T, pool *pgxpool.Pool, opts ...EpicOpt) *db.Epic {
	t.Helper()

	o := db.EpicCreateOptions{
		Name:     "test-epic-" + randSuffix(),
		Status:   "active",
		Priority: "p3_medium",
	}

	for _, fn := range opts {
		fn(&o)
	}

	e, err := db.CreateEpic(context.Background(), pool, o)
	require.NoError(t, err, "CreateEpic")

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		_, _ = pool.Exec(ctx, "DELETE FROM createos_epic WHERE id = $1", e.ID)
	})

	return e
}

// SprintOpt is the option type for MakeSprint.
type SprintOpt func(*db.SprintCreateOptions)

// WithSprintName overrides the auto-generated name.
func WithSprintName(name string) SprintOpt {
	return func(o *db.SprintCreateOptions) { o.Name = name }
}

// WithSprintStatus overrides the default "active" status.
func WithSprintStatus(status string) SprintOpt {
	return func(o *db.SprintCreateOptions) { o.Status = status }
}

// WithSprintDates sets both start and end dates (YYYY-MM-DD).
func WithSprintDates(start, end string) SprintOpt {
	return func(o *db.SprintCreateOptions) {
		o.StartDate = start
		o.EndDate = end
	}
}

// MakeSprint creates a Sprint row, optionally linked to the given
// Epic (pass nil to leave epic_ref unset). Cleanup deletes the row.
func MakeSprint(t *testing.T, pool *pgxpool.Pool, epic *db.Epic, opts ...SprintOpt) *db.Sprint {
	t.Helper()

	o := db.SprintCreateOptions{
		Name:      "test-sprint-" + randSuffix(),
		Status:    "active",
		StartDate: "2026-05-01",
		EndDate:   "2026-05-14",
	}

	if epic != nil {
		o.EpicID = epic.ID
	}

	for _, fn := range opts {
		fn(&o)
	}

	s, err := db.CreateSprint(context.Background(), pool, o)
	require.NoError(t, err, "CreateSprint")

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		_, _ = pool.Exec(ctx, "DELETE FROM createos_sprint WHERE id = $1", s.ID)
	})

	return s
}

// StoryOpt is the option type for MakeStory.
type StoryOpt func(*db.StoryCreateOptions)

// WithStoryName overrides the auto-generated name.
func WithStoryName(name string) StoryOpt {
	return func(o *db.StoryCreateOptions) { o.Name = name }
}

// WithStoryDescription sets the description.
func WithStoryDescription(desc string) StoryOpt {
	return func(o *db.StoryCreateOptions) { o.Description = desc }
}

// WithStoryPriority overrides the default "should_have" priority.
func WithStoryPriority(priority string) StoryOpt {
	return func(o *db.StoryCreateOptions) { o.Priority = priority }
}

// WithStorySprint links the story to a sprint.
func WithStorySprint(sprint *db.Sprint) StoryOpt {
	return func(o *db.StoryCreateOptions) { o.SprintID = sprint.ID }
}

// MakeStory creates a UserStory row linked to the given Epic (always
// required — UserStory.epic_ref is non-nullable in the schema).
// Cleanup deletes the row.
func MakeStory(t *testing.T, pool *pgxpool.Pool, epic *db.Epic, opts ...StoryOpt) *db.Story {
	t.Helper()
	require.NotNil(t, epic, "MakeStory: epic is required")

	o := db.StoryCreateOptions{
		Name:        "test-story-" + randSuffix(),
		Description: "fixture story for testing",
		Priority:    "should_have",
		EpicID:      epic.ID,
		UserType:    "maintainer",
	}

	for _, fn := range opts {
		fn(&o)
	}

	s, err := db.CreateStory(context.Background(), pool, o)
	require.NoError(t, err, "CreateStory")

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		_, _ = pool.Exec(ctx, "DELETE FROM createos_userstory WHERE id = $1", s.ID)
	})

	return s
}
