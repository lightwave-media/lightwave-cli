package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/sst"
	"github.com/spf13/cobra"
)

var (
	checkSchemaJSON   bool
	checkSchemaStrict bool
)

// checkSchemaCmd validates that the CLI handler registry exactly matches the
// SST schema (commands.yaml). This is the drift gate: any handler without a
// schema entry, or any schema entry without a handler, fails the command.
//
// Wired into pre-commit + lw check ci so the binary cannot drift from the
// declared surface without CI catching it.
var checkSchemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Validate CLI schema vs handler registry (drift gate)",
	Long: `Compare commands.yaml against the in-binary handler registry.

Reports two kinds of drift:
  - Schema entries with no registered handler (declared but unimplemented)
  - Handlers with no schema entry (implemented but undeclared)

During the Phase 4 dispatcher migration, --strict treats any drift as a
failure (exit 1). The default mode reports drift but exits 0 so partial
migrations stay buildable.`,
	RunE: runCheckSchema,
}

func init() {
	checkSchemaCmd.Flags().BoolVar(&checkSchemaJSON, "json", false, "emit machine-readable JSON")
	checkSchemaCmd.Flags().BoolVar(&checkSchemaStrict, "strict", false, "exit 1 on any drift")
	checkCmd.AddCommand(checkSchemaCmd)
}

type schemaDriftReport struct {
	SchemaVersion     string   `json:"schema_version"`
	DomainCount       int      `json:"domain_count"`
	CommandCount      int      `json:"command_count"`
	HandlerCount      int      `json:"handler_count"`
	MissingHandlers   []string `json:"missing_handlers"`
	OrphanedHandlers  []string `json:"orphaned_handlers"`
	HandlerMatchRatio float64  `json:"handler_match_ratio"`
}

func runCheckSchema(cmd *cobra.Command, _ []string) error {
	cfg := config.Get()
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}

	schema, err := sst.LoadCLIConfig(cfg.Paths.LightwaveRoot)
	if err != nil {
		return fmt.Errorf("load CLI schema: %w", err)
	}

	schemaKeys := schema.Keys()
	registryKeys := RegisteredKeys()

	schemaSet := make(map[string]bool, len(schemaKeys))
	for _, k := range schemaKeys {
		schemaSet[k] = true
	}
	registrySet := make(map[string]bool, len(registryKeys))
	for _, k := range registryKeys {
		registrySet[k] = true
	}

	var missing, orphaned []string
	for _, k := range schemaKeys {
		if !registrySet[k] {
			missing = append(missing, k)
		}
	}
	for _, k := range registryKeys {
		if !schemaSet[k] {
			orphaned = append(orphaned, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(orphaned)

	report := schemaDriftReport{
		SchemaVersion:    schema.Version,
		DomainCount:      len(schema.Domains),
		CommandCount:     len(schemaKeys),
		HandlerCount:     len(registryKeys),
		MissingHandlers:  missing,
		OrphanedHandlers: orphaned,
	}
	if len(schemaKeys) > 0 {
		matched := len(schemaKeys) - len(missing)
		report.HandlerMatchRatio = float64(matched) / float64(len(schemaKeys))
	}

	if checkSchemaJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printSchemaDriftHuman(report)
	}

	if checkSchemaStrict && (len(missing) > 0 || len(orphaned) > 0) {
		return fmt.Errorf("schema drift: %d missing handler(s), %d orphaned handler(s)",
			len(missing), len(orphaned))
	}
	return nil
}

func printSchemaDriftHuman(r schemaDriftReport) {
	fmt.Printf("%s %s\n", color.CyanString("CLI schema:"), r.SchemaVersion)
	fmt.Printf("  domains:  %d\n", r.DomainCount)
	fmt.Printf("  commands: %d\n", r.CommandCount)
	fmt.Printf("  handlers: %d (%.0f%% coverage)\n",
		r.HandlerCount, r.HandlerMatchRatio*100)

	if len(r.MissingHandlers) == 0 && len(r.OrphanedHandlers) == 0 {
		fmt.Println(color.GreenString("\n✓ no drift"))
		return
	}

	if len(r.MissingHandlers) > 0 {
		fmt.Printf("\n%s (%d)\n",
			color.YellowString("missing handlers (declared in schema, no Go handler)"),
			len(r.MissingHandlers))
		for _, k := range r.MissingHandlers {
			fmt.Printf("  - %s\n", k)
		}
	}
	if len(r.OrphanedHandlers) > 0 {
		fmt.Printf("\n%s (%d)\n",
			color.RedString("orphaned handlers (registered but not in schema)"),
			len(r.OrphanedHandlers))
		for _, k := range r.OrphanedHandlers {
			fmt.Printf("  - %s\n", k)
		}
	}
}
