package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
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
		org = "lightwave-media"
	}

	if flagBool(flags, "dry-run") {
		fmt.Printf("would create %s/%s and render repo-bootstrap\n", org, name)
		return nil
	}

	create := exec.CommandContext(ctx, "gh", "repo", "create", org+"/"+name, "--private")
	create.Stdout = os.Stdout

	create.Stderr = os.Stderr
	if err := create.Run(); err != nil {
		return fmt.Errorf("gh repo create: %w", err)
	}

	home, _ := os.UserHomeDir()
	out := filepath.Join(home, "dev", name)
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

	fmt.Printf("created %s/%s scaffold at %s\n", org, name, out)

	return nil
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
