package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	driftSchema          string
	driftJSON            bool
	driftOutput          string
	driftOrphans         bool
	driftReconcile       bool
	driftReconcileAll    bool
	driftReconcileDryRun bool
)

var driftCmd = &cobra.Command{
	Use:   "drift",
	Short: "Schema drift detection and reconciliation",
	Long: `Compare YAML ideal state in lightwave-core against database reality.

Detects missing items, field drift, orphans, and errors. Can auto-reconcile
database to match YAML definitions.

Examples:
  lw drift report                        # Full drift report (all schemas)
  lw drift report --schema layouts       # Report for specific schema
  lw drift report --json                 # JSON output
  lw drift report --json -o drift.json   # Save to file
  lw drift reconcile                     # Sync DB to match YAML (specific schema)
  lw drift reconcile --all               # Sync all schemas`,
}

var driftReportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate drift report comparing YAML vs database",
	Long: `Check all schema definitions in lightwave-core against the database.

Reports items that are:
  MISSING - defined in YAML but not in database
  DRIFT   - exists in both but field values differ
  ORPHAN  - in database but not defined in YAML
  ERROR   - comparison failed

The report shows exactly what needs to change to bring the database
in sync with YAML ideal state.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}

		var parts []string
		parts = append(parts, "drift_report")

		if driftSchema != "" {
			parts = append(parts, fmt.Sprintf("--schema %s", driftSchema))
		}
		if driftJSON {
			parts = append(parts, "--json")
		}
		if driftOutput != "" {
			parts = append(parts, fmt.Sprintf("--output %s", driftOutput))
		}
		if driftOrphans {
			parts = append(parts, "--include-orphans")
		}

		return runMake(dir, "dj-manage", fmt.Sprintf("CMD=%s", strings.Join(parts, " ")))
	},
}

var driftReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Sync database to match YAML ideal state",
	Long: `Apply changes to make the database match YAML definitions.

Creates missing items, updates drifted fields. Does NOT delete orphans
(items in DB but not in YAML) unless explicitly configured.

Use 'lw drift report' first to preview changes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveMakeDir("platform")
		if err != nil {
			return err
		}

		var parts []string

		if driftReconcileAll {
			parts = append(parts, "reconcile")
		} else if driftSchema != "" {
			parts = append(parts, "reconcile", fmt.Sprintf("--schema %s", driftSchema))
		} else {
			return fmt.Errorf("specify --schema <name> or --all")
		}

		if driftReconcileDryRun {
			parts = append(parts, "--dry-run")
		}

		return runMake(dir, "dj-manage", fmt.Sprintf("CMD=%s", strings.Join(parts, " ")))
	},
}

func init() {
	// drift report flags
	driftReportCmd.Flags().StringVar(&driftSchema, "schema", "", "specific schema to report")
	driftReportCmd.Flags().BoolVar(&driftJSON, "json", false, "output as JSON")
	driftReportCmd.Flags().StringVarP(&driftOutput, "output", "o", "", "save report to file")
	driftReportCmd.Flags().BoolVar(&driftOrphans, "include-orphans", false, "include orphan items in report")

	// drift reconcile flags
	driftReconcileCmd.Flags().StringVar(&driftSchema, "schema", "", "specific schema to reconcile")
	driftReconcileCmd.Flags().BoolVar(&driftReconcileAll, "all", false, "reconcile all schemas")
	driftReconcileCmd.Flags().BoolVar(&driftReconcileDryRun, "dry-run", false, "preview changes without applying")

	driftCmd.AddCommand(driftReportCmd)
	driftCmd.AddCommand(driftReconcileCmd)
}
