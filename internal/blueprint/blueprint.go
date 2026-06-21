// Package blueprint resolves a named blueprint from the canonical
// lightwave-core blueprint library and renders it via the Gruntwork
// `boilerplate` engine. lw does NOT implement templating itself — it
// resolves a blueprint directory by name and shells out to boilerplate.
package blueprint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// EnvBlueprintsDir overrides the blueprint library location.
const EnvBlueprintsDir = "LW_BLUEPRINTS_DIR"

// manifestName is the file every blueprint directory must contain.
const manifestName = "boilerplate.yml"

// dirPerm is the mode for directories created while committing a staged render.
const dirPerm fs.FileMode = 0o755

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
	Force         bool     // overwrite existing files (default: refuse on collision)
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
//
// It renders into a temporary staging dir FIRST, then commits the staged tree
// into o.OutputFolder. boilerplate overwrites unconditionally, so rendering
// straight into an existing repo silently clobbers files — e.g. `lw scaffold
// spec-repo -o .` overwriting the repo's README.md with the blueprint's
// spec/README index. Staging lets us detect collisions before touching the
// output folder and refuse them unless o.Force.
//
// NOTE: blueprint `after` hooks run against the staging dir (active blueprints
// use echo/format hooks, which is fine); a hook that depends on the final path
// would see staging — revisit if such a blueprint is added.
func Render(ctx context.Context, o *RenderOptions) error {
	engine, err := EnginePath()
	if err != nil {
		return err
	}

	if o.OutputFolder == "" {
		return errors.New("blueprint: --output-folder is required")
	}

	staging, err := os.MkdirTemp("", "lw-scaffold-")
	if err != nil {
		return fmt.Errorf("blueprint: create staging dir: %w", err)
	}

	defer func() { _ = os.RemoveAll(staging) }()

	staged := *o
	staged.OutputFolder = staging

	cmd := exec.CommandContext(ctx, engine, Args(&staged)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("blueprint: boilerplate failed: %w", err)
	}

	if !o.Force {
		clashes, err := collisions(staging, o.OutputFolder)
		if err != nil {
			return err
		}

		if len(clashes) > 0 {
			return fmt.Errorf(
				"blueprint: refusing to overwrite %d existing file(s) in %s (pass --force to overwrite):\n  %s",
				len(clashes), o.OutputFolder, strings.Join(clashes, "\n  "),
			)
		}
	}

	return copyTree(staging, o.OutputFolder)
}

// collisions returns the paths (relative to src) of staged files whose
// destination already exists under dst, sorted for stable output.
func collisions(src, dst string) ([]string, error) {
	var clashes []string

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		if _, statErr := os.Stat(filepath.Join(dst, rel)); statErr == nil {
			clashes = append(clashes, rel)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("blueprint: scanning staged files: %w", err)
	}

	sort.Strings(clashes)

	return clashes, nil
}

// copyTree copies every file under src into dst, creating parent dirs and
// preserving each source file's mode.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, dirPerm)
		}

		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}

	return out.Close()
}
