package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
)

// Schema-driven compose handlers. The blueprint at
// Infrastructure/blueprints/compose/ owns templates + var schema; this CLI is
// thin glue: validate env, locate paths + boilerplate binary, exec it.
//
// Replaces three legacy generators (scripts/generate_compose.py,
// scripts/codegen/compose.py, lightwave-platform/src/Makefile compose-generate)
// with a single Gruntwork-boilerplate-driven path.

func init() {
	RegisterHandler("compose.generate", composeGenerateHandler)
	RegisterHandler("compose.verify", composeVerifyHandler)
}

const composeRenderTimeout = 60 * time.Second

// composeBlueprintRel is the path of the compose-only sub-blueprint relative
// to the workspace root. Resolved at runtime against config.Paths.LightwaveRoot.
const composeBlueprintRel = "Infrastructure/blueprints/compose"

// composeOutputName is the filename the blueprint emits at the workspace root.
const composeOutputName = "docker-compose.yml"

// validComposeEnvs constrains --env to the set the blueprint ships var files
// for. Anything else is a user error, surfaced before we shell out.
var validComposeEnvs = map[string]bool{
	"local":      true,
	"staging":    true,
	"production": true,
}

func composeGenerateHandler(ctx context.Context, _ []string, flags map[string]any) error {
	env, err := composeEnv(flags)
	if err != nil {
		return err
	}
	root, err := composeWorkspaceRoot()
	if err != nil {
		return err
	}

	if flagBool(flags, "dry-run") {
		// Dry run: render to a tmp dir, print the diff vs. the committed file
		// (or a "would create" line if no committed file exists). No mutation.
		tmp, err := os.MkdirTemp("", "lw-compose-dryrun-*")
		if err != nil {
			return fmt.Errorf("mktemp: %w", err)
		}
		defer os.RemoveAll(tmp)
		if err := runBoilerplate(ctx, root, env, tmp); err != nil {
			return err
		}
		return printComposeDiff(filepath.Join(root, composeOutputName), filepath.Join(tmp, composeOutputName))
	}

	if err := runBoilerplate(ctx, root, env, root); err != nil {
		return err
	}
	fmt.Printf("%s docker-compose.yml regenerated for %s\n",
		color.GreenString("✓"), color.CyanString(env))
	return nil
}

func composeVerifyHandler(ctx context.Context, _ []string, flags map[string]any) error {
	env, err := composeEnv(flags)
	if err != nil {
		return err
	}
	root, err := composeWorkspaceRoot()
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "lw-compose-verify-*")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := runBoilerplate(ctx, root, env, tmp); err != nil {
		return err
	}

	committed := filepath.Join(root, composeOutputName)
	rendered := filepath.Join(tmp, composeOutputName)

	if err := composeFilesEqual(committed, rendered); err != nil {
		// Print the diff for human triage, then fail with a remediation hint.
		_ = printComposeDiff(committed, rendered)
		return fmt.Errorf("docker-compose.yml drifted from SST. Run: lw compose generate --env %s", env)
	}

	fmt.Printf("%s docker-compose.yml in sync with SST (%s)\n",
		color.GreenString("✓"), color.CyanString(env))
	return nil
}

// composeEnv extracts and validates --env, defaulting to "local" so
// `lw compose verify` works without args (the common pre-commit case).
func composeEnv(flags map[string]any) (string, error) {
	env := flagStrOr(flags, "env", "local")
	if !validComposeEnvs[env] {
		return "", fmt.Errorf("invalid --env %q (valid: local, staging, production)", env)
	}
	return env, nil
}

// composeWorkspaceRoot returns the absolute workspace root, failing if the
// configured path doesn't exist (catches misconfigured LW_PATHS_LIGHTWAVE_ROOT
// before we shell anything out).
func composeWorkspaceRoot() (string, error) {
	cfg := config.Get()
	if cfg == nil {
		return "", fmt.Errorf("config not loaded")
	}
	root := cfg.Paths.LightwaveRoot
	if root == "" {
		return "", fmt.Errorf("paths.lightwave_root not configured")
	}
	if _, err := os.Stat(root); err != nil {
		return "", fmt.Errorf("workspace root %s: %w", root, err)
	}
	return root, nil
}

// runBoilerplate shells out to the `boilerplate` binary, rendering the
// compose blueprint with the env-specific var file into outDir.
func runBoilerplate(ctx context.Context, root, env, outDir string) error {
	bp, err := exec.LookPath("boilerplate")
	if err != nil {
		return fmt.Errorf("boilerplate binary not on PATH (install: https://github.com/gruntwork-io/boilerplate)")
	}

	tmpl := filepath.Join(root, composeBlueprintRel)
	if _, err := os.Stat(tmpl); err != nil {
		return fmt.Errorf("compose blueprint not found at %s — has the workspace PR landed?", tmpl)
	}
	varFile := filepath.Join(tmpl, "vars", env+".yml")
	if _, err := os.Stat(varFile); err != nil {
		return fmt.Errorf("var file not found at %s", varFile)
	}

	ctx, cancel := context.WithTimeout(ctx, composeRenderTimeout)
	defer cancel()

	c := exec.CommandContext(ctx, bp,
		"--template-url", tmpl,
		"--output-folder", outDir,
		"--var-file", varFile,
		"--non-interactive",
	)
	c.Dir = root
	out, err := c.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("boilerplate: timeout after %s", composeRenderTimeout)
		}
		return fmt.Errorf("boilerplate: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// composeFilesEqual returns nil iff the two files have byte-identical contents.
// Errors with a non-nil err indicate a read problem; mismatch returns a
// sentinel error the caller can use to gate diff printing.
func composeFilesEqual(a, b string) error {
	ab, err := os.ReadFile(a)
	if err != nil {
		return fmt.Errorf("read %s: %w", a, err)
	}
	bb, err := os.ReadFile(b)
	if err != nil {
		return fmt.Errorf("read %s: %w", b, err)
	}
	if string(ab) != string(bb) {
		return errComposeDrift
	}
	return nil
}

var errComposeDrift = errors.New("compose files differ")

// printComposeDiff shells out to `diff -u` for human-readable output. Best-effort:
// if `diff` is missing or fails internally, we still surface the mismatch via
// the caller's error path.
func printComposeDiff(a, b string) error {
	c := exec.Command("diff", "-u", a, b)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	// diff exits 1 on differences — that's the expected case here, not an error.
	_ = c.Run()
	return nil
}
