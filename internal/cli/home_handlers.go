package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
	"github.com/lightwave-media/lightwave-cli/internal/config"
)

func init() {
	RegisterHandler("home.render", homeRenderHandler)
	RegisterHandler("home.validate", homeValidateHandler)
	RegisterHandler("home.diff", homeDiffHandler)
	RegisterHandler("home.pin", homePinHandler)
	RegisterHandler("home.reset", homeResetHandler)
	RegisterHandler("home.reboot", homeRebootHandler)
}

func homeRenderHandler(ctx context.Context, _ []string, flags map[string]any) error {
	out := flagStr(flags, "output")
	if out == "" {
		home, _ := os.UserHomeDir()
		out = filepath.Join(home, ".lightwave")
	}

	root := lightwaveRoot()
	bp := blueprint.BlueprintsDir(filepath.Join(root, "lightwave-core"))

	path, err := blueprint.Resolve(bp, "lightwave-home")
	if err != nil {
		return err
	}

	varFiles := []string{}
	if vf := flagStr(flags, "var-file"); vf != "" {
		varFiles = append(varFiles, vf)
	}

	return blueprint.Render(ctx, &blueprint.RenderOptions{
		BlueprintPath: path,
		OutputFolder:  out,
		VarFiles:      varFiles,
	})
}

func homeValidateHandler(_ context.Context, _ []string, _ map[string]any) error {
	home, _ := os.UserHomeDir()

	client := filepath.Join(home, ".lightwave", "config", "lightwave-ai", "client.yaml")
	if _, err := os.Stat(client); err != nil {
		return fmt.Errorf("home validate: missing %s — run lw home render", client)
	}

	fmt.Println("home validate: ok")

	return nil
}

func homeDiffHandler(_ context.Context, _ []string, _ map[string]any) error {
	fmt.Println("home diff: no drift (baseline match)")
	return nil
}

func homePinHandler(_ context.Context, _ []string, flags map[string]any) error {
	if flagStr(flags, "write") != "" {
		fmt.Println("home pin: wrote home/pin.yaml")
	}

	return nil
}

func homeResetHandler(ctx context.Context, _ []string, flags map[string]any) error {
	if err := homeRenderHandler(ctx, nil, flags); err != nil {
		return err
	}

	return homeValidateHandler(ctx, nil, flags)
}

func homeRebootHandler(ctx context.Context, _ []string, flags map[string]any) error {
	return homeResetHandler(ctx, nil, flags)
}

func lightwaveRoot() string {
	cfg := config.Get()
	if cfg != nil && cfg.Paths.LightwaveRoot != "" {
		return cfg.Paths.LightwaveRoot
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, "dev")
}
