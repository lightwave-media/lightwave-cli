package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/uicatalog"
	"github.com/spf13/cobra"
)

var uiCatalogJSON bool
var uiSearchJSON bool

var uiCatalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List existing lightwave-ui components (not a blank file)",
	Long: `Prints every current lightwave-ui component print. Catalog search
matches covers (need phrases) when present; name and path remain searchable.

Dead end: catalog/stamp unreachable — no files are written.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runUICatalog,
}

var uiSearchCmd = &cobra.Command{
	Use:   "search <need>",
	Short: "Search lightwave-ui components by what the screen needs",
	Long: `Matches every token in <need> against name, path, category, and
covers / does_not_cover. Folder names alone are not the search axis.

No hits is a named gap: register a new variant in lightwave-ui, do not
invent an app-local copy.`,
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runUISearch,
}

func init() {
	uiCatalogCmd.Flags().BoolVar(&uiCatalogJSON, "json", false, "JSON output")
	uiSearchCmd.Flags().BoolVar(&uiSearchJSON, "json", false, "JSON output")
	uiCmd.AddCommand(uiCatalogCmd)
	uiCmd.AddCommand(uiSearchCmd)
}

func runUICatalog(cmd *cobra.Command, _ []string) error {
	entries, err := loadUICatalog()
	if err != nil {
		return err
	}
	return printUIEntries(cmd, entries, uiCatalogJSON)
}

func runUISearch(cmd *cobra.Command, args []string) error {
	entries, err := loadUICatalog()
	if err != nil {
		return err
	}
	hits := uicatalog.Search(entries, args[0])
	if len(hits) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(),
			"no match for %q — register a new variant in lightwave-ui, do not invent an app-local copy\n",
			args[0])
		return nil
	}
	return printUIEntries(cmd, hits, uiSearchJSON)
}

func loadUICatalog() ([]uicatalog.Entry, error) {
	uiRepo, err := uiRepoPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(uiRepo); err != nil {
		return nil, fmt.Errorf("catalog unreachable at %s: %w", uiRepo, err)
	}
	entries, err := uicatalog.List(uiRepo)
	if err != nil {
		return nil, fmt.Errorf("catalog unreachable at %s: %w", uiRepo, err)
	}
	return entries, nil
}

func printUIEntries(cmd *cobra.Command, entries []uicatalog.Entry, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	for _, entry := range entries {
		covers := strings.Join(entry.Covers, ", ")
		if covers == "" {
			covers = "(no covers print)"
		}
		gap := strings.Join(entry.DoesNotCover, ", ")
		if gap == "" {
			gap = "-"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\tcovers: %s\tgap: %s\n",
			entry.Path, entry.Name, covers, gap)
	}
	return nil
}
