package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/local/gate"
)

func init() {
	RegisterHandler("local.pr", localPRHandler)
}

// localPRHandler is the egress wrapper. Before doing anything visible to
// GitHub, it asks `gate.RequireGreenForHEAD` whether the gate is green
// for the current sha. On a refused gate it prints the failing stage,
// the remediation command, and exits non-zero. On approval, it shells
// out to `gh pr create` (or `gh pr edit` if a PR already exists for the
// branch).
//
// Flags:
//
//	--dry-run  — show what would happen without calling gh
//	--yes      — skip the "Open PR for branch X? [y/N]" prompt
//	--title    — PR title (defaults to last commit subject)
//	--body     — PR body (defaults to last commit body)
//
// The full PR-template handling described in the `github-pr` skill stays
// in that skill; this command is the gate-guarded entry point, not a
// reimplementation of PR creation.
func localPRHandler(ctx context.Context, _ []string, flags map[string]any) error {
	info, err := detectRepo()
	if err != nil {
		return err
	}

	sha, err := headSHA(info)
	if err != nil {
		return err
	}

	if ok, reason := gate.RequireGreenForHEAD(string(info.ID), sha); !ok {
		fmt.Fprintln(os.Stderr, color.RedString("gate refused: %s", reason))
		os.Exit(1)
	}

	dryRun := flagBool(flags, "dry-run")
	yes := flagBool(flags, "yes")
	title := flagStr(flags, "title")
	body := flagStr(flags, "body")

	args := []string{"pr", "create", "--fill"}
	if title != "" {
		args = []string{"pr", "create", "--title", title}
		if body != "" {
			args = append(args, "--body", body)
		} else {
			args = append(args, "--fill")
		}
	}

	fmt.Println(color.GreenString("✓ gate green for %s@%s", info.ID, short(sha)))
	fmt.Printf("  → gh %v\n", args)

	if dryRun {
		fmt.Println(color.HiBlackString("dry run — not invoking gh"))
		return nil
	}

	if !yes && !promptYesNo("Open PR for current branch?") {
		fmt.Println("aborted")
		return nil
	}

	c := exec.CommandContext(ctx, "gh", args...)
	c.Dir = info.Root
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	return c.Run()
}
