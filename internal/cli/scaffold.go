package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
)

// `lw scaffold` — front door to the Gruntwork boilerplate engine over the
// canonical lightwave-core blueprint library. lw resolves a blueprint by
// name and shells out to boilerplate (it does NOT template anything itself).
//
// Hardcoded in root.go and parked in legacyHardcodedDomains so the schema
// dispatcher won't double-register `scaffold` once a commands.yaml stamp
// lands.

var (
	scaffoldVars       []string
	scaffoldVarFiles   []string
	scaffoldOutput     string
	scaffoldBlueprints string
	scaffoldNoHooks    bool
)

var scaffoldCmd = &cobra.Command{
	Use:   "scaffold <blueprint>",
	Short: "Render a blueprint via the boilerplate engine",
	Long: `Resolve a blueprint by name from the canonical lightwave-core library and
render it with the Gruntwork boilerplate engine (non-interactive).

Blueprint library resolution:
  1. --blueprints-dir, else
  2. $LW_BLUEPRINTS_DIR, else
  3. <lightwave_root>/src/boilerplate/blueprints

All variables come from --var/--var-file (blueprint defaults fill the rest).

Examples:
  lw scaffold react-component -o ./out --var category=marketing --var component_name=Hero
  lw scaffold site-section -o ./src/components/marketing --var-file vars.yml`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runScaffold,
}

func init() {
	scaffoldCmd.Flags().StringArrayVar(&scaffoldVars, "var", nil, "Set a blueprint variable NAME=VALUE (repeatable)")
	scaffoldCmd.Flags().StringArrayVar(&scaffoldVarFiles, "var-file", nil, "Load variables from a YAML file (repeatable)")
	scaffoldCmd.Flags().StringVarP(&scaffoldOutput, "output-folder", "o", "", "Output directory (required)")
	scaffoldCmd.Flags().StringVar(&scaffoldBlueprints, "blueprints-dir", "", "Override the blueprint library location")
	scaffoldCmd.Flags().BoolVar(&scaffoldNoHooks, "no-hooks", false, "Skip blueprint hooks")
	_ = scaffoldCmd.MarkFlagRequired("output-folder")
}

// blueprintsDir resolves the library, honoring an explicit override first.
func blueprintsDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}

	cfg := config.Get()
	if cfg == nil {
		return "", errors.New("config not loaded")
	}

	return blueprint.BlueprintsDir(cfg.Paths.LightwaveRoot), nil
}

func runScaffold(cmd *cobra.Command, args []string) error {
	dir, err := blueprintsDir(scaffoldBlueprints)
	if err != nil {
		return err
	}

	path, err := blueprint.Resolve(dir, args[0])
	if err != nil {
		return err
	}

	return blueprint.Render(cmd.Context(), &blueprint.RenderOptions{
		BlueprintPath: path,
		OutputFolder:  scaffoldOutput,
		Vars:          scaffoldVars,
		VarFiles:      scaffoldVarFiles,
		NoHooks:       scaffoldNoHooks,
	})
}

// --- `lw ui component <category>/<Name>` — sugar over scaffold react-component.

var uiComponentOutput string

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "UI scaffolding shortcuts",
}

var uiComponentCmd = &cobra.Command{
	Use:   "component <category>/<Name>",
	Short: "Scaffold a lightwave-ui React component (sugar over `lw scaffold react-component`)",
	Long: `Sugar over ` + "`lw scaffold react-component`" + `: maps <category>/<Name> to
--var category=<category> --var component_name=<Name>.

Default output is <lightwave_root>/packages/lightwave-ui/src/components.

Example:
  lw ui component application/DataTable`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runUIComponent,
}

func init() {
	uiComponentCmd.Flags().StringVarP(&uiComponentOutput, "output-folder", "o", "", "Output directory (default: lightwave-ui components dir)")
	uiCmd.AddCommand(uiComponentCmd)
}

func runUIComponent(cmd *cobra.Command, args []string) error {
	const wantParts = 2

	parts := strings.SplitN(args[0], "/", wantParts)
	if len(parts) != wantParts || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("expected <category>/<Name>, got %q", args[0])
	}

	category, name := parts[0], parts[1]

	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	root := cfg.Paths.LightwaveRoot

	path, err := blueprint.Resolve(blueprint.BlueprintsDir(root), "react-component")
	if err != nil {
		return err
	}

	out := uiComponentOutput
	if out == "" {
		out = filepath.Join(root, "packages", "lightwave-ui", "src", "components")
	}

	return blueprint.Render(cmd.Context(), &blueprint.RenderOptions{
		BlueprintPath: path,
		OutputFolder:  out,
		Vars:          []string{"category=" + category, "component_name=" + name},
	})
}
