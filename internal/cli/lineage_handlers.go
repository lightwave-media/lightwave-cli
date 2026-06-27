package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/db"
)

// lineage_handlers wires `lw lineage check|fix <epic-id>` to the existing
// lineage backend (internal/db/lineage.go). check reports R-P-I-V-R gaps in an
// epic's artifact tree (read-only); fix creates the missing documents
// (destructive — honors --dry-run preview and --yes confirm-skip).

func init() {
	RegisterHandler("lineage.check", lineageCheckHandler)
	RegisterHandler("lineage.fix", lineageFixHandler)
}

func lineageCheckHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw lineage check <epic-id>")
	}

	epicID := args[0]

	pool, err := db.Connect(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	gaps, err := db.CheckLineage(ctx, pool, epicID)
	if err != nil {
		return fmt.Errorf("checking lineage: %w", err)
	}

	if flagBool(flags, "json") {
		out, marshalErr := json.MarshalIndent(gaps, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}

		fmt.Println(string(out))

		return nil
	}

	if len(gaps) == 0 {
		color.Green("✓ no lineage gaps for epic %s", epicID)
		return nil
	}

	for _, g := range gaps {
		fmt.Printf("  [%s] %s/%s — %s (%s)\n", g.Severity, g.EntityType, g.EntityShortID, g.DocumentType, g.Status)
	}

	fmt.Printf("\n%d lineage gap(s) for epic %s\n", len(gaps), epicID)

	return nil
}

func lineageFixHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw lineage fix <epic-id>")
	}

	epicID := args[0]

	pool, err := db.Connect(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	gaps, err := db.CheckLineage(ctx, pool, epicID)
	if err != nil {
		return fmt.Errorf("checking lineage: %w", err)
	}

	if len(gaps) == 0 {
		color.Green("✓ no lineage gaps for epic %s — nothing to fix", epicID)
		return nil
	}

	if flagBool(flags, "dry-run") {
		fmt.Printf("would create %d missing document(s) for epic %s:\n", len(gaps), epicID)

		for _, g := range gaps {
			fmt.Printf("  %s for %s/%s\n", g.DocumentType, g.EntityType, g.EntityShortID)
		}

		return nil
	}

	if !flagBool(flags, "yes") && !promptYesNo(fmt.Sprintf("Create %d missing document(s) for epic %s?", len(gaps), epicID)) {
		fmt.Println("aborted")
		return nil
	}

	created, err := db.FixLineage(ctx, pool, epicID)
	if err != nil {
		return fmt.Errorf("fixing lineage: %w", err)
	}

	for _, d := range created {
		color.Green("✓ created %s %s (%s)", d.Category, d.ShortID, d.Title)
	}

	fmt.Printf("\ncreated %d document(s) for epic %s\n", len(created), epicID)

	return nil
}
