package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LineageGap represents a missing upstream document in the lineage chain
type LineageGap struct {
	DocumentType  string // prd, sad, nfr, ddd, api_spec
	Status        string // missing, draft, stale
	Severity      string // required, recommended
	EntityType    string // epic, user_story
	EntityID      string
	EntityShortID string
}

// Required document types for an epic (from SST document_lineage.epic_requires)
var epicRequiredDocs = []string{"prd"}

// Recommended document types for an epic (from SST document_lineage.epic_recommended)
var epicRecommendedDocs = []string{"sad", "nfr"}

// CheckLineage checks for missing upstream documents for an epic
func CheckLineage(ctx context.Context, pool *pgxpool.Pool, epicID string) ([]LineageGap, error) {
	var gaps []LineageGap

	// Check epic-level documents (PRD, SAD, NFR)
	epicGaps, err := checkEpicDocuments(ctx, pool, epicID)
	if err != nil {
		return nil, err
	}
	gaps = append(gaps, epicGaps...)

	// Check story-level documents (DDD for complex stories)
	storyGaps, err := checkStoryDocuments(ctx, pool, epicID)
	if err != nil {
		return nil, err
	}
	gaps = append(gaps, storyGaps...)

	return gaps, nil
}

func checkEpicDocuments(ctx context.Context, pool *pgxpool.Pool, epicID string) ([]LineageGap, error) {
	var gaps []LineageGap
	shortID := epicID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	// Query existing documents linked to this epic
	query := `
		SELECT category FROM createos_document
		WHERE epic_id = $1 AND is_deleted = false
	`
	rows, err := pool.Query(ctx, query, epicID)
	if err != nil {
		return nil, fmt.Errorf("failed to query documents: %w", err)
	}
	defer rows.Close()

	existingDocs := make(map[string]bool)
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			return nil, fmt.Errorf("failed to scan document: %w", err)
		}
		existingDocs[category] = true
	}

	// Check required docs
	for _, docType := range epicRequiredDocs {
		if !existingDocs[docType] {
			gaps = append(gaps, LineageGap{
				DocumentType:  docType,
				Status:        "missing",
				Severity:      "required",
				EntityType:    "epic",
				EntityID:      epicID,
				EntityShortID: shortID,
			})
		}
	}

	// Check recommended docs
	for _, docType := range epicRecommendedDocs {
		if !existingDocs[docType] {
			gaps = append(gaps, LineageGap{
				DocumentType:  docType,
				Status:        "missing",
				Severity:      "recommended",
				EntityType:    "epic",
				EntityID:      epicID,
				EntityShortID: shortID,
			})
		}
	}

	return gaps, nil
}

func checkStoryDocuments(ctx context.Context, pool *pgxpool.Pool, epicID string) ([]LineageGap, error) {
	var gaps []LineageGap

	// Find stories with 5+ tasks (complexity threshold for DDD requirement)
	query := `
		SELECT s.id, s.name,
			(SELECT COUNT(*) FROM createos_task t WHERE t.user_story_id = s.id) AS task_count
		FROM createos_userstory s
		WHERE s.epic_id = $1
		AND (SELECT COUNT(*) FROM createos_task t WHERE t.user_story_id = s.id) >= 5
	`
	rows, err := pool.Query(ctx, query, epicID)
	if err != nil {
		return nil, fmt.Errorf("failed to query stories: %w", err)
	}
	defer rows.Close()

	type storyRow struct {
		id        string
		name      string
		taskCount int
	}
	var complexStories []storyRow
	for rows.Next() {
		var s storyRow
		if err := rows.Scan(&s.id, &s.name, &s.taskCount); err != nil {
			return nil, fmt.Errorf("failed to scan story: %w", err)
		}
		complexStories = append(complexStories, s)
	}

	// For each complex story, check for DDD document
	for _, story := range complexStories {
		shortID := story.id
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}

		var count int
		err := pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM createos_document WHERE user_story_id = $1 AND category = 'ddd' AND is_deleted = false",
			story.id,
		).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("failed to check DDD for story %s: %w", shortID, err)
		}

		if count == 0 {
			gaps = append(gaps, LineageGap{
				DocumentType:  "ddd",
				Status:        "missing",
				Severity:      "recommended",
				EntityType:    "story",
				EntityID:      story.id,
				EntityShortID: shortID,
			})
		}
	}

	return gaps, nil
}
