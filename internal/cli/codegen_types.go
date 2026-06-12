package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/codegen/zodgen"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
)

var codegenTypesOut string

const (
	codegenDirPerm  = 0o755
	codegenFilePerm = 0o644
)

var codegenTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "Generate Zod TypeScript from the data/ui schema family",
	Long: `Reads lightwave-core's src/schemas/data/ui contracts and data/enums/ui_*
stamps and emits Zod TypeScript per their generates: declarations (ADR-0006).

Emits:
  enums.generated.ts      one z.enum per ui_* enum stamp
  sections.generated.ts   per-section Zod props + a sectionSchemas registry
                          keyed "<family>/<variant>" — drop-in replacement
                          for hand-written registries like joelschaeffer-site
                          src/data/sections.ts

Section instances are currently sourced from section_contract.yaml's example
block (the stamped round-trip fixture); instance files under data/ui/sections/
join in Run 3+.

Every invocation asserts PropField parity between component_contract and
section_contract and fails generation on drift (lightwave-cli#77).

Examples:
  lw codegen types                 # write into <root>/lightwave-ui/src/contracts
  lw codegen types --out ./gen     # write elsewhere
  lw codegen types --dry-run       # print to stdout, write nothing`,
	Args: cobra.NoArgs,
	RunE: runCodegenTypes,
}

func init() {
	codegenTypesCmd.Flags().StringVar(&codegenTypesOut, "out", "", "output directory (default <lightwave_root>/lightwave-ui/src/contracts)")
	codegenCmd.AddCommand(codegenTypesCmd)
}

func runCodegenTypes(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	root := cfg.Paths.LightwaveRoot
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "dev")
	}

	uiDir := filepath.Join(root, "lightwave-core", "src", "schemas", "data", "ui")
	enumsDir := filepath.Join(root, "lightwave-core", "src", "schemas", "data", "enums")

	outDir := codegenTypesOut
	if outDir == "" {
		outDir = filepath.Join(root, "lightwave-ui", "src", "contracts")
	}

	files, err := generateTypes(uiDir, enumsDir)
	if err != nil {
		return err
	}

	if codegenDryRun {
		for _, name := range []string{"enums.generated.ts", "contracts.generated.ts", "sections.generated.ts"} {
			fmt.Printf("── %s ──\n%s\n", name, files[name])
		}

		color.Yellow("dry-run: wrote nothing")

		return nil
	}

	if err := os.MkdirAll(outDir, codegenDirPerm); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	for name, content := range files {
		dest := filepath.Join(outDir, name)
		if _, err := os.Stat(dest); err == nil && !codegenForce {
			return fmt.Errorf("%s exists; re-run with --force to overwrite", dest)
		}

		if err := os.WriteFile(dest, []byte(content), codegenFilePerm); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}

		color.Green("✓ %s", dest)
	}

	return nil
}

// generateTypes is the pure core: load, resolve, assert parity, emit.
// Separated from the cobra wrapper so tests drive it against fixture dirs.
func generateTypes(uiDir, enumsDir string) (map[string]string, error) {
	enums, err := zodgen.LoadEnums(enumsDir)
	if err != nil {
		return nil, err
	}

	component, err := zodgen.LoadSchema(filepath.Join(uiDir, "component_contract.yaml"))
	if err != nil {
		return nil, err
	}

	section, err := zodgen.LoadSchema(filepath.Join(uiDir, "section_contract.yaml"))
	if err != nil {
		return nil, err
	}

	if err := zodgen.CheckPropFieldParity(component, section); err != nil {
		return nil, err
	}

	if err := zodgen.ResolveValuesRefs(section.RequiredFields, enums); err != nil {
		return nil, fmt.Errorf("section_contract: %w", err)
	}

	fixture, err := zodgen.SectionInstanceFromExample(section)
	if err != nil {
		return nil, err
	}

	sectionsTS, err := zodgen.EmitSections([]*zodgen.SectionInstance{fixture})
	if err != nil {
		return nil, err
	}

	// Contract shapes: every data/ui schema with a typescript target, in a
	// stable order matching the family's dependency chain.
	contracts := []*zodgen.Schema{component, section}

	for _, name := range []string{"page_definition.yaml", "site_config.yaml", "app_shell.yaml"} {
		s, err := zodgen.LoadSchema(filepath.Join(uiDir, name))
		if err != nil {
			return nil, err
		}

		contracts = append(contracts, s)
	}

	contractsTS, err := zodgen.EmitContracts(contracts, enums)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"enums.generated.ts":     zodgen.EmitEnums(enums),
		"contracts.generated.ts": contractsTS,
		"sections.generated.ts":  sectionsTS,
	}, nil
}
