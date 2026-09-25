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
	"sort"
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/secrets"
)

// cloudflareTokenKey is the one store key terragrunt needs beyond AWS
// credentials: root.hcl generates the Cloudflare provider into every unit, and
// Terraform configures it only for units with Cloudflare resources.
const cloudflareTokenKey = "CLOUDFLARE_API_TOKEN"

// fetchSecret reads one store key by name. A seam for tests.
var fetchSecret = secrets.FetchOne

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

// GetWorkingDir returns the working directory for the environment.
// infraRoot is the lightwave-infrastructure-live checkout, whose layout
// is <env>/<region>/<unit> directly under the repo root.
func (t *TerragruntRunner) GetWorkingDir() string {
	return filepath.Join(t.infraRoot, t.env, t.region)
}

// Plan runs terragrunt plan for a specific unit/stack.
// Output streams to terminal in real-time and is captured for change detection.
func (t *TerragruntRunner) Plan(ctx context.Context, path string) (*PlanResult, error) {
	workDir := t.ResolveUnitDir(path)

	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("path does not exist: %s", workDir)
	}

	cmd := exec.CommandContext(ctx, "terragrunt", "plan", "-no-color")
	cmd.Dir = workDir
	cmd.Env = terragruntEnv(ctx, workDir, false)

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
	workDir := t.ResolveUnitDir(path)

	args := []string{"apply", "-no-color"}
	if autoApprove {
		args = append(args, "-auto-approve")
	}

	cmd := exec.CommandContext(ctx, "terragrunt", args...)
	cmd.Dir = workDir
	cmd.Env = terragruntEnv(ctx, workDir, false)
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
	cmd.Env = terragruntEnv(ctx, workDir, true)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// terragruntEnv is the environment for a terragrunt plan or apply in dir. When
// the run can reach Cloudflare and the caller did not supply the token, it is
// read from SSM by name and given to terragrunt alone, never to lw's own
// environment: sessions no longer carry store keys (CLAUDE.md §24). If it
// cannot be read, the run goes ahead without it and says so by name, because
// only Cloudflare resources need it.
func terragruntEnv(ctx context.Context, dir string, wholeTree bool) []string {
	env := append(os.Environ(), "TF_IN_AUTOMATION=1")
	if os.Getenv(cloudflareTokenKey) != "" || (!wholeTree && !usesCloudflare(dir)) {
		return env
	}

	token, err := fetchSecret(ctx, cloudflareTokenKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lw infra: %s is not set and could not be read from SSM (%v); Cloudflare resources will fail\n", cloudflareTokenKey, err)

		return env
	}

	return append(env, cloudflareTokenKey+"="+token)
}

// usesCloudflare reports whether a unit's terragrunt.hcl mentions Cloudflare,
// which every Cloudflare unit's module source and header do.
func usesCloudflare(dir string) bool {
	body, err := os.ReadFile(filepath.Join(dir, "terragrunt.hcl"))

	return err == nil && bytes.Contains(bytes.ToLower(body), []byte("cloudflare"))
}

// terraformOutput represents a single terraform output value
type terraformOutput struct {
	Value interface{} `json:"value"`
	Type  interface{} `json:"type"`
}

// Output gets outputs from a specific unit
func (t *TerragruntRunner) Output(ctx context.Context, path string) (map[string]string, error) {
	workDir := t.ResolveUnitDir(path)

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

// ListUnits returns every unit the infrastructure repo tracks, as
// <env>/<region>/<unit> paths, sorted.
//
// It used to walk the filesystem from <root>/<env>/<region>, with env and
// region fixed at prod / us-east-1 because nothing registered the flags that
// would have changed them (#367). Everything outside that one directory was
// invisible: on the live repo it returned the 9 units under prod/us-east-1 and
// none of the 11 under prod/us-west-2, so `lw infra list` answered "these are
// the units" while omitting more than half of them. The Cloudflare diagnosis
// that filed the issue had to fall back to raw `aws s3 ls`.
//
// Enumerating from `git ls-files` rather than walking the tree is deliberate,
// and not only for speed:
//
//   - A walk of the repo root descends into nested worktrees. The live
//     lightwave-infrastructure-live checkout has 20 terragrunt.hcl files under
//     .claude/worktrees — another session's branch — which a walk would report
//     as units of this one. That is exactly the failure #404 had to be reverted
//     for, and here it is pre-existing rather than hypothetical.
//   - .terragrunt-cache and .terragrunt-stack are generated and untracked, so
//     they drop out by construction instead of by a string-match skip list that
//     has to keep guessing at directory names. A plain `find` over the live repo
//     counts 82 and 78 "units" in the two regions against the real 9 and 11.
//
// The returned paths are repo-root-relative so they can be handed straight back
// to plan/apply. A bare unit name would be ambiguous the moment two regions
// contain the same unit, which is the normal case here.
func (t *TerragruntRunner) ListUnits(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx,
		"git", "-C", t.infraRoot, "ls-files", "-z", "*terragrunt.hcl").Output()
	if err != nil {
		return nil, fmt.Errorf("listing tracked units in %s: %w", t.infraRoot, err)
	}

	var units []string

	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" {
			continue
		}

		dir := filepath.Dir(f)
		if dir == "." {
			continue // the repo-root terragrunt.hcl is config, not a unit
		}

		units = append(units, dir)
	}

	sort.Strings(units)

	return units, nil
}

// ResolveUnitDir turns a user-supplied unit path into an absolute directory.
//
// ListUnits now prints <env>/<region>/<unit>, so that form has to work. The
// bare <unit> form predates this change and still appears in scripts, so it is
// resolved against the runner's env/region rather than broken. Returning the
// repo-root form first means an explicit path always wins over the default.
func (t *TerragruntRunner) ResolveUnitDir(path string) string {
	if hasTerragrunt(filepath.Join(t.infraRoot, path)) {
		return filepath.Join(t.infraRoot, path)
	}

	return filepath.Join(t.GetWorkingDir(), path)
}

func hasTerragrunt(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "terragrunt.hcl"))

	return err == nil
}

// Validate runs terragrunt validate with live output streaming
func (t *TerragruntRunner) Validate(ctx context.Context, path string) error {
	workDir := t.ResolveUnitDir(path)

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
