package cli

import (
	"fmt"
	"os"
	"path/filepath"
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

var codegenGoCmd = &cobra.Command{
	Use:   "go [family]",
	Short: "Generate Go structs + SQL DDL from SST entity schemas",
	Long: `Reads entity schemas from lightwave-core and emits:
  {table}.go   — Go struct with db/json tags, pointer optionals
  {table}.sql  — PostgreSQL DDL: CREATE TABLE, indexes, RLS enable

family selects a schema sub-directory (default: data/agile_artifacts).

Examples:
  lw codegen go                          # generate all agile_artifacts entities
  lw codegen go --only epic              # generate only the epic entity
  lw codegen go --dry-run                # print to stdout, write nothing
  lw codegen go --out ./gen              # write to ./gen/ instead of default
  lw codegen go --check                  # exit 1 if generated output is stale`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCodegenGo,
}

func init() {
	codegenGoCmd.Flags().StringVar(&codegenGoOut, "out", "", "output directory (default: <lightwave_root>/lightwave-platform/internal/store/generated)")
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
		outDir = filepath.Join(root, "lightwave-platform", "internal", "store", "generated")
	}

	paths, err := gogen.FindEntities(entityDir)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", entityDir, err)
	}

	if len(paths) == 0 {
		color.Yellow("no entity schemas found in %s", entityDir)
		return nil
	}

	if codegenGoOnly != "" {
		var filtered []string

		for _, p := range paths {
			base := strings.TrimSuffix(filepath.Base(p), ".yaml")

			if base == codegenGoOnly {
				filtered = append(filtered, p)
			}
		}

		if len(filtered) == 0 {
			return fmt.Errorf("no entity named %q found in %s", codegenGoOnly, entityDir)
		}

		paths = filtered
	}

	stale := 0
	generated := 0

	for _, p := range paths {
		e, err := gogen.Load(p)
		if err != nil {
			color.Yellow("skip %s: %v", filepath.Base(p), err)
			continue
		}

		out := gogen.Generate(e, "store")
		goFile := filepath.Join(outDir, e.Meta.TableName+".go")
		sqlFile := filepath.Join(outDir, e.Meta.TableName+".sql")

		if codegenGoCheck {
			stale += checkFile(goFile, out.GoFile)
			stale += checkFile(sqlFile, out.SQLFile)

			continue
		}

		if codegenDryRun {
			fmt.Printf("── %s.go ──\n%s\n", e.Meta.TableName, out.GoFile)
			fmt.Printf("── %s.sql ──\n%s\n", e.Meta.TableName, out.SQLFile)

			generated++

			continue
		}

		if err := os.MkdirAll(outDir, codegenDirPerm); err != nil {
			return fmt.Errorf("creating output dir: %w", err)
		}

		if err := os.WriteFile(goFile, []byte(out.GoFile), codegenFilePerm); err != nil {
			return fmt.Errorf("writing %s: %w", goFile, err)
		}

		if err := os.WriteFile(sqlFile, []byte(out.SQLFile), codegenFilePerm); err != nil {
			return fmt.Errorf("writing %s: %w", sqlFile, err)
		}

		color.Green("✓ %s", goFile)
		color.Green("✓ %s", sqlFile)

		generated++
	}

	if codegenGoCheck {
		if stale > 0 {
			return fmt.Errorf("%d generated file(s) are stale — run: lw codegen go", stale)
		}

		color.Green("✓ all generated files are up to date")

		return nil
	}

	if !codegenDryRun {
		fmt.Printf("\nGenerated %s entities → %s\n", color.CyanString("%d", generated), outDir)
	}

	return nil
}

// checkFile compares an on-disk file against expected content.
// Returns 1 if stale or missing, 0 if current.
func checkFile(path, want string) int {
	got, err := os.ReadFile(path)

	if err != nil || string(got) != want {
		if err != nil {
			color.Red("MISSING %s", path)
		} else {
			color.Red("STALE   %s", path)
		}

		return 1
	}

	return 0
}
