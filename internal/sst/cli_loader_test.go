package sst

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot resolves the workspace root from the test file's location.
// This file lives at lightwave-cli/internal/sst/cli_loader_test.go;
// the workspace root is three directories up — assuming the flat
// sibling-repo layout (~/dev/{lightwave-cli,lightwave-core}).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// skipIfNoLightwaveCore skips the test when the sibling lightwave-core
// repo isn't checked out at the expected path (CI runs lightwave-cli
// stand-alone without the workspace layout). This is the historical
// reason CI's Tests job has been red — predates the gruntwork-harden
// mission, surfaced when PR1's golangci-lint config exposed an
// otherwise-unreliable signal.
func skipIfNoLightwaveCore(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(CLIConfigPath(root)); os.IsNotExist(err) {
		t.Skipf("lightwave-core schema not present at %s; skipping integration test", CLIConfigPath(root))
	}
}

func TestLoadCLIConfig_RoundTripsActualFile(t *testing.T) {
	root := repoRoot(t)
	skipIfNoLightwaveCore(t, root)
	cfg, err := LoadCLIConfig(root)
	if err != nil {
		t.Fatalf("LoadCLIConfig: %v", err)
	}

	if cfg.Version == "" {
		t.Fatal("expected version")
	}
	if len(cfg.Domains) == 0 {
		t.Fatal("expected at least one domain")
	}

	// Sanity check: task domain must exist with `create` command.
	task := cfg.FindDomain("task")
	if task == nil {
		t.Fatal("task domain missing")
	}
	var foundCreate bool
	for _, cmd := range task.Commands {
		if cmd.Name == "create" {
			foundCreate = true
			break
		}
	}
	if !foundCreate {
		t.Error("task.create missing")
	}
}

func TestCLIConfig_IndexAndKeysAlign(t *testing.T) {
	root := repoRoot(t)
	skipIfNoLightwaveCore(t, root)
	cfg, err := LoadCLIConfig(root)
	if err != nil {
		t.Fatalf("LoadCLIConfig: %v", err)
	}

	idx := cfg.Index()
	keys := cfg.Keys()

	if len(idx) != len(keys) {
		t.Fatalf("index size %d != keys length %d", len(idx), len(keys))
	}
	for _, k := range keys {
		if _, ok := idx[k]; !ok {
			t.Errorf("key %q in Keys() but missing from Index()", k)
		}
	}
}

func TestCLIConfig_RejectsDuplicateDomain(t *testing.T) {
	cfg := &CLIConfig{
		Version: "1.0.0",
		Domains: []CLIDomain{
			{Name: "task", Commands: []CLICommand{{Name: "list", Description: "x"}}},
			{Name: "task", Commands: []CLICommand{{Name: "show", Description: "x"}}},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected duplicate-domain error")
	}
}

func TestCLIConfig_RejectsMissingDescription(t *testing.T) {
	cfg := &CLIConfig{
		Version: "1.0.0",
		Domains: []CLIDomain{
			{Name: "task", Commands: []CLICommand{{Name: "list"}}},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing-description error")
	}
}

func TestCommandKey(t *testing.T) {
	if got := CommandKey("task", "list"); got != "task.list" {
		t.Errorf("CommandKey: got %q want %q", got, "task.list")
	}
}
