// Package blueprint resolves a named blueprint from the canonical
// lightwave-core blueprint library and renders it via the Gruntwork
// `boilerplate` engine. lw does NOT implement templating itself — it
// resolves a blueprint directory by name and shells out to boilerplate.
package blueprint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// EnvBlueprintsDir overrides the blueprint library location.
const EnvBlueprintsDir = "LW_BLUEPRINTS_DIR"

// manifestName is the file every blueprint directory must contain.
const manifestName = "boilerplate.yml"

// BlueprintsDir resolves the canonical blueprint library:
//  1. $LW_BLUEPRINTS_DIR if set, else
//  2. <lightwaveRoot>/src/boilerplate/blueprints
func BlueprintsDir(lightwaveRoot string) string {
	if v := os.Getenv(EnvBlueprintsDir); v != "" {
		return v
	}

	return filepath.Join(lightwaveRoot, "lightwave-core", "src", "boilerplate", "blueprints")
}

// Resolve returns the absolute path to the blueprint named `name` inside
// `dir`. It errors clearly when the library or the blueprint is missing.
func Resolve(dir, name string) (string, error) {
	if name == "" {
		return "", errors.New("blueprint: empty name")
	}

	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("blueprint: library not found at %s (set %s): %w", dir, EnvBlueprintsDir, err)
	}

	path := filepath.Join(dir, name)

	manifest := filepath.Join(path, manifestName)
	if _, err := os.Stat(manifest); err != nil {
		return "", fmt.Errorf("blueprint %q not found (expected %s)", name, manifest)
	}

	return path, nil
}

// RenderOptions configures one boilerplate invocation.
type RenderOptions struct {
	BlueprintPath string   // --template-url (resolved blueprint dir)
	OutputFolder  string   // --output-folder
	Vars          []string // repeatable --var NAME=VALUE
	VarFiles      []string // repeatable --var-file FILE
	NoHooks       bool     // --no-hooks
}

// Args builds the boilerplate argument vector. Always non-interactive:
// lw is agent/CI-first, so every variable must come from --var/--var-file
// (blueprint defaults fill the rest). Pure function — golden-testable.
func Args(o *RenderOptions) []string {
	args := []string{
		"--template-url", o.BlueprintPath,
		"--output-folder", o.OutputFolder,
		"--non-interactive",
	}
	for _, v := range o.Vars {
		args = append(args, "--var", v)
	}

	for _, f := range o.VarFiles {
		args = append(args, "--var-file", f)
	}

	if o.NoHooks {
		args = append(args, "--no-hooks")
	}

	return args
}

// EnginePath finds the boilerplate binary: PATH first, then ~/go/bin.
func EnginePath() (string, error) {
	if p, err := exec.LookPath("boilerplate"); err == nil {
		return p, nil
	}

	if home, err := os.UserHomeDir(); err == nil {
		fallback := filepath.Join(home, "go", "bin", "boilerplate")
		if _, err := os.Stat(fallback); err == nil {
			return fallback, nil
		}
	}

	return "", errors.New("blueprint: boilerplate engine not found (install gruntwork-io/boilerplate; expected on PATH or ~/go/bin)")
}

// Render runs the boilerplate engine with the given options, streaming the
// engine's stdout/stderr through to the caller's.
func Render(ctx context.Context, o *RenderOptions) error {
	engine, err := EnginePath()
	if err != nil {
		return err
	}

	if o.OutputFolder == "" {
		return errors.New("blueprint: --output-folder is required")
	}

	cmd := exec.CommandContext(ctx, engine, Args(o)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("blueprint: boilerplate failed: %w", err)
	}

	return nil
}
