package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// Plan runs terragrunt plan for a specific unit/stack.
// Output streams to terminal in real-time and is captured for change detection.
func (t *TerragruntRunner) Plan(ctx context.Context, path string) (*PlanResult, error) {
	workDir := filepath.Join(t.GetWorkingDir(), path)

	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("path does not exist: %s", workDir)
	}

	cmd := exec.CommandContext(ctx, "terragrunt", "plan", "-no-color")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")

	// Stream to terminal AND capture for parsing
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)

	err := cmd.Run()
	output := buf.String()
	result := &PlanResult{
		Output: output,
	}

	if err != nil {
		if strings.Contains(output, "No changes") {
			return result, nil
		}
		return result, fmt.Errorf("plan failed: %w\n\n%s", err, lastLines(output, 30))
	}

	// Parse output for change counts
	result.HasChanges = strings.Contains(output, "Plan:") &&
		!strings.Contains(output, "Plan: 0 to add, 0 to change, 0 to destroy")

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

	args := []string{
		"run-all", command,
		"--terragrunt-non-interactive",
		// Exclude Terragrunt-generated directories from run-all discovery
		"--terragrunt-exclude-dir", "**/.terragrunt-stack/**",
	}

	cmd := exec.CommandContext(ctx, "terragrunt", args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// terraformOutput represents a single terraform output value
type terraformOutput struct {
	Value interface{} `json:"value"`
	Type  interface{} `json:"type"`
}

// Output gets outputs from a specific unit
func (t *TerragruntRunner) Output(ctx context.Context, path string) (map[string]string, error) {
	workDir := filepath.Join(t.GetWorkingDir(), path)

	cmd := exec.CommandContext(ctx, "terragrunt", "output", "-json")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("output failed: %w\n\n%s", err, string(output))
	}

	// Terragrunt may emit log lines before the JSON — find the JSON object
	raw := string(output)
	jsonStart := strings.Index(raw, "{")
	if jsonStart < 0 {
		return nil, fmt.Errorf("no JSON found in output:\n%s", raw)
	}

	var parsed map[string]terraformOutput
	if err := json.Unmarshal([]byte(raw[jsonStart:]), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse output JSON: %w\n\n%s", err, raw)
	}

	result := make(map[string]string, len(parsed))
	for key, out := range parsed {
		switch v := out.Value.(type) {
		case string:
			result[key] = v
		default:
			b, _ := json.Marshal(v)
			result[key] = string(b)
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

		// Skip generated Terragrunt cache and stack expansion directories
		if info.IsDir() && (strings.Contains(path, ".terragrunt-cache") || strings.Contains(path, ".terragrunt-stack")) {
			return filepath.SkipDir
		}

		if info.Name() == "terragrunt.hcl" && !strings.Contains(path, ".terragrunt-cache") && !strings.Contains(path, ".terragrunt-stack") {
			relPath, _ := filepath.Rel(workDir, filepath.Dir(path))
			if relPath != "." {
				units = append(units, relPath)
			}
		}
		return nil
	})

	return units, err
}

// Validate runs terragrunt validate with live output streaming
func (t *TerragruntRunner) Validate(ctx context.Context, path string) error {
	workDir := filepath.Join(t.GetWorkingDir(), path)

	cmd := exec.CommandContext(ctx, "terragrunt", "validate")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("validate failed: %w", err)
	}
	return nil
}

// lastLines returns the last n lines from s, useful for error context
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return "...\n" + strings.Join(lines[len(lines)-n:], "\n")
}
