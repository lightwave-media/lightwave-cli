package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/maintenance"
)

func init() {
	RegisterHandler("maintenance.scan-slop", maintenanceScanSlopHandler)
	RegisterHandler("maintenance.rotate-logs", maintenanceRotateLogsHandler)
	RegisterHandler("maintenance.prune-empty", maintenancePruneEmptyHandler)
}

// scanPrint loads the stamp and measures the print against it.
//
// An absent print is an error, not an empty scan. "0 signals, all clean" from
// a tree that does not exist is the defect class this whole surface exists to
// catch, and it is the one a hygiene verb must never produce itself.
func scanPrint() (string, maintenance.Shape, maintenance.Findings, error) {
	var (
		noShape    maintenance.Shape
		noFindings maintenance.Findings
	)

	cfg := config.Get()
	if cfg == nil {
		return "", noShape, noFindings, errors.New("config not loaded")
	}

	shape, err := maintenance.LoadShape(cfg.Paths.LightwaveRoot)
	if err != nil {
		return "", noShape, noFindings, err
	}

	root := config.PrintRoot()
	if root == "" {
		return "", noShape, noFindings, errors.New("cannot resolve the rendered print root")
	}

	if _, statErr := os.Stat(root); statErr != nil {
		return "", noShape, noFindings, fmt.Errorf("no rendered print at %s: %w", root, statErr)
	}

	return root, shape, maintenance.Scan(root, shape, time.Now()), nil
}

func maintenanceScanSlopHandler(_ context.Context, _ []string, flags map[string]any) error {
	root, shape, findings, err := scanPrint()
	if err != nil {
		return err
	}

	if flagBool(flags, "json") {
		return writeJSON(findings)
	}

	writeSlopReport(root, shape, &findings)

	return nil
}

func maintenanceRotateLogsHandler(_ context.Context, _ []string, flags map[string]any) error {
	root, _, findings, err := scanPrint()
	if err != nil {
		return err
	}

	rotations := maintenance.PlanRotations(findings.OversizeLogFiles, time.Now())

	if flagBool(flags, "json") {
		return writeJSON(map[string]any{
			"rotations": rotations,
			"applied":   false,
			"dry_run":   flagBool(flags, "dry-run"),
		})
	}

	if len(rotations) == 0 {
		fmt.Printf("no log over %s in %s/observability\n",
			humanBytes(maintenance.MaxLogBytes), root)

		return nil
	}

	for _, r := range rotations {
		fmt.Printf("  %s  %s → %s\n", humanBytes(r.Bytes), r.Source, r.Archive)
	}

	fmt.Println()

	if flagBool(flags, "dry-run") {
		fmt.Printf("dry run — %d log(s) would be archived and truncated in place\n", len(rotations))

		return nil
	}

	if !flagBool(flags, "yes") &&
		!promptYesNo(fmt.Sprintf("Archive and truncate %d log(s)?", len(rotations))) {
		fmt.Println("Cancelled — nothing was written.")

		return nil
	}

	var rotated int

	for _, r := range rotations {
		if err := maintenance.Rotate(root, r); err != nil {
			// Report and continue: one unwritable log must not strand the rest,
			// and the archive is written before the truncate, so a failure here
			// has cost nothing.
			fmt.Fprintf(os.Stderr, "  %s %v\n", color.RedString("FAIL"), err)
			continue
		}

		rotated++
	}

	fmt.Printf("rotated %d of %d log(s) into %s\n", rotated, len(rotations), maintenance.ArchiveDir)

	if rotated < len(rotations) {
		return fmt.Errorf("%d log(s) could not be rotated", len(rotations)-rotated)
	}

	return nil
}

func maintenancePruneEmptyHandler(_ context.Context, _ []string, flags map[string]any) error {
	root, shape, findings, err := scanPrint()
	if err != nil {
		return err
	}

	eligible, protected := maintenance.PlanPrune(findings.EmptyDirs, shape)

	if flagBool(flags, "json") {
		return writeJSON(map[string]any{
			"eligible":  eligible,
			"protected": protected,
			"applied":   false,
			"dry_run":   flagBool(flags, "dry-run"),
		})
	}

	for _, dir := range eligible {
		fmt.Printf("  %s %s/\n", color.YellowString("PRUNE"), dir)
	}

	if len(protected) > 0 {
		fmt.Printf("\n  %d empty dir(s) left alone — their zone is not wipe_on_reset:\n", len(protected))

		for _, dir := range protected {
			fmt.Printf("    %s/  (%s)\n", dir, zoneLabel(shape, dir))
		}
	}

	fmt.Println()

	if len(eligible) == 0 {
		fmt.Println("nothing to prune")

		return nil
	}

	if flagBool(flags, "dry-run") {
		fmt.Printf("dry run — %d empty dir(s) would be removed\n", len(eligible))

		return nil
	}

	if !flagBool(flags, "yes") &&
		!promptYesNo(fmt.Sprintf("Remove %d empty dir(s)?", len(eligible))) {
		fmt.Println("Cancelled — nothing was removed.")

		return nil
	}

	// Deepest first, so pruning a leaf leaves its parent prunable in the same
	// pass rather than failing on a directory that was empty a moment ago.
	for i := len(eligible) - 1; i >= 0; i-- {
		if err := maintenance.Prune(root, eligible[i]); err != nil {
			fmt.Fprintf(os.Stderr, "  %s %v\n", color.RedString("FAIL"), err)
		}
	}

	fmt.Printf("pruned %d empty dir(s)\n", len(eligible))

	return nil
}

func zoneLabel(shape maintenance.Shape, dir string) string {
	if zone := shape.Zone[maintenance.TopLevelOf(dir)]; zone != "" {
		return zone
	}

	return "unclassified"
}

func writeJSON(payload any) error {
	out, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(out))

	return nil
}

func writeSlopReport(root string, shape maintenance.Shape, f *maintenance.Findings) {
	fmt.Printf("slop scan — %s (zones v%s)\n\n", root, shape.ZonesVersion)

	section := func(title string, items []string) {
		fmt.Printf("  %s (%d)\n", title, len(items))

		for _, item := range items {
			fmt.Printf("    - %s\n", item)
		}
	}

	section("undeclared top-level entries", f.UndeclaredTopLevel)
	section("empty directories", f.EmptyDirs)

	fmt.Printf("  oversize logs >%s (%d)\n", humanBytes(maintenance.MaxLogBytes), len(f.OversizeLogFiles))

	for _, o := range f.OversizeLogFiles {
		fmt.Printf("    - %s  (%s)\n", o.Path, humanBytes(o.Bytes))
	}

	section("broken symlinks", f.BrokenSymlinks)
	section("empty observability channels", f.EmptyChannels)
	section(fmt.Sprintf("stale artefacts >%dd", maintenance.StaleArtefactDays), f.StaleArtefacts)

	// Stamp-side drift, reported apart from print-side drift because the fix is
	// a lightwave-core PR rather than anything on this machine.
	if len(f.UnclassifiedDirs) > 0 || len(f.PhantomZoneDirs) > 0 {
		fmt.Printf("\n  %s homedir.yaml and homedir_zones.yaml disagree:\n",
			color.YellowString("STAMP"))

		for _, dir := range f.UnclassifiedDirs {
			fmt.Printf("    - %s/ is declared but no zone classifies it\n", dir)
		}

		for _, dir := range f.PhantomZoneDirs {
			fmt.Printf("    - %s/ is classified but homedir.yaml does not declare it\n", dir)
		}
	}

	fmt.Printf("\ntotal slop signals: %d\n", f.Total())
}
