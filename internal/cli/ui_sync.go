package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/uisync"
	"github.com/spf13/cobra"
)

var (
	uiAddForce   bool
	uiAddNoDeps  bool
	uiSyncDryRun bool
)

var uiAddCmd = &cobra.Command{
	Use:   "add <Name | category/dir>",
	Short: "Copy a lightwave-ui component into this site and pin its provenance",
	Long: `Copies a component from the lightwave-ui checkout into the current site
(copy-in distribution) and records a pin in lightwave-ui.lock — the
provenance manifest mapping this site to lightwave-ui releases (shape:
lightwave-core data/ui site_config ui_release).

Updates never go through re-add: use ` + "`lw ui sync`" + ` (three-way) so local
customizations are preserved. Re-adding over an existing copy requires
--force and overwrites local edits.

Transitive dependencies are resolved automatically: the copied files' imports
are followed so sibling components (` + "`@/components/...`" + `, pinned too) and shared
src files (` + "`@/utils/cx`" + ` and friends) come along. Pass --no-deps to copy only
the named component's own files.

Examples:
  lw ui add Avatar          # also copies base/tooltip + @/utils/cx
  lw ui add Button          # resolves to base/buttons via the file layout
  lw ui add base/buttons    # explicit path under src/components
  lw ui add Badge           # resolves to base/badges via pluralization
  lw ui add Avatar --no-deps  # named component only, deps left to you`,
	Args: cobra.ExactArgs(1),
	RunE: runUIAdd,
}

var uiSyncCmd = &cobra.Command{
	Use:   "sync [Name]",
	Short: "Three-way sync pinned components against the lightwave-ui checkout",
	Long: `For every pinned component (or just <Name>), diffs local files against the
upstream lightwave-ui checkout using the pinned release tag as the merge
base:

  upstream changed, local untouched  → fast-forward (applied)
  local changed, upstream untouched  → local kept
  both changed                       → conflict: upstream written alongside
                                       as <file>.upstream; pin stays put so
                                       re-sync retries after you resolve

The pin (and lightwave-ui.lock) advances only on conflict-free syncs.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runUISync,
}

func init() {
	uiAddCmd.Flags().BoolVarP(&uiAddForce, "force", "f", false, "overwrite an existing local copy (loses local edits)")
	uiAddCmd.Flags().BoolVar(&uiAddNoDeps, "no-deps", false, "copy only the named component, skip transitive dependency resolution")
	uiSyncCmd.Flags().BoolVar(&uiSyncDryRun, "dry-run", false, "report outcomes without writing files")

	uiCmd.AddCommand(uiAddCmd)
	uiCmd.AddCommand(uiSyncCmd)
}

func uiRepoPath() string {
	cfg := config.Get()

	root := cfg.Paths.LightwaveRoot
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "dev")
	}

	return filepath.Join(root, "lightwave-ui")
}

// uiRepoVersion reads the lightwave-ui package version — the release every
// add/sync pins against.
func uiRepoVersion(uiRepo string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(uiRepo, "package.json"))
	if err != nil {
		return "", fmt.Errorf("reading lightwave-ui package.json: %w", err)
	}

	var pkg struct {
		Version string `json:"version"`
	}

	if err := json.Unmarshal(raw, &pkg); err != nil {
		return "", fmt.Errorf("parsing lightwave-ui package.json: %w", err)
	}

	if pkg.Version == "" {
		return "", errors.New("lightwave-ui package.json has no version")
	}

	return pkg.Version, nil
}

func runUIAdd(cmd *cobra.Command, args []string) error {
	uiRepo := uiRepoPath()

	version, err := uiRepoVersion(uiRepo)
	if err != nil {
		return err
	}

	siteDir, err := os.Getwd()
	if err != nil {
		return err
	}

	copied, err := uisync.Add(uiRepo, siteDir, args[0], version, uiAddForce, uiAddNoDeps, time.Now())
	if err != nil {
		return err
	}

	for _, f := range copied {
		color.Green("✓ %s", f)
	}

	color.Green("✓ pinned %s @ lightwave-ui v%s in %s", args[0], version, uisync.LockFile)

	return nil
}

func runUISync(cmd *cobra.Command, args []string) error {
	uiRepo := uiRepoPath()

	version, err := uiRepoVersion(uiRepo)
	if err != nil {
		return err
	}

	siteDir, err := os.Getwd()
	if err != nil {
		return err
	}

	lock, err := uisync.ReadLock(siteDir)
	if err != nil {
		return err
	}

	if len(lock.Components) == 0 {
		return fmt.Errorf("no pins in %s — `lw ui add` components first", uisync.LockFile)
	}

	base := uisync.GitBase(uiRepo)
	conflicts := 0

	for _, pin := range lock.Components {
		if len(args) == 1 && pin.Name != args[0] {
			continue
		}

		rel, err := uisync.ResolveComponentDir(uiRepo, pin.Name)
		if err != nil {
			return err
		}

		report, err := uisync.SyncComponent(uiRepo, siteDir, pin, rel, version, base, uiSyncDryRun, time.Now())
		if err != nil {
			return err
		}

		fmt.Printf("%s: %s → %s\n", report.Component, report.From, report.To)

		for _, f := range report.Files {
			fmt.Printf("  %-13s %s\n", f.Outcome, f.Path)
		}

		conflicts += report.Conflicts
	}

	if conflicts > 0 {
		color.Yellow("⚠ %d conflict(s): resolve the .upstream files, then re-run lw ui sync", conflicts)

		return fmt.Errorf("%d unresolved conflict(s)", conflicts)
	}

	if uiSyncDryRun {
		color.Yellow("dry-run: wrote nothing")
	}

	return nil
}
