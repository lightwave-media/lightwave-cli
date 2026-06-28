package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/codegen/gogen"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	codegenGoOut   string
	codegenGoOnly  string
	codegenGoCheck bool
)

// migrationFile is the single combined DDL output. One file (not per-table) so
// the platform's lexical db.Migrate applies tables in FK order (lightwave-cli#227).
const migrationFile = "schema.sql"

var codegenGoCmd = &cobra.Command{
	Use:   "go [family]",
	Short: "Generate Go structs + a multi-tenant SQL migration from SST entity schemas",
	Long: `Reads entity schemas from lightwave-core and emits:
  {table}.go   — Go struct (id, tenant_id, then schema fields; db/json tags)
  schema.sql   — ONE migration: all tables in FK-topological order, each with
                 tenant_id + an RLS tenant-isolation policy + FORCE (ADR-0010).
                 Depends on 001_init.sql (tenants table); apply after it.

family selects a schema sub-directory (default: data/agile_artifacts).

Examples:
  lw codegen go                          # generate all agile_artifacts entities
  lw codegen go --only epic              # generate only the epic struct
  lw codegen go --dry-run                # print to stdout, write nothing
  lw codegen go --out ./gen              # write elsewhere
  lw codegen go --check                  # exit 1 if generated output is stale`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCodegenGo,
}

func init() {
	codegenGoCmd.Flags().StringVar(&codegenGoOut, "out", "", "output directory (default: <lightwave_root>/lightwave-platform/backend/foundation/store/generated)")
	codegenGoCmd.Flags().StringVar(&codegenGoOnly, "only", "", "generate only the named entity (e.g. --only epic)")
	codegenGoCmd.Flags().BoolVar(&codegenGoCheck, "check", false, "exit 1 if output is stale (drift gate)")
	codegenCmd.AddCommand(codegenGoCmd)
}

func runCodegenGo(_ *cobra.Command, args []string) error {
	cfg := config.Get()

	root := cfg.Paths.LightwaveRoot
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "dev")
	}

	family := "data/agile_artifacts"
	if len(args) > 0 {
		family = args[0]
	}

	entityDir := filepath.Join(root, "lightwave-core", "src", "schemas", family)
	if _, err := os.Stat(entityDir); err != nil {
		return fmt.Errorf("schema directory not found: %s (is lightwave-core checked out at %s?)", entityDir, root)
	}

	outDir := codegenGoOut
	if outDir == "" {
		outDir = filepath.Join(root, "lightwave-platform", "backend", "foundation", "store", "generated")
	}

	entities, err := loadEntities(entityDir)
	if err != nil {
		return err
	}

	if len(entities) == 0 {
		color.Yellow("no entity schemas found in %s", entityDir)
		return nil
	}

	files, err := buildOutputs(entities)
	if err != nil {
		return err
	}

	if codegenGoCheck {
		return checkOutputs(outDir, files)
	}

	if codegenDryRun {
		for _, name := range sortedFileNames(files) {
			fmt.Printf("── %s ──\n%s\n", name, files[name])
		}

		return nil
	}

	if err := os.MkdirAll(outDir, codegenDirPerm); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	for _, name := range sortedFileNames(files) {
		dest := filepath.Join(outDir, name)
		if err := os.WriteFile(dest, []byte(files[name]), codegenFilePerm); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}

		color.Green("✓ %s", dest)
	}

	fmt.Printf("\nGenerated %s entities → %s\n", color.CyanString("%d", len(entities)), outDir)

	return nil
}

// loadEntities loads every entity in dir. The full set is always loaded so the
// migration's FK ordering is complete even under --only (which filters only
// which Go structs are emitted, in buildOutputs).
func loadEntities(dir string) ([]*gogen.EntitySchema, error) {
	paths, err := gogen.FindEntities(dir)
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", dir, err)
	}

	entities := make([]*gogen.EntitySchema, 0, len(paths))

	for _, p := range paths {
		e, loadErr := gogen.Load(p)
		if loadErr != nil {
			color.Yellow("skip %s: %v", filepath.Base(p), loadErr)
			continue
		}

		entities = append(entities, e)
	}

	return entities, nil
}

// buildOutputs renders the per-entity Go structs plus, unless --only is set,
// the single combined migration. Under --only only the matching struct is
// emitted and the migration is skipped — a one-entity migration would silently
// drop cross-table FKs (lightwave-cli#227).
func buildOutputs(entities []*gogen.EntitySchema) (map[string]string, error) {
	files := make(map[string]string, len(entities)+1)

	matched := 0

	for _, e := range entities {
		if codegenGoOnly != "" && entityShortName(e) != codegenGoOnly {
			continue
		}

		goSrc, err := gogen.GenerateGo(e, "store")
		if err != nil {
			return nil, err
		}

		files[e.Meta.TableName+".go"] = goSrc
		matched++
	}

	if codegenGoOnly != "" {
		if matched == 0 {
			return nil, fmt.Errorf("no entity named %q", codegenGoOnly)
		}

		color.Yellow("--only: emitting struct(s) only; %s needs the full entity set", migrationFile)

		return files, nil
	}

	migration, err := gogen.EmitMigration(entities)
	if err != nil {
		return nil, fmt.Errorf("emitting migration: %w", err)
	}

	files[migrationFile] = migration

	return files, nil
}

// entityShortName is the trailing segment of the schema_id (…/agile_artifacts/
// epic → "epic"), the token --only matches against.
func entityShortName(e *gogen.EntitySchema) string {
	parts := strings.Split(e.Meta.SchemaID, "/")

	return parts[len(parts)-1]
}

// checkOutputs compares every rendered file against disk, reporting drift.
func checkOutputs(outDir string, files map[string]string) error {
	stale := 0

	for _, name := range sortedFileNames(files) {
		got, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			color.Red("MISSING %s", name)

			stale++

			continue
		}

		if string(got) != files[name] {
			color.Red("STALE   %s", name)

			stale++
		}
	}

	if stale > 0 {
		return fmt.Errorf("%d generated file(s) are stale — run: lw codegen go", stale)
	}

	color.Green("✓ all generated files are up to date")

	return nil
}

func sortedFileNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}
