// Package githuborg bootstraps lightwave-media GitHub org assets (swarm labels,
// milestones).
package githuborg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/lightwave-media/lightwave-cli/internal/github"
)

const (
	DefaultOrg = github.DefaultOrg
	// The bootstrap script moved into this repo's own scripts/ as a
	// self-checkout (#281, 2026-07-27) so the org-sync workflow no longer
	// clones a private sibling. This constant still pointed at the old
	// sibling-repo path, so every CI run — which checks out only
	// lightwave-cli (+ lightwave-core for the schema) — failed to resolve
	// it while a dev machine's ~/dev/lightwave-infrastructure-catalog
	// leftover masked the same bug locally.
	BootstrapScriptRel = "lightwave-cli/scripts/bootstrap-github-org.sh"
)

// Options controls bootstrap.
type Options struct {
	Org           string
	LightwaveRoot string
	TargetRepo    string
	DryRun        bool
}

// ResolveBootstrapScript locates the org bootstrap shell script under ~/dev.
func ResolveBootstrapScript(lightwaveRoot string) (string, error) {
	if lightwaveRoot == "" {
		home, _ := os.UserHomeDir()
		lightwaveRoot = filepath.Join(home, "dev")
	}
	path := filepath.Join(lightwaveRoot, BootstrapScriptRel)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("bootstrap script not found at %s: %w", path, err)
	}
	return path, nil
}

// RunBootstrap executes bootstrap-github-org.sh (idempotent).
func RunBootstrap(ctx context.Context, opts Options) error {
	if opts.Org == "" {
		opts.Org = DefaultOrg
	}
	if opts.DryRun {
		if opts.TargetRepo != "" {
			fmt.Printf("would bootstrap org slice for %s/%s\n", opts.Org, opts.TargetRepo)
		} else {
			fmt.Printf("would bootstrap full org %s\n", opts.Org)
		}
		return nil
	}

	script, err := ResolveBootstrapScript(opts.LightwaveRoot)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Env = append(os.Environ(), "ORG_LOGIN="+opts.Org)
	if opts.TargetRepo != "" {
		cmd.Env = append(cmd.Env, "TARGET_REPO="+opts.TargetRepo)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
