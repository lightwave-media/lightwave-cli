package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/uicatalog"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

// `lw scaffold` — front door to the Gruntwork boilerplate engine over the
// canonical lightwave-core blueprint library. lw resolves a blueprint by name
// and renders it through the engine, which is linked in as a library (#355);
// lw does NOT template anything itself.
//
// Hardcoded in root.go and parked in legacyHardcodedDomains so the schema
// dispatcher won't double-register `scaffold`.
//
// The stamp has not "not landed yet" — it landed wrong. commands.yaml declares
// this domain as "Code generation (Django/Go skeleton templates)" with verbs
// app/model/api/test, which is Django-era vocabulary that no longer matches
// anything here. The Go handlers for those four verbs exist only to satisfy
// `lw check schema` (it counts registered handlers, not working ones) and
// every one of them returns "not yet wired", pointing at an internal/scaffold
// package that does not exist. They are unreachable anyway, because this
// domain is dispatcher-exempt.
//
// Deleting them requires fixing the stamp first, and fitting the real surface
// (`lw scaffold <blueprint>`, a positional on the domain) to the schema's
// domain→commands model means a UX change. Tracked separately; do not "fix"
// the stubs by making them do something.

var (
	scaffoldVars       []string
	scaffoldVarFiles   []string
	scaffoldOutput     string
	scaffoldBlueprints string
	scaffoldNoHooks    bool
	scaffoldForce      bool
	scaffoldList       bool
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
	Args: func(cmd *cobra.Command, args []string) error {
		if scaffoldList {
			return nil
		}
		return cobra.ExactArgs(1)(cmd, args)
	},
	SilenceUsage: true,
	RunE:         runScaffold,
}

func init() {
	scaffoldCmd.Flags().StringArrayVar(&scaffoldVars, "var", nil, "Set a blueprint variable NAME=VALUE (repeatable)")
	scaffoldCmd.Flags().StringArrayVar(&scaffoldVarFiles, "var-file", nil, "Load variables from a YAML file (repeatable)")
	scaffoldCmd.Flags().StringVarP(&scaffoldOutput, "output-folder", "o", "", "Output directory (required unless --list)")
	scaffoldCmd.Flags().StringVar(&scaffoldBlueprints, "blueprints-dir", "", "Override the blueprint library location")
	scaffoldCmd.Flags().BoolVar(&scaffoldNoHooks, "no-hooks", false, "Skip blueprint hooks")
	scaffoldCmd.Flags().BoolVar(&scaffoldForce, "force", false, "Overwrite existing files (default: refuse if the blueprint would clobber any)")
	scaffoldCmd.Flags().BoolVar(&scaffoldList, "list", false, "List all available blueprints and templates")
}

// listCatalog resolves what `--list` should enumerate.
//
// --blueprints-dir is documented as overriding the library location, but the
// list path ignored it and always read the config's lightwave root — so
// `lw scaffold --list --blueprints-dir X` listed one library while
// `lw scaffold <slug> --blueprints-dir X` rendered from another. The override
// points at the blueprints/ directory; the catalog lives one level up,
// alongside templates/.
func listCatalog(override string) ([]blueprint.CatalogEntry, error) {
	if override != "" {
		return blueprint.ListFrom(filepath.Dir(override))
	}

	cfg := config.Get()
	if cfg == nil {
		return nil, errors.New("config not loaded")
	}

	return blueprint.List(cfg.Paths.LightwaveRoot)
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
	if scaffoldList {
		return runScaffoldList()
	}

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
		Force:         scaffoldForce,
	})
}

func runScaffoldList() error {
	entries, err := listCatalog(scaffoldBlueprints)
	if err != nil {
		return err
	}

	fmt.Printf("%s\n\n", color.CyanString("Available blueprints and templates (lw scaffold <slug>)"))

	tw := tablewriter.NewWriter(os.Stdout)
	tw.SetHeader([]string{"Kind", "Slug", "Dir"})
	tw.SetBorder(false)
	tw.SetColumnSeparator("  ")
	tw.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	tw.SetAlignment(tablewriter.ALIGN_LEFT)

	for _, e := range entries {
		tw.Append([]string{e.Kind, e.Slug, e.Dir})
	}

	tw.Render()

	return nil
}

// --- `lw ui component <category>/<Name>` — sugar over scaffold react-component.

var (
	uiComponentOutput string
	uiComponentDryRun bool
	uiComponentForce  bool
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "UI scaffolding shortcuts",
}

var uiComponentCmd = &cobra.Command{
	Use:   "component <category>/<Name>",
	Short: "Scaffold a lightwave-ui React component (sugar over `lw scaffold react-component`)",
	Long: `Sugar over ` + "`lw scaffold react-component`" + `: maps <category>/<Name> to
--var category=<category> --var component_name=<Name>.

Default output is <lightwave_root>/lightwave-ui/src/components.

Example:
  lw ui component application/DataTable`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runUIComponent,
}

func init() {
	uiComponentCmd.Flags().StringVarP(&uiComponentOutput, "output-folder", "o", "", "Output directory (default: <lightwave_root>/lightwave-ui/src/components)")
	uiComponentCmd.Flags().BoolVar(&uiComponentDryRun, "dry-run", false, "Preview files that would be written; do not write")
	uiComponentCmd.Flags().BoolVar(&uiComponentForce, "force", false, "Overwrite existing files (default: refuse on collision)")
	uiCmd.AddCommand(uiComponentCmd)
}

func defaultUIComponentsDir(root string) string {
	return filepath.Join(root, "lightwave-ui", "src", "components")
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

	uiRepo, err := uiRepoPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(uiRepo); err != nil {
		return fmt.Errorf("lightwave-ui unreachable at %s: %w", uiRepo, err)
	}

	path, err := blueprint.Resolve(blueprint.BlueprintsDir(root), "react-component")
	if err != nil {
		return err
	}

	out := uiComponentOutput
	if out == "" {
		out = defaultUIComponentsDir(root)
	}

	if err := refuseAppLocalDuplicate(uiRepo, out, name); err != nil {
		return err
	}

	return blueprint.Render(cmd.Context(), &blueprint.RenderOptions{
		BlueprintPath: path,
		OutputFolder:  out,
		Vars:          []string{"category=" + category, "component_name=" + name},
		Force:         uiComponentForce,
		DryRun:        uiComponentDryRun,
	})
}

func refuseAppLocalDuplicate(uiRepo, out, name string) error {
	uiAbs, err := filepath.Abs(uiRepo)
	if err != nil {
		return err
	}

	outAbs, err := filepath.Abs(out)
	if err != nil {
		return err
	}

	sep := string(os.PathSeparator)

	inUI := outAbs == uiAbs || strings.HasPrefix(outAbs, uiAbs+sep)
	if inUI {
		return nil
	}

	entries, err := uicatalog.List(uiRepo)
	if err != nil {
		return fmt.Errorf("catalog unreachable at %s: %w", uiRepo, err)
	}

	if dup := uicatalog.Duplicate(entries, name); dup != nil {
		return fmt.Errorf(
			"refusing app-local duplicate of %s (%s); reuse that variant or register a new one in lightwave-ui",
			dup.Name, dup.Path,
		)
	}

	return nil
}
