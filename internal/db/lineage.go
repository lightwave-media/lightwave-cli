package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"gopkg.in/yaml.v3"
)

// LineageGap represents a missing or incomplete upstream document
type LineageGap struct {
	DocumentType  string // prd, sad, nfr, ddd, api_spec
	Status        string // missing, draft, stale
	Severity      string // required, recommended
	EntityType    string // epic, story
	EntityID      string
	EntityShortID string
	EntityName    string // human-readable name for display
	TaskCount     int    // for stories: number of tasks that triggered threshold
}

// DocumentTypeConfig holds SST metadata for a document category
type DocumentTypeConfig struct {
	Description    string   `yaml:"description"`
	Requires       []string `yaml:"requires"`
	Produces       []string `yaml:"produces"`
	LinksTo        *string  `yaml:"links_to"`
	RequiredFields []string `yaml:"required_fields"`
}

// LineageConfig holds validation rules loaded from SST YAML
type LineageConfig struct {
	EpicRequires    []string
	EpicRecommended []string
	TaskThreshold   int
	SprintBlockers  []string
	// Full document type definitions from document_lineage
	DocumentTypes map[string]DocumentTypeConfig
	// Ordered chain: product_vision → market_analysis → prd → sad/nfr → ddd → api_spec
	ChainOrder []string
}

// ValidCategories returns the set of valid document categories from SST
func (lc LineageConfig) ValidCategories() map[string]bool {
	cats := make(map[string]bool)
	for k := range lc.DocumentTypes {
		cats[k] = true
	}
	return cats
}

// sst YAML structs for unmarshaling
type createOSConfigYAML struct {
	DocumentLineage   map[string]DocumentTypeConfig `yaml:"document_lineage"`
	LineageValidation struct {
		EpicRequires       []string `yaml:"epic_requires"`
		EpicRecommended    []string `yaml:"epic_recommended"`
		StoryRequiresDddIf struct {
			TaskCountThreshold int `yaml:"task_count_threshold"`
		} `yaml:"story_requires_ddd_if"`
		SprintBlockers []string `yaml:"sprint_blockers"`
	} `yaml:"lineage_validation"`
}

// LoadLineageConfig reads lineage validation rules from SST YAML.
// Falls back to hardcoded defaults if the file can't be read.
func LoadLineageConfig() LineageConfig {
	cfg := config.Get()
	configPath := filepath.Join(
		cfg.Paths.LightwaveRoot,
		"packages", "lightwave-core", "lightwave", "schema",
		"definitions", "products", "createos", "config.yaml",
	)

	data, err := os.ReadFile(configPath)
	if err != nil {
		return defaultLineageConfig()
	}

	var parsed createOSConfigYAML
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return defaultLineageConfig()
	}

	lv := parsed.LineageValidation
	lc := LineageConfig{
		EpicRequires:    lv.EpicRequires,
		EpicRecommended: lv.EpicRecommended,
		TaskThreshold:   lv.StoryRequiresDddIf.TaskCountThreshold,
		SprintBlockers:  lv.SprintBlockers,
		DocumentTypes:   parsed.DocumentLineage,
	}

	// Build chain order via topological walk from roots
	if len(lc.DocumentTypes) > 0 {
		lc.ChainOrder = buildChainOrder(lc.DocumentTypes)
	}

	// Ensure sane defaults for anything missing
	if len(lc.EpicRequires) == 0 {
		lc.EpicRequires = []string{"prd"}
	}
	if lc.TaskThreshold == 0 {
		lc.TaskThreshold = 5
	}

	// Recommended = full list minus required (deduplicated)
	if len(lc.EpicRecommended) > 0 {
		reqSet := make(map[string]bool)
		for _, r := range lc.EpicRequires {
			reqSet[r] = true
		}
		var recOnly []string
		for _, r := range lc.EpicRecommended {
			if !reqSet[r] {
				recOnly = append(recOnly, r)
			}
		}
		lc.EpicRecommended = recOnly
	}

	return lc
}

func defaultLineageConfig() LineageConfig {
	return LineageConfig{
		EpicRequires:    []string{"prd"},
		EpicRecommended: []string{"sad", "nfr"},
		TaskThreshold:   5,
		SprintBlockers:  []string{"prd"},
		DocumentTypes: map[string]DocumentTypeConfig{
			"product_vision":  {Description: "Product vision", Produces: []string{"market_analysis", "prd"}},
			"market_analysis": {Description: "Market analysis", Requires: []string{"product_vision"}, Produces: []string{"prd"}},
			"prd":             {Description: "Product requirements", Requires: []string{"product_vision"}, Produces: []string{"sad", "nfr"}},
			"sad":             {Description: "System architecture", Requires: []string{"prd"}, Produces: []string{"ddd"}},
			"nfr":             {Description: "Non-functional requirements", Requires: []string{"prd"}},
			"ddd":             {Description: "Detailed design", Requires: []string{"sad"}, Produces: []string{"api_spec"}},
			"api_spec":        {Description: "API specification", Requires: []string{"ddd"}},
		},
		ChainOrder: []string{"product_vision", "market_analysis", "prd", "sad", "nfr", "ddd", "api_spec"},
	}
}

// buildChainOrder does a topological sort from roots (no requires) to leaves
func buildChainOrder(types map[string]DocumentTypeConfig) []string {
	// Find roots: types with no requires
	var roots []string
	for name, dt := range types {
		if len(dt.Requires) == 0 {
			roots = append(roots, name)
		}
	}

	visited := make(map[string]bool)
	var order []string

	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		// Visit requires first
		if dt, ok := types[name]; ok {
			for _, req := range dt.Requires {
				visit(req)
			}
		}
		order = append(order, name)
	}

	for _, root := range roots {
		visit(root)
	}
	// Catch any not reachable from roots
	for name := range types {
		visit(name)
	}
	return order
}

// CheckLineage checks for missing or incomplete upstream documents for an epic
func CheckLineage(ctx context.Context, pool *pgxpool.Pool, epicID string) ([]LineageGap, error) {
	lc := LoadLineageConfig()
	var gaps []LineageGap

	epicGaps, err := checkEpicDocuments(ctx, pool, epicID, lc)
	if err != nil {
		return nil, err
	}
	gaps = append(gaps, epicGaps...)

	storyGaps, err := checkStoryDocuments(ctx, pool, epicID, lc)
	if err != nil {
		return nil, err
	}
	gaps = append(gaps, storyGaps...)

	return gaps, nil
}

// docRow holds category + status from a document query
type docRow struct {
	category string
	status   string
}

func checkEpicDocuments(ctx context.Context, pool *pgxpool.Pool, epicID string, lc LineageConfig) ([]LineageGap, error) {
	var gaps []LineageGap
	shortID := epicID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	query := `
		SELECT category, status FROM createos_document
		WHERE epic_id = $1 AND is_deleted = false
	`
	rows, err := pool.Query(ctx, query, epicID)
	if err != nil {
		return nil, fmt.Errorf("failed to query documents: %w", err)
	}
	defer rows.Close()

	existingDocs := make(map[string]string) // category → status
	for rows.Next() {
		var d docRow
		if err := rows.Scan(&d.category, &d.status); err != nil {
			return nil, fmt.Errorf("failed to scan document: %w", err)
		}
		existingDocs[d.category] = d.status
	}

	// Check required docs
	for _, docType := range lc.EpicRequires {
		gap := checkDocPresence(docType, "required", "epic", epicID, shortID, existingDocs)
		if gap != nil {
			gaps = append(gaps, *gap)
		}
	}

	// Check recommended docs
	for _, docType := range lc.EpicRecommended {
		gap := checkDocPresence(docType, "recommended", "epic", epicID, shortID, existingDocs)
		if gap != nil {
			gaps = append(gaps, *gap)
		}
	}

	// Check chain ordering: doc exists but upstream prerequisite is missing
	gaps = append(gaps, checkChainOrder(lc, existingDocs, "epic", epicID, shortID)...)

	return gaps, nil
}

// checkDocPresence returns a LineageGap if the document is missing or still in draft
func checkDocPresence(docType, severity, entityType, entityID, entityShortID string, existingDocs map[string]string) *LineageGap {
	status, exists := existingDocs[docType]
	if !exists {
		return &LineageGap{
			DocumentType:  docType,
			Status:        "missing",
			Severity:      severity,
			EntityType:    entityType,
			EntityID:      entityID,
			EntityShortID: entityShortID,
		}
	}
	if status == "draft" {
		return &LineageGap{
			DocumentType:  docType,
			Status:        "draft",
			Severity:      severity,
			EntityType:    entityType,
			EntityID:      entityID,
			EntityShortID: entityShortID,
		}
	}
	return nil
}

// checkChainOrder detects docs that exist but whose upstream prerequisites are missing.
// E.g., SAD exists but PRD doesn't → out_of_order warning.
func checkChainOrder(lc LineageConfig, existingDocs map[string]string, entityType, entityID, entityShortID string) []LineageGap {
	var gaps []LineageGap
	for cat, dt := range lc.DocumentTypes {
		_, exists := existingDocs[cat]
		if !exists {
			continue // doc doesn't exist, nothing to check
		}
		for _, req := range dt.Requires {
			if _, reqExists := existingDocs[req]; !reqExists {
				gaps = append(gaps, LineageGap{
					DocumentType:  cat,
					Status:        "out_of_order",
					Severity:      "warning",
					EntityType:    entityType,
					EntityID:      entityID,
					EntityShortID: entityShortID,
					EntityName:    fmt.Sprintf("%s exists but requires %s", strings.ToUpper(cat), strings.ToUpper(req)),
				})
			}
		}
	}
	return gaps
}

func checkStoryDocuments(ctx context.Context, pool *pgxpool.Pool, epicID string, lc LineageConfig) ([]LineageGap, error) {
	var gaps []LineageGap

	query := fmt.Sprintf(`
		SELECT s.id, s.name,
			(SELECT COUNT(*) FROM createos_task t WHERE t.user_story_id = s.id) AS task_count
		FROM createos_userstory s
		WHERE s.epic_id = $1
		AND (SELECT COUNT(*) FROM createos_task t WHERE t.user_story_id = s.id) >= %d
	`, lc.TaskThreshold)

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

	for _, story := range complexStories {
		shortID := story.id
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}

		var dddStatus *string
		err := pool.QueryRow(ctx,
			"SELECT status FROM createos_document WHERE user_story_id = $1 AND category = 'ddd' AND is_deleted = false LIMIT 1",
			story.id,
		).Scan(&dddStatus)

		if err != nil {
			// No row = missing
			gaps = append(gaps, LineageGap{
				DocumentType:  "ddd",
				Status:        "missing",
				Severity:      "recommended",
				EntityType:    "story",
				EntityID:      story.id,
				EntityShortID: shortID,
				EntityName:    story.name,
				TaskCount:     story.taskCount,
			})
		} else if dddStatus != nil && *dddStatus == "draft" {
			gaps = append(gaps, LineageGap{
				DocumentType:  "ddd",
				Status:        "draft",
				Severity:      "recommended",
				EntityType:    "story",
				EntityID:      story.id,
				EntityShortID: shortID,
				EntityName:    story.name,
				TaskCount:     story.taskCount,
			})
		}
	}

	return gaps, nil
}

// SkeletonContent generates placeholder content sections from SST required_fields
func SkeletonContent(lc LineageConfig, category string) map[string]string {
	dt, ok := lc.DocumentTypes[category]
	if !ok || len(dt.RequiredFields) == 0 {
		return nil
	}
	content := make(map[string]string)
	for _, field := range dt.RequiredFields {
		content[field] = "TODO"
	}
	return content
}

// FixLineage auto-creates missing documents for an epic, returns what was created
func FixLineage(ctx context.Context, pool *pgxpool.Pool, epicID string) ([]Document, error) {
	lc := LoadLineageConfig()
	gaps, err := CheckLineage(ctx, pool, epicID)
	if err != nil {
		return nil, err
	}

	// Only fix "missing" gaps, not "draft" ones
	var created []Document
	for _, gap := range gaps {
		if gap.Status != "missing" {
			continue
		}

		opts := DocumentCreateOptions{
			Category: gap.DocumentType,
			Title:    fmt.Sprintf("%s — %s", gap.EntityShortID, strings.ToUpper(gap.DocumentType)),
			Content:  SkeletonContent(lc, gap.DocumentType),
		}
		if gap.EntityType == "epic" {
			opts.FullEpicID = gap.EntityID // Full UUID — skip GetEpic lookup
		} else if gap.EntityType == "story" {
			opts.UserStoryID = gap.EntityID
		}

		doc, err := CreateDocument(ctx, pool, opts)
		if err != nil {
			return created, fmt.Errorf("failed to create %s for %s %s: %w",
				gap.DocumentType, gap.EntityType, gap.EntityShortID, err)
		}
		created = append(created, *doc)
	}

	return created, nil
}
