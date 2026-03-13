package infra

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// TerragruntRunner wraps terragrunt commands
type TerragruntRunner struct {
	infraRoot string
	env       string
	region    string
}

// PlanResult represents the result of a plan operation
type PlanResult struct {
	HasChanges   bool
	AddCount     int
	ChangeCount  int
	DestroyCount int
	Output       string
}

// NewTerragruntRunner creates a new runner
func NewTerragruntRunner(infraRoot, env, region string) *TerragruntRunner {
	return &TerragruntRunner{
		infraRoot: infraRoot,
		env:       env,
		region:    region,
	}
}

// GetWorkingDir returns the working directory for the environment
func (t *TerragruntRunner) GetWorkingDir() string {
	return filepath.Join(t.infraRoot, "live", t.env, t.region)
}

// Plan runs terragrunt plan for a specific unit/stack
func (t *TerragruntRunner) Plan(ctx context.Context, path string) (*PlanResult, error) {
	workDir := filepath.Join(t.GetWorkingDir(), path)

	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("path does not exist: %s", workDir)
	}

	cmd := exec.CommandContext(ctx, "terragrunt", "plan", "-no-color")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")

	output, err := cmd.CombinedOutput()
	result := &PlanResult{
		Output: string(output),
	}

	if err != nil {
		// Check if it's just "no changes" (which returns 0)
		if strings.Contains(result.Output, "No changes") {
			return result, nil
		}
		return result, fmt.Errorf("plan failed: %w", err)
	}

	// Parse output for change counts
	result.HasChanges = strings.Contains(result.Output, "Plan:") &&
		!strings.Contains(result.Output, "Plan: 0 to add, 0 to change, 0 to destroy")

	return result, nil
}

// Apply runs terragrunt apply for a specific unit/stack
func (t *TerragruntRunner) Apply(ctx context.Context, path string, autoApprove bool) error {
	workDir := filepath.Join(t.GetWorkingDir(), path)

	args := []string{"apply", "-no-color"}
	if autoApprove {
		args = append(args, "-auto-approve")
	}

	cmd := exec.CommandContext(ctx, "terragrunt", args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// RunAll runs a command across all units
func (t *TerragruntRunner) RunAll(ctx context.Context, command string) error {
	workDir := t.GetWorkingDir()

	args := []string{"run-all", command, "--terragrunt-non-interactive"}

	cmd := exec.CommandContext(ctx, "terragrunt", args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// Output gets outputs from a specific unit
func (t *TerragruntRunner) Output(ctx context.Context, path string) (map[string]string, error) {
	workDir := filepath.Join(t.GetWorkingDir(), path)

	cmd := exec.CommandContext(ctx, "terragrunt", "output", "-json")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("output failed: %w", err)
	}

	// Parse JSON output (simplified - in production use encoding/json)
	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(strings.Trim(parts[0], "\""))
				result[key] = strings.TrimSpace(parts[1])
			}
		}
	}

	return result, nil
}

// ListUnits lists all terragrunt units in the environment
func (t *TerragruntRunner) ListUnits(ctx context.Context) ([]string, error) {
	var units []string
	workDir := t.GetWorkingDir()

	err := filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Skip .terragrunt-cache directories
		if info.IsDir() && strings.Contains(path, ".terragrunt-cache") {
			return filepath.SkipDir
		}

		if info.Name() == "terragrunt.hcl" && !strings.Contains(path, ".terragrunt-cache") {
			relPath, _ := filepath.Rel(workDir, filepath.Dir(path))
			if relPath != "." {
				units = append(units, relPath)
			}
		}
		return nil
	})

	return units, err
}

// Validate runs terragrunt validate
func (t *TerragruntRunner) Validate(ctx context.Context, path string) error {
	workDir := filepath.Join(t.GetWorkingDir(), path)

	cmd := exec.CommandContext(ctx, "terragrunt", "validate")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("validate failed: %s", string(output))
	}
	return nil
}
