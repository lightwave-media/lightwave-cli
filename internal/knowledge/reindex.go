package knowledge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// Reindex restores the derived store using preserved files only. It never
// contacts providers, changes reconciliation bases, or clears pending intents.
func Reindex(ctx context.Context, files Files, projector Projector, dryRun bool) (Report, error) {
	report := Report{StartedAt: time.Now().UTC(), DryRun: dryRun, Changes: []Change{}}
	if !dryRun {
		unlock, err := files.Lock()
		if err != nil {
			return report, err
		}
		defer unlock()

		if projector == nil {
			return report, errors.New("reindex requires a projection store")
		}
	}

	databases, err := files.Databases()
	if err != nil {
		return report, err
	}

	for index := range databases {
		if !dryRun {
			if err := projector.ProjectDatabase(ctx, &databases[index]); err != nil {
				return report, err
			}
		}
	}

	pages, err := readPrints[Page](filepath.Join(files.Root, "specs", "notion_page"))
	if err != nil {
		return report, err
	}

	for index := range pages {
		page := &pages[index]

		binding, err := files.LoadBinding(page.NotionId)
		if err != nil {
			return report, fmt.Errorf("page %s recovery binding: %w", page.NotionId, err)
		}

		if binding.LocalId != page.ID.String() || binding.TenantID != page.TenantID || binding.ExternalId == nil || *binding.ExternalId != page.NotionId {
			return report, errors.New("reindex binding identity mismatch")
		}

		if !dryRun {
			if err := projector.Project(ctx, *page, binding); err != nil {
				return report, err
			}
		}

		report.Changes = append(report.Changes, Change{PageID: page.NotionId, Action: "reindex", Complete: page.ContentComplete != nil && *page.ContentComplete})
	}

	return report, nil
}
