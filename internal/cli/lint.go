package cli

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/docsfactory"
	"github.com/spf13/cobra"
)

// `lw lint` — schema-driven linters for LightWave template kinds. The naming
// matches the `validator:` field each kind declares in lightwave-core
// template_kinds.yaml (e.g. agent_handoff → "lw lint handoff"), so there is no
// parallel taxonomy. Wired as a hardcoded cobra tree exactly like `lw docs`
// (verified, but not yet in commands.yaml — see command_status.go).

var lintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Schema-driven linters for LightWave template kinds",
	Long: `lw lint validates rendered LightWave artifacts against their template_kind
contract in lightwave-core. Today: ` + "`handoff`" + `.`,
}

var lintHandoffCmd = &cobra.Command{
	Use:   "handoff <file>",
	Short: "Validate an agent_handoff (steps[] execution contract)",
	Long: `Validate a handoff (the agent_handoff data shape, YAML) against the per-step
contract in lightwave-core spec/sad/0001 § "The HandoffStep contract":

  - kind ∈ handoff_block_kinds (check|command|inputs|template|admonition)
  - success/failure REQUIRED for check/command/template, OMITTED for
    admonition, OPTIONAL for inputs
  - step_id unique; depends_on / consumes resolve to existing steps
  - depends_on forms a DAG (no cycles); no self-dependency
  - depends_on ⊇ consumes; request_ref resolves to a requests[] entry
  - status ∈ handoff_statuses

A negotiation-only handoff (no steps[]) is valid. The .handoff.md print is a
slice-2 follow-up; pass the YAML data shape today (the agent_handoff.yaml
schema file's example block works directly).

Exit codes:
  0  clean
  1  one or more violations
  2  tool error (schema load / parse failure)

Examples:
  lw lint handoff ./my.handoff.yaml
  lw lint handoff ~/dev/lightwave-core/src/schemas/data/reference_documents/agent_handoff.yaml`,
	Args: cobra.ExactArgs(1),
	// A validation failure is a result, not a misuse — don't dump usage.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := filepath.Abs(args[0])
		if err != nil {
			path = args[0]
		}
		schemas, err := loadDocsSchemas()
		if err != nil {
			return lintToolError(err)
		}
		doc, err := docsfactory.LoadHandoff(path)
		if err != nil {
			return lintToolError(err)
		}
		res := docsfactory.LintHandoff(doc, schemas)
		res.Path = path
		return reportHandoffLint(res)
	},
}

func init() {
	lintCmd.AddCommand(lintHandoffCmd)
	rootCmd.AddCommand(lintCmd)
}

// lintToolError tags a setup-stage failure (schema load / parse) so it reads as
// a tool error, distinct from a validation violation.
func lintToolError(err error) error {
	return fmt.Errorf("lint: tool error: %w", err)
}

func reportHandoffLint(res *docsfactory.HandoffLintResult) error {
	fmt.Printf("%s %d step(s)\n", color.CyanString("lint handoff:"), res.TotalSteps)
	if len(res.Violations) == 0 {
		fmt.Println(color.GreenString("✓ clean"))
		return nil
	}
	sort.Slice(res.Violations, func(i, j int) bool {
		if res.Violations[i].StepID != res.Violations[j].StepID {
			return res.Violations[i].StepID < res.Violations[j].StepID
		}
		return res.Violations[i].Rule < res.Violations[j].Rule
	})
	fmt.Printf("\n%s %d violation(s):\n", color.RedString("✗"), len(res.Violations))
	for _, v := range res.Violations {
		id := v.StepID
		if id == "" {
			id = "(handoff)"
		}
		fmt.Printf("  %s  [%s]  %s\n", id, v.Rule, v.Message)
	}
	return fmt.Errorf("%d handoff violation(s) in %s", len(res.Violations), filepath.Base(res.Path))
}
