package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
)

// Known Makefile scopes and their paths relative to the repo root
var makeScopes = map[string]string{
	"root":     ".",
	"platform": "lightwave-platform/src",
	"cli":      "packages/lightwave-cli",
	"augusta":  "packages/lightwave-sys",
	"infra":    "Infrastructure/live",
	"catalog":  "Infrastructure/catalog",
}

// resolveMakeDir returns the absolute directory for a scope
func resolveMakeDir(scope string) (string, error) {
	rel, ok := makeScopes[scope]
	if !ok {
		return "", fmt.Errorf("unknown scope %q (valid: %s)", scope, scopeList())
	}
	cfg := config.Get()
	return filepath.Join(cfg.Paths.LightwaveRoot, rel), nil
}

// runMake runs a make target in the given directory, streaming output.
// It resolves COMPOSE_FILE paths to absolute so docker compose works
// regardless of which subdirectory the Makefile lives in.
func runMake(dir, target string, extraArgs ...string) error {
	args := []string{target}
	args = append(args, extraArgs...)

	cmd := exec.Command("make", args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// COMPOSE_FILE may contain relative paths (set by .envrc at workspace root).
	// When cmd.Dir differs from the workspace root, docker compose resolves
	// those relative paths from cmd.Dir and fails. Fix: make them absolute.
	cfg := config.Get()
	if composeFile := os.Getenv("COMPOSE_FILE"); composeFile != "" {
		parts := strings.Split(composeFile, ":")
		for i, p := range parts {
			if !filepath.IsAbs(p) {
				parts[i] = filepath.Join(cfg.Paths.LightwaveRoot, p)
			}
		}
		cmd.Env = append(os.Environ(), "COMPOSE_FILE="+strings.Join(parts, ":"))
	}

	return cmd.Run()
}

// listMakeTargets parses a Makefile and prints its targets
func listMakeTargets(scope string) error {
	dir, err := resolveMakeDir(scope)
	if err != nil {
		return err
	}

	path := filepath.Join(dir, "Makefile")
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read Makefile: %w", err)
	}
	defer f.Close()

	fmt.Printf("Targets in %s (%s):\n\n", color.CyanString(scope), dir)

	scanner := bufio.NewScanner(f)
	var comment string
	for scanner.Scan() {
		line := scanner.Text()

		// Capture comments immediately before targets
		if strings.HasPrefix(line, "#") {
			comment = strings.TrimPrefix(line, "# ")
			continue
		}

		// Match target lines: "target-name:" (skip internal targets starting with _)
		if len(line) > 0 && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, ".") && !strings.HasPrefix(line, "_") {
			if idx := strings.IndexByte(line, ':'); idx > 0 {
				target := line[:idx]
				// Skip variable assignments and multi-word "targets"
				if strings.ContainsAny(target, " \t=") {
					comment = ""
					continue
				}
				if comment != "" {
					fmt.Printf("  %-24s %s\n", color.GreenString(target), comment)
				} else {
					fmt.Printf("  %s\n", color.GreenString(target))
				}
			}
		}
		if !strings.HasPrefix(line, "#") {
			comment = ""
		}
	}
	fmt.Println()
	return scanner.Err()
}

func scopeList() string {
	names := make([]string, 0, len(makeScopes))
	for k := range makeScopes {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}
