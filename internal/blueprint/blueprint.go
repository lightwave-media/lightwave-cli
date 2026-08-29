// Package blueprint resolves a named blueprint from the canonical
// lightwave-core blueprint library and renders it with the Gruntwork
// `boilerplate` engine, which is linked in as a library.
//
// lw does NOT implement templating itself. It used to shell out to a
// `boilerplate` binary found on PATH (or ~/go/bin), which meant rendering
// depended on whatever version happened to be installed on the machine, could
// not be tested in-process, and failed differently per operator. The engine is
// now a pinned module dependency: same version everywhere, no install step,
// and generators are testable.
package blueprint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gruntwork-io/boilerplate/getterhelper"
	"github.com/gruntwork-io/boilerplate/options"
	"github.com/gruntwork-io/boilerplate/pkg/logging"
	"github.com/gruntwork-io/boilerplate/templates"
	"github.com/gruntwork-io/boilerplate/variables"
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
	DryRun        bool     // stage + list files, do not write to OutputFolder
}

// engineOptions maps a RenderOptions onto the boilerplate library's options,
// mirroring how upstream's own CLI assembles them (cli/parse_options.go) so
// behaviour matches the binary this replaced.
//
// Always non-interactive: lw is agent/CI-first, so every variable comes from
// Vars/VarFiles and blueprint defaults fill the rest. A prompt in an agent run
// is a hang, not a question.
func engineOptions(o *RenderOptions, outputFolder string) (*options.BoilerplateOptions, error) {
	vars, err := variables.ParseVars(o.Vars, o.VarFiles)
	if err != nil {
		return nil, fmt.Errorf("blueprint: parse vars: %w", err)
	}

	// DetermineTemplateConfig accepts a local directory or a remote ref and
	// splits it into the URL/folder pair the engine expects.
	templateURL, templateFolder, err := getterhelper.DetermineTemplateConfig(o.BlueprintPath)
	if err != nil {
		return nil, fmt.Errorf("blueprint: resolve template %q: %w", o.BlueprintPath, err)
	}

	return &options.BoilerplateOptions{
		TemplateURL:         templateURL,
		TemplateFolder:      templateFolder,
		OutputFolder:        outputFolder,
		Vars:                vars,
		ShellCommandAnswers: make(map[string]bool),
		OnMissingKey:        options.DefaultMissingKeyAction,
		OnMissingConfig:     options.DefaultMissingConfigAction,
		NonInteractive:      true,
		NoHooks:             o.NoHooks,
	}, nil
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
	if o.OutputFolder == "" {
		return errors.New("blueprint: --output-folder is required")
	}

	staging, err := os.MkdirTemp("", "lw-scaffold-")
	if err != nil {
		return fmt.Errorf("blueprint: create staging dir: %w", err)
	}

	defer func() { _ = os.RemoveAll(staging) }()

	engineOpts, err := engineOptions(o, staging)
	if err != nil {
		return err
	}

	// Warn-level: the engine's info chatter is noise in agent transcripts, but
	// real problems still surface. Errors come back through the return value.
	log := logging.New(os.Stderr, logging.LevelWarn)

	if _, err := templates.ProcessTemplateWithContext(
		ctx, log, engineOpts, engineOpts, &variables.Dependency{},
	); err != nil {
		return fmt.Errorf("blueprint: render %q: %w", o.BlueprintPath, err)
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

	if o.DryRun {
		files, listErr := relFiles(staging)
		if listErr != nil {
			return listErr
		}

		if _, err := fmt.Fprintf(os.Stdout, "dry-run: would write %d file(s) to %s:\n  %s\n",
			len(files), o.OutputFolder, strings.Join(files, "\n  ")); err != nil {
			return err
		}

		return nil
	}

	return copyTree(staging, o.OutputFolder)
}

// relFiles returns staged file paths relative to src, sorted.
func relFiles(src string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}

		files = append(files, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)

	return files, nil
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
