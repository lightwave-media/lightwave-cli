package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/research"
	"github.com/spf13/cobra"
)

// `lw research` — Perplexity-backed research primitive.
//
// MVP: one synchronous "ask → cited answer" call (internal/research). The
// north-star is Search-as-Code (agents orchestrating low-level search
// primitives via generated code) — see docs/research-as-code.md.
//
// New top-level command not yet declared in lightwave-core's commands.yaml —
// wired hardcoded in root.go alongside agentCmd / memoryCmd / msgCmd. Schema
// entry lands in a follow-up (same transitional pattern).

var (
	researchModel   string
	researchDeep    bool
	researchJSON    bool
	researchOutput  string
	researchRecency string
	researchDomains []string
	researchSystem  string
)

var researchCmd = &cobra.Command{
	Use:   "research <query>",
	Short: "Perplexity-backed research (cited report)",
	Long: `Run a research query through Perplexity and print a cited answer.

The API key is read from AWS SSM (/lightwave/prod/PERPLEXITY_API_KEY,
SecureString); set PERPLEXITY_API_KEY to override for dev/CI.

Models: sonar-pro (default, fast) or sonar-deep-research via --deep
(slower, multi-step, richer citations).

Examples:
  lw research "what changed in the EU AI Act in 2026?"
  lw research --deep "survey agentic retrieval architectures" -o report.md
  lw research --recency week --domains arxiv.org "search-as-code prior art"
  lw research --json "latest Go 1.24 release notes" | jq .citations`,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	RunE:         runResearch,
}

func init() {
	researchCmd.Flags().StringVar(&researchModel, "model", "", "Perplexity model (default sonar-pro)")
	researchCmd.Flags().BoolVar(&researchDeep, "deep", false, "Use sonar-deep-research (slower, richer)")
	researchCmd.Flags().BoolVar(&researchJSON, "json", false, "Emit JSON {answer, citations, model, usage}")
	researchCmd.Flags().StringVarP(&researchOutput, "output", "o", "", "Write the report to a file")
	researchCmd.Flags().StringVar(&researchRecency, "recency", "", "Restrict sources by recency: hour|day|week|month")
	researchCmd.Flags().StringSliceVar(&researchDomains, "domains", nil, "Limit/exclude source domains (max 10; prefix - to exclude)")
	researchCmd.Flags().StringVar(&researchSystem, "system", "", "Optional system prompt to steer the research")
}

func runResearch(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	key, err := research.ResolveAPIKey(ctx)
	if err != nil {
		return err
	}

	model := researchModel
	if model == "" && researchDeep {
		model = research.ModelDeepResearch
	}

	res, err := research.NewClient(key).Research(ctx, research.Request{
		Query:   strings.Join(args, " "),
		Model:   model,
		System:  researchSystem,
		Recency: researchRecency,
		Domains: researchDomains,
	})
	if err != nil {
		return err
	}

	if researchJSON {
		return emitJSON(res)
	}

	out := renderResearch(res)
	if researchOutput != "" {
		if err := os.WriteFile(researchOutput, []byte(out), 0644); err != nil {
			return fmt.Errorf("write %s: %w", researchOutput, err)
		}
		fmt.Printf("%s wrote report to %s\n", color.GreenString("✓"), researchOutput)
		return nil
	}
	fmt.Print(out)
	return nil
}

// renderResearch formats the answer followed by a numbered Sources list.
func renderResearch(r *research.Result) string {
	var b strings.Builder
	b.WriteString(r.Answer)
	if !strings.HasSuffix(r.Answer, "\n") {
		b.WriteByte('\n')
	}
	if len(r.Citations) > 0 {
		b.WriteString("\nSources:\n")
		for i, src := range r.Citations {
			fmt.Fprintf(&b, "  [%d] %s\n", i+1, src)
		}
	}
	return b.String()
}
