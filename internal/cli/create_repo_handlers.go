package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
	"github.com/lightwave-media/lightwave-cli/internal/githuborg"
)

func init() {
	RegisterHandler("create.repo", createRepoHandler)
	RegisterHandler("scaffold.from-delta", scaffoldFromDeltaHandler)
}

func createRepoHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw create repo <name>")
	}

	name := args[0]

	org := flagStr(flags, "org")
	if org == "" {
		org = githuborg.DefaultOrg
	}

	home, _ := os.UserHomeDir()

	out := flagStr(flags, "clone")
	if out == "" {
		out = filepath.Join(home, "dev", name)
	}

	if flagBool(flags, "dry-run") {
		fmt.Printf("would create %s/%s, render repo-bootstrap at %s, push, org-bootstrap\n", org, name, out)
		return nil
	}

	root := lightwaveRoot()
	bpDir := blueprint.BlueprintsDir(filepath.Join(root, "lightwave-core"))

	bpPath, err := blueprint.Resolve(bpDir, "repo-bootstrap")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(out, codegenDirPerm); err != nil {
		return err
	}

	if err := blueprint.Render(ctx, &blueprint.RenderOptions{
		BlueprintPath: bpPath,
		OutputFolder:  out,
		Vars: []string{
			"repo_name=" + name,
			"org=" + org,
			"repo_kind=" + repoKind(flags),
		},
	}); err != nil {
		return err
	}

	if err := gitInitCommit(ctx, out, name); err != nil {
		return err
	}

	createArgs := []string{
		"repo", "create", org + "/" + name,
		"--private", "--source", out, "--remote", "origin", "--push",
	}
	create := exec.CommandContext(ctx, "gh", createArgs...)
	create.Stdout = os.Stdout
	create.Stderr = os.Stderr

	if err := create.Run(); err != nil {
		return fmt.Errorf("gh repo create: %w", err)
	}

	if err := githuborg.RunBootstrap(ctx, githuborg.Options{
		Org:           org,
		LightwaveRoot: root,
		TargetRepo:    name,
	}); err != nil {
		return fmt.Errorf("org bootstrap: %w", err)
	}

	fmt.Printf("created %s/%s at %s (org bootstrap applied)\n", org, name, out)

	return nil
}

func gitInitCommit(ctx context.Context, dir, name string) error {
	run := func(cmd string, args ...string) error {
		c := exec.CommandContext(ctx, cmd, args...)
		c.Dir = dir
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr

		return c.Run()
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := run("git", "init", "-b", "main"); err != nil {
			return err
		}
	}

	if err := run("git", "add", "-A"); err != nil {
		return err
	}

	status, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return err
	}

	if len(strings.TrimSpace(string(status))) == 0 {
		return nil
	}

	return run("git", "commit", "-m", fmt.Sprintf("chore: initial scaffold from repo-bootstrap (%s)", name))
}

func scaffoldFromDeltaHandler(_ context.Context, _ []string, flags map[string]any) error {
	fmt.Printf("scaffold from-delta: item %s (stub)\n", flagStr(flags, "item"))
	return nil
}

func repoKind(flags map[string]any) string {
	if k := flagStr(flags, "kind"); k != "" {
		return k
	}

	return "generic"
}
