package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Document represents a createos_document row
type Document struct {
	ID          string
	SiteID      string
	Category    string
	Status      string
	Title       string
	EpicID      *string
	UserStoryID *string
	CreatedAt   time.Time
	ShortID     string
}

// DocumentCreateOptions holds fields for creating a document
type DocumentCreateOptions struct {
	Category    string
	Title       string
	EpicID      string // optional — resolved via GetEpic (supports short ID prefix)
	UserStoryID string // optional — used directly as full UUID
	FullEpicID  string // optional — bypass GetEpic lookup when full UUID is known
}

// GetDefaultSiteID returns the first site ID in the current tenant schema.
// If no site exists, auto-creates a default one for the current tenant.
func GetDefaultSiteID(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var siteID string
	err := pool.QueryRow(ctx, "SELECT id::text FROM platform_site LIMIT 1").Scan(&siteID)
	if err == nil {
		return siteID, nil
	}

	// No site found — auto-create a default one
	newID := uuid.New().String()
	now := time.Now()
	_, err = pool.Exec(ctx, `
		INSERT INTO platform_site (id, domain, name, status, site_type, visibility, allow_search_indexing, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', 'platform', 'private', false, $4, $4)
	`, newID, "cli.lightwave.local", "CLI Default Site", now)
	if err != nil {
		return "", fmt.Errorf("no site found and failed to create default: %w", err)
	}
	return newID, nil
}

// CreateDocument inserts a new document into createos_document
func CreateDocument(ctx context.Context, pool *pgxpool.Pool, opts DocumentCreateOptions) (*Document, error) {
	siteID, err := GetDefaultSiteID(ctx, pool)
	if err != nil {
		return nil, err
	}

	id := uuid.New().String()
	now := time.Now()

	metadata, _ := json.Marshal(map[string]string{"title": opts.Title})

	query := `
		INSERT INTO createos_document
			(id, site_id, category, status, version_number, content_hash, content, metadata,
			 epic_id, user_story_id, is_deleted, created_at, updated_at)
		VALUES ($1, $2, $3, 'draft', 1, '', '{}'::jsonb, $4::jsonb,
			$5, $6, false, $7, $7)
		RETURNING id::text, category, status
	`

	var epicID, storyID *string
	if opts.FullEpicID != "" {
		// Direct UUID — skip GetEpic lookup
		epicID = &opts.FullEpicID
	} else if opts.EpicID != "" {
		// Resolve full epic ID from short prefix
		epic, err := GetEpic(ctx, pool, opts.EpicID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve epic: %w", err)
		}
		epicID = &epic.ID
	}
	if opts.UserStoryID != "" {
		storyID = &opts.UserStoryID
	}

	var doc Document
	err = pool.QueryRow(ctx, query,
		id, siteID, opts.Category, string(metadata),
		epicID, storyID,
		now,
	).Scan(&doc.ID, &doc.Category, &doc.Status)
	if err != nil {
		return nil, fmt.Errorf("failed to create document: %w", err)
	}

	doc.SiteID = siteID
	doc.Title = opts.Title
	doc.EpicID = epicID
	doc.UserStoryID = storyID
	doc.CreatedAt = now
	if len(doc.ID) >= 8 {
		doc.ShortID = doc.ID[:8]
	}
	return &doc, nil
}

// GetDocument finds a document by short ID prefix
func GetDocument(ctx context.Context, pool *pgxpool.Pool, shortID string) (*Document, error) {
	query := `
		SELECT d.id::text, d.category, d.status, d.metadata->>'title',
			d.epic_id::text, d.user_story_id::text, d.created_at
		FROM createos_document d
		WHERE d.id::text LIKE $1 || '%' AND d.is_deleted = false
		LIMIT 2
	`
	rows, err := pool.Query(ctx, query, shortID)
	if err != nil {
		return nil, fmt.Errorf("failed to query document: %w", err)
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		var title, eid, sid *string
		if err := rows.Scan(&d.ID, &d.Category, &d.Status, &title, &eid, &sid, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan document: %w", err)
		}
		if title != nil {
			d.Title = *title
		}
		d.EpicID = eid
		d.UserStoryID = sid
		if len(d.ID) >= 8 {
			d.ShortID = d.ID[:8]
		}
		docs = append(docs, d)
	}

	if len(docs) == 0 {
		return nil, fmt.Errorf("no document found matching '%s'", shortID)
	}
	if len(docs) > 1 {
		return nil, fmt.Errorf("ambiguous ID '%s' matches %d documents — use more characters", shortID, len(docs))
	}
	return &docs[0], nil
}

// DocumentUpdateOptions holds fields for updating a document
type DocumentUpdateOptions struct {
	Status *string
	Title  *string
}

// UpdateDocument updates specified fields of a document
func UpdateDocument(ctx context.Context, pool *pgxpool.Pool, shortID string, opts DocumentUpdateOptions) (*Document, error) {
	doc, err := GetDocument(ctx, pool, shortID)
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
	if opts.Title != nil {
		setClauses = append(setClauses, fmt.Sprintf("metadata = jsonb_set(metadata, '{title}', to_jsonb($%d::text))", argNum))
		args = append(args, *opts.Title)
		argNum++
	}

	if len(setClauses) == 0 {
		return nil, fmt.Errorf("no fields to update")
	}

	setClauses = append(setClauses, fmt.Sprintf("updated_at = $%d", argNum))
	args = append(args, time.Now())
	argNum++

	args = append(args, doc.ID)
	query := fmt.Sprintf("UPDATE createos_document SET %s WHERE id = $%d",
		strings.Join(setClauses, ", "), argNum)

	_, err = pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update document: %w", err)
	}

	// Apply updates to returned doc
	if opts.Status != nil {
		doc.Status = *opts.Status
	}
	if opts.Title != nil {
		doc.Title = *opts.Title
	}
	return doc, nil
}

// ListDocuments lists documents, optionally filtered by category and/or epic
func ListDocuments(ctx context.Context, pool *pgxpool.Pool, category, epicID string) ([]Document, error) {
	query := `
		SELECT d.id::text, d.category, d.status, d.metadata->>'title',
			d.epic_id::text, d.user_story_id::text, d.created_at
		FROM createos_document d
		WHERE d.is_deleted = false
	`
	var args []interface{}
	argNum := 1

	if category != "" {
		query += fmt.Sprintf(" AND d.category = $%d", argNum)
		args = append(args, category)
		argNum++
	}
	if epicID != "" {
		query += fmt.Sprintf(" AND d.epic_id::text LIKE $%d || '%%'", argNum)
		args = append(args, epicID)
		argNum++
	}

	query += " ORDER BY d.created_at DESC LIMIT 50"

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query documents: %w", err)
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		var title, eid, sid *string
		err := rows.Scan(&d.ID, &d.Category, &d.Status, &title, &eid, &sid, &d.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan document: %w", err)
		}
		if title != nil {
			d.Title = *title
		}
		d.EpicID = eid
		d.UserStoryID = sid
		if len(d.ID) >= 8 {
			d.ShortID = d.ID[:8]
		}
		docs = append(docs, d)
	}
	return docs, nil
}
