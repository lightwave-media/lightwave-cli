package cli

// runbook_catalog.go — the discovery half of `lw runbook`.
//
// Every other verb in this domain (start, status, apply, step-complete, cancel)
// takes a slug the caller must already know. There was no way to ask what is
// published, so 56 registered runbooks across 9 categories were reachable only
// by reading lightwave-core's src/runbooks/__index.yaml by hand — the library
// was there and invisible (#377, #417).
//
// Probing for the gap needed care, and the issue records why: cobra prints the
// PARENT help with exit 0 for an unknown subcommand, so `lw runbook list --help`
// looked identical to `lw runbook apply --help` succeeding. Only a nonsense
// control (`lw runbook zzzznope --help`, same output) showed that the probe was
// measuring nothing.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/runbook"
	"github.com/spf13/cobra"
)

var (
	runbookCategory    string
	runbookUnreachable bool
	runbookJSON        bool
)

var runbookListCmd = &cobra.Command{
	//nolint:goconst // cobra Use fields are literals everywhere in this package; a const for one of 13 sites would read worse
	Use:   "list",
	Short: "List published runbooks by category",
	Long: `List the runbooks published in lightwave-core/src/runbooks.

Shows what can actually RUN, not what the registry claims. The two differ:
the index lists entries whose runbook.mdx was never written, and ` + "`start`" + ` on
one of those fails on a missing edition — so the caller ends up debugging
their checkout for a runbook that does not exist.

  lw runbook list                     every reachable runbook, by category
  lw runbook list --category deploy   one category
  lw runbook list --unreachable       ONLY the broken registry entries
  lw runbook list --json              machine-readable, includes both`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runRunbookList,
}

var runbookSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Find a runbook by slug, category or description",
	Long: `Case-insensitive substring search over slug, category, description
and status.

  lw runbook search fargate
  lw runbook search "roll back"
  lw runbook search rds --json`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runRunbookSearch,
}

func init() {
	runbookListCmd.Flags().StringVar(&runbookCategory, "category", "", "only this category")
	runbookListCmd.Flags().BoolVar(&runbookUnreachable, "unreachable", false,
		"show only registry entries with no runbook.mdx")
	runbookListCmd.Flags().BoolVar(&runbookJSON, "json", false, "emit JSON")
	runbookSearchCmd.Flags().BoolVar(&runbookJSON, "json", false, "emit JSON")

	runbookCmd.AddCommand(runbookListCmd, runbookSearchCmd)

	RegisterHandler("runbook.list", runbookListHandler)
	RegisterHandler("runbook.search", runbookSearchHandler)
	RegisterHandler("runbook.show", runbookShowHandler)
}

func runbookListHandler(ctx context.Context, _ []string, flags map[string]any) error {
	runbookCategory = flagStr(flags, "category")
	runbookUnreachable = flagBool(flags, "unreachable")
	runbookJSON = flagBool(flags, "json")

	runbookListCmd.SetContext(ctx)

	return runRunbookList(runbookListCmd, nil)
}

func runbookSearchHandler(ctx context.Context, args []string, flags map[string]any) error {
	runbookJSON = flagBool(flags, "json")

	runbookSearchCmd.SetContext(ctx)

	if len(args) == 0 {
		return errors.New("usage: lw runbook search <query>")
	}

	return runRunbookSearch(runbookSearchCmd, args)
}

// runbookShowHandler prints one runbook's inputs and steps (#545), so an agent
// can run it without reading its MDX.
func runbookShowHandler(_ context.Context, args []string, flags map[string]any) error {
	if len(args) == 0 {
		return errors.New("usage: lw runbook show <slug>")
	}

	desc, err := runbook.Describe(coreRepoPath(), args[0])
	if err != nil {
		return err
	}

	if flagBool(flags, "json") {
		return emitJSON(desc)
	}

	return printRunbookDescription(os.Stdout, desc)
}

func runRunbookList(cmd *cobra.Command, _ []string) error {
	records, err := runbook.LoadCatalog(coreRepoPath())
	if err != nil {
		return err
	}

	wanted := make([]runbook.Record, 0, len(records))

	for _, r := range records {
		if runbookCategory != "" && r.Category != runbookCategory {
			continue
		}

		// Default view is what can run. --unreachable inverts it into a
		// registry-health view rather than adding the broken entries to the
		// normal listing, where they would read as available.
		if r.Reachable == runbookUnreachable {
			continue
		}

		wanted = append(wanted, r)
	}

	if runbookJSON {
		return emitJSON(wanted)
	}

	return printRunbookRecords(cmd.OutOrStdout(), wanted, records)
}

func runRunbookSearch(cmd *cobra.Command, args []string) error {
	records, err := runbook.LoadCatalog(coreRepoPath())
	if err != nil {
		return err
	}

	hits := runbook.Search(records, args[0])

	if runbookJSON {
		return emitJSON(hits)
	}

	if len(hits) == 0 {
		// The domain's stated dead end: no published match means file a
		// tool-gap, not improvise a procedure.
		_, err = fmt.Fprintf(cmd.OutOrStdout(),
			"no published runbook matches %q (searched %d entries).\n"+
				"File a tool-gap rather than improvising the procedure.\n",
			args[0], len(records))

		return err
	}

	return printRunbookRecords(cmd.OutOrStdout(), hits, records)
}

// printRunbookRecords renders grouped, aligned output. all is passed so the
// footer can say how much of the catalog the caller is looking at — a filtered
// list that does not say it is filtered is how people conclude a runbook does
// not exist.
//
// The whole listing is rendered into a builder and written once. Writing each
// line straight to w means an unchecked error per line, and the alternative —
// `_, _ =` on a dozen Fprintf calls — discards the one failure that matters
// (a closed pipe) a dozen times over.
func printRunbookRecords(w io.Writer, shown, all []runbook.Record) error {
	var out strings.Builder

	if len(shown) == 0 {
		out.WriteString("no runbooks match.\n")

		_, err := io.WriteString(w, out.String())

		return err
	}

	widest := 0

	for _, r := range shown {
		if len(r.Slug) > widest {
			widest = len(r.Slug)
		}
	}

	category := ""

	for _, r := range shown {
		if r.Category != category {
			category = r.Category

			fmt.Fprintf(&out, "\n%s\n", strings.ToUpper(category))
		}

		mark := " "
		if !r.Reachable {
			mark = "!"
		}

		fmt.Fprintf(&out, "  %s %-*s  %s\n", mark, widest, r.Slug, firstLine(r.Description))
	}

	reachable := 0

	for _, r := range all {
		if r.Reachable {
			reachable++
		}
	}

	fmt.Fprintf(&out, "\n%d shown · %d runnable of %d registered\n", len(shown), reachable, len(all))

	if broken := len(all) - reachable; broken > 0 && !runbookUnreachable {
		fmt.Fprintf(&out,
			"%d registry %s no runbook.mdx and cannot be started — `lw runbook list --unreachable`\n",
			broken, pluralHas(broken))
	}

	_, err := io.WriteString(w, out.String())

	return err
}

func pluralHas(n int) string {
	if n == 1 {
		return "entry has"
	}

	return "entries have"
}

// firstLine keeps the listing to one row per runbook; descriptions in front
// matter are sometimes wrapped paragraphs.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}

	const maxWidth = 88
	if len(s) > maxWidth {
		s = s[:maxWidth-1] + "…"
	}

	return strings.TrimSpace(s)
}

// printRunbookDescription renders what a caller needs to run a runbook: its
// inputs (which are required, which have defaults), its steps (marking the
// ones that pause for sign-off), and the command that runs it.
func printRunbookDescription(w io.Writer, d *runbook.Description) error {
	var out strings.Builder

	where := "changes files, so runs in a task worktree"
	if d.CheckOnly {
		where = "check-only, runs anywhere"
	}

	fmt.Fprintf(&out, "%s  (%s)  %s · %s\n", d.Slug, d.Dir, cmp.Or(d.Status, "no status"), where)

	if d.Description != "" {
		fmt.Fprintf(&out, "%s\n", firstLine(d.Description))
	}

	var required []string

	if len(d.Inputs) > 0 {
		out.WriteString("\nInputs (--var Name=Value)\n")

		width := 0
		for i := range d.Inputs {
			width = max(width, len(d.Inputs[i].Name))
		}

		for i := range d.Inputs {
			in := &d.Inputs[i]

			note := ""
			if in.Required() {
				note = "required"

				required = append(required, in.Name)
			} else if in.Default != nil {
				note = fmt.Sprintf("default %v", in.Default)
			}

			fmt.Fprintf(&out, "  %-*s  %-6s  %s\n", width, in.Name, in.Type, note)
		}
	}

	out.WriteString("\nSteps\n")

	width := 0
	for i := range d.Steps {
		width = max(width, len(d.Steps[i].ID))
	}

	for i := range d.Steps {
		st := &d.Steps[i]

		signoff := ""
		if st.HighBlast {
			signoff = "  [sign-off]"
		}

		fmt.Fprintf(&out, "  %-8s %-*s  %s%s\n", st.Kind, width, st.ID, firstLine(cmp.Or(st.Path, st.Command)), signoff)
	}

	fmt.Fprintf(&out, "\nRun: lw runbook apply %s", d.Slug)

	for _, name := range required {
		fmt.Fprintf(&out, " --var %s=<value>", name)
	}

	out.WriteString("\n")

	_, err := io.WriteString(w, out.String())

	return err
}
