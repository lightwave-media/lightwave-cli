package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/spf13/cobra"
)

var selfCmd = &cobra.Command{
	Use:   "self",
	Short: "Manage the lw binary (dev fast path)",
	Long: `Rebuild lw from the sibling lightwave-cli checkout.

Use after merging CLI features or pulling main — avoids waiting for the
nightly Homebrew release train.

Examples:
  lw self sync
  lw self sync --home`,
}

var selfSyncHome bool

var selfSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Rebuild lw from ~/dev/lightwave-cli into ~/.local/bin",
	RunE:  selfSyncRun,
}

func init() {
	selfSyncCmd.Flags().BoolVar(&selfSyncHome, "home", false, "Also run lw home render after sync")
	selfCmd.AddCommand(selfSyncCmd)
}

func selfSyncRun(_ *cobra.Command, _ []string) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("self sync: config not loaded")
	}

	cliRoot := filepath.Join(cfg.Paths.LightwaveRoot, "lightwave-cli")
	if _, err := os.Stat(filepath.Join(cliRoot, "go.mod")); err != nil {
		return fmt.Errorf("self sync: lightwave-cli not found at %s", cliRoot)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	out := filepath.Join(home, ".local/bin", "lw")
	if err := os.MkdirAll(filepath.Dir(out), codegenDirPerm); err != nil {
		return err
	}

	build := exec.CommandContext(context.Background(), "go", "build", "-o", out, "./cmd/lw")
	build.Dir = cliRoot
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr

	if err := build.Run(); err != nil {
		return fmt.Errorf("self sync: go build: %w", err)
	}

	fmt.Printf("self sync: installed %s\n", out)

	if selfSyncHome {
		render := exec.CommandContext(context.Background(), out, "home", "render")
		render.Stdout = os.Stdout
		render.Stderr = os.Stderr

		if err := render.Run(); err != nil {
			return fmt.Errorf("self sync: home render: %w", err)
		}
	}

	return nil
}
