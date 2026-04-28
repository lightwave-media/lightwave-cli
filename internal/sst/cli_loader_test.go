package sst

import (
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot resolves the workspace root from the test file's location.
// This file lives at packages/lightwave-cli/internal/sst/cli_loader_test.go;
// the workspace root is four directories up.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
}

func TestLoadCLIConfig_RoundTripsActualFile(t *testing.T) {
	cfg, err := LoadCLIConfig(repoRoot(t))
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
	cfg, err := LoadCLIConfig(repoRoot(t))
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
