package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/blueprint"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/homepolicy"
)

func init() {
	RegisterHandler("home.render", homeRenderHandler)
	RegisterHandler("home.sync", homeSyncHandler)
	RegisterHandler("home.validate", homeValidateHandler)
	RegisterHandler("home.diff", homeDiffHandler)
	RegisterHandler("home.pin", homePinHandler)
	RegisterHandler("home.reset", homeResetHandler)
	RegisterHandler("home.reboot", homeRebootHandler)
	RegisterHandler("home.doctor", homeDoctorHandler)
}

// homeDoctorHandler runs a read-only slop/drift scan of the rendered
// ~/.lightwave print and reports signals by class. It shells out to the
// existing detector (slop.ts, a flagless scan-only bun script that exits 0),
// streaming its report straight through. Read-only sibling to home diff: it
// never writes. Degrades gracefully when the operator runtime isn't
// provisioned (slop.ts absent) or bun isn't installed, rather than panicking.
func homeDoctorHandler(ctx context.Context, _ []string, _ map[string]any) error {
	start := time.Now()

	home, _ := os.UserHomeDir()
	slop := filepath.Join(home, ".lightwave", "lib", "maintenance", "slop.ts")

	if _, err := os.Stat(slop); err != nil {
		msg := fmt.Sprintf("slop detector not found at %s — run lw home render to provision the operator runtime", slop)
		emitOperatorCLI("home.doctor", "fail", msg, 1, start, nil)

		return errors.New("home doctor: " + msg)
	}

	bun, err := exec.LookPath("bun")
	if err != nil {
		msg := "bun not found on PATH — required to run the slop detector"
		emitOperatorCLI("home.doctor", "fail", msg, 1, start, nil)

		return errors.New("home doctor: " + msg)
	}

	cmd := exec.CommandContext(ctx, bun, slop)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if runErr := cmd.Run(); runErr != nil {
		emitOperatorCLI("home.doctor", "fail", runErr.Error(), 1, start, nil)

		return fmt.Errorf("home doctor: slop scan failed: %w", runErr)
	}

	emitOperatorCLI("home.doctor", "pass", "slop scan complete", 0, start, nil)

	return nil
}

func homeSyncHandler(_ context.Context, _ []string, _ map[string]any) error {
	start := time.Now()

	result, err := homepolicy.SyncBaseline()
	if err != nil {
		emitOperatorCLI("home.sync", "fail", err.Error(), 1, start, nil)
		return err
	}

	measurements := map[string]any{"files_updated": len(result.Updated)}

	detail := "policy print already current"
	if len(result.Updated) > 0 {
		detail = fmt.Sprintf("updated %d file(s)", len(result.Updated))
		fmt.Printf("home sync: updated %d policy file(s):\n", len(result.Updated))

		for _, rel := range result.Updated {
			fmt.Printf("  ~ %s\n", rel)
		}
	} else {
		fmt.Println("home sync: policy print already current")
	}

	emitOperatorCLI("home.sync", "pass", detail, 0, start, measurements)

	return nil
}

func homeRenderHandler(ctx context.Context, _ []string, flags map[string]any) error {
	out := flagStr(flags, "output")
	if out == "" {
		home, _ := os.UserHomeDir()
		out = filepath.Join(home, ".lightwave")
	}

	root := lightwaveRoot()
	bp := blueprint.BlueprintsDir(root)

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
	return errors.New("home diff is decommissioned: no baseline comparison is implemented; runtime drift has not been measured")
}

func homePinHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errors.New("home pin is decommissioned: no pin writer is implemented; no release pin was changed")
}

func homeResetHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errors.New("home reset is decommissioned: template rendering is not a verified runtime reset; preserve canonical files and bindings and rehearse recovery first")
}

func homeRebootHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errors.New("home reboot is decommissioned: service restart and recovery verification are not implemented")
}

func lightwaveRoot() string {
	cfg := config.Get()
	if cfg != nil && cfg.Paths.LightwaveRoot != "" {
		return cfg.Paths.LightwaveRoot
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, "dev")
}
