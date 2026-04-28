package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/sst"
)

// Schema-driven check handlers. commands.yaml v3.0.0 declares 12 checks:
// ci, ruff, types, domains, schema, locks, deps, git, aws, docker, ecs, smoke.
//
// Most thin-wrap an existing Make target; locks/git/docker/aws are direct
// probes; schema reuses the Phase 3 drift validator core.

func init() {
	RegisterHandler("check.ci", checkCIHandler)
	RegisterHandler("check.ruff", checkRuffHandler)
	RegisterHandler("check.types", checkTypesHandler)
	RegisterHandler("check.domains", checkDomainsHandler)
	RegisterHandler("check.schema", checkSchemaHandler)
	RegisterHandler("check.locks", checkLocksHandler)
	RegisterHandler("check.deps", checkDepsHandler)
	RegisterHandler("check.git", checkGitHandler)
	RegisterHandler("check.aws", checkAWSHandler)
	RegisterHandler("check.docker", checkDockerHandler)
	RegisterHandler("check.ecs", checkECSHandler)
	RegisterHandler("check.smoke", checkSmokeHandler)
}

func checkCIHandler(_ context.Context, _ []string, flags map[string]any) error {
	dir, err := resolveMakeDir("root")
	if err != nil {
		return err
	}
	target := "ci-local"
	if flagBool(flags, "skip-tests") {
		target = "ci-local-fast"
	}
	return runMake(dir, target)
}

func checkRuffHandler(_ context.Context, _ []string, flags map[string]any) error {
	dir, err := resolveMakeDir("platform")
	if err != nil {
		return err
	}
	if flagBool(flags, "fix") {
		return runMake(dir, "ruff-fix")
	}
	return runMake(dir, "ruff")
}

func checkTypesHandler(_ context.Context, _ []string, _ map[string]any) error {
	dir, err := resolveMakeDir("platform")
	if err != nil {
		return err
	}
	return runMake(dir, "npm-type-check")
}

func checkDomainsHandler(_ context.Context, _ []string, _ map[string]any) error {
	dir, err := resolveMakeDir("platform")
	if err != nil {
		return err
	}
	return runMake(dir, "lint-api-domains")
}

// checkSchemaHandler is the Phase 3 drift validator, re-shaped for the
// dispatcher. Default mode: report drift, exit 0. Caller pairs with `--strict`
// shape via direct cobra invocation if needed (lw check schema --json), or
// the legacy checkSchemaCmd wrapper still exists for the cobra path.
func checkSchemaHandler(_ context.Context, _ []string, flags map[string]any) error {
	cfg := config.Get()
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}
	schema, err := sst.LoadCLIConfig(cfg.Paths.LightwaveRoot)
	if err != nil {
		return fmt.Errorf("load CLI schema: %w", err)
	}

	schemaKeys := schema.Keys()
	registryKeys := RegisteredKeys()

	schemaSet := make(map[string]bool, len(schemaKeys))
	for _, k := range schemaKeys {
		schemaSet[k] = true
	}
	registrySet := make(map[string]bool, len(registryKeys))
	for _, k := range registryKeys {
		registrySet[k] = true
	}

	var missing, orphaned []string
	for _, k := range schemaKeys {
		if !registrySet[k] {
			missing = append(missing, k)
		}
	}
	for _, k := range registryKeys {
		if !schemaSet[k] {
			orphaned = append(orphaned, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(orphaned)

	report := schemaDriftReport{
		SchemaVersion:    schema.Version,
		DomainCount:      len(schema.Domains),
		CommandCount:     len(schemaKeys),
		HandlerCount:     len(registryKeys),
		MissingHandlers:  missing,
		OrphanedHandlers: orphaned,
	}
	if len(schemaKeys) > 0 {
		matched := len(schemaKeys) - len(missing)
		report.HandlerMatchRatio = float64(matched) / float64(len(schemaKeys))
	}

	if asJSON(flags) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	printSchemaDriftHuman(report)
	return nil
}

// checkLocksHandler verifies that uv.lock and pnpm-lock.yaml are committed
// (no uncommitted changes that would drift CI). Fast — uses git status.
func checkLocksHandler(_ context.Context, _ []string, _ map[string]any) error {
	cfg := config.Get()
	root := cfg.Paths.LightwaveRoot
	files := []string{"uv.lock", "pnpm-lock.yaml"}
	dirty := []string{}
	for _, f := range files {
		out, err := runGitDiff(root, f)
		if err != nil {
			return fmt.Errorf("git diff %s: %w", f, err)
		}
		if strings.TrimSpace(out) != "" {
			dirty = append(dirty, f)
		}
	}
	if len(dirty) > 0 {
		return fmt.Errorf("uncommitted lock changes: %s", strings.Join(dirty, ", "))
	}
	fmt.Println(color.GreenString("✓ lock files clean"))
	return nil
}

// checkDepsHandler verifies workspace dependency consistency. Delegates to
// the Make target (no direct Go impl — pnpm/uv would re-implement work).
func checkDepsHandler(_ context.Context, _ []string, _ map[string]any) error {
	dir, err := resolveMakeDir("root")
	if err != nil {
		return err
	}
	return runMake(dir, "deps-check")
}

// checkGitHandler ensures the working tree is clean (no uncommitted changes
// to tracked files; untracked files are allowed).
func checkGitHandler(_ context.Context, _ []string, _ map[string]any) error {
	cfg := config.Get()
	root := cfg.Paths.LightwaveRoot
	c := exec.Command("git", "diff", "--quiet")
	c.Dir = root
	if err := c.Run(); err != nil {
		return fmt.Errorf("uncommitted changes in tracked files")
	}
	fmt.Println(color.GreenString("✓ working tree clean"))
	return nil
}

// checkAWSHandler verifies AWS credentials resolve via STS.
func checkAWSHandler(_ context.Context, _ []string, _ map[string]any) error {
	c := exec.Command("aws", "sts", "get-caller-identity")
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("AWS credentials not configured: %s", strings.TrimSpace(string(out)))
	}
	fmt.Println(color.GreenString("✓ AWS credentials valid"))
	return nil
}

// checkDockerHandler verifies the Docker daemon is reachable.
func checkDockerHandler(_ context.Context, _ []string, _ map[string]any) error {
	c := exec.Command("docker", "info")
	if err := c.Run(); err != nil {
		return fmt.Errorf("docker daemon not running")
	}
	fmt.Println(color.GreenString("✓ Docker daemon running"))
	return nil
}

func checkECSHandler(_ context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw check ecs <service> [--environment=<env>]")
	}
	env := flagStrOr(flags, "environment", "prod")
	cluster := fmt.Sprintf("platform-%s", env)
	c := exec.Command("aws", "ecs", "describe-services",
		"--cluster", cluster, "--services", args[0],
		"--query", "services[0].{Status:status,Desired:desiredCount,Running:runningCount}",
		"--output", "table")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func checkSmokeHandler(_ context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw check smoke <target> [--env=<env>]")
	}
	dir, err := resolveMakeDir("platform")
	if err != nil {
		return err
	}
	extra := []string{"TARGET=" + args[0]}
	if env := flagStr(flags, "env"); env != "" {
		extra = append(extra, "ENV="+env)
	}
	return runMake(dir, "smoke", extra...)
}

// runGitDiff returns the diff output for a path. Empty string = clean.
func runGitDiff(root, path string) (string, error) {
	c := exec.Command("git", "diff", "--", path)
	c.Dir = root
	out, err := c.CombinedOutput()
	if err != nil {
		// non-zero exit also means changes — but we want stdout regardless
		return string(out), nil
	}
	return string(out), nil
}
