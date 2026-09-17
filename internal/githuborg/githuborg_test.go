package githuborg

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveBootstrapScript pins the self-checkout layout: a CI runner (or
// any lightwaveRoot) that has ONLY lightwave-cli checked out, not a sibling
// lightwave-infrastructure-catalog. BootstrapScriptRel pointed at the sibling
// path until this test was added, and the bug shipped invisibly because a
// leftover script at ~/dev/lightwave-infrastructure-catalog/scripts/ on a real
// dev machine resolved fine — a temp dir catches what a real ~/dev cannot.
func TestResolveBootstrapScript(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	scriptPath := filepath.Join(root, BootstrapScriptRel)
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatalf("write fixture script: %v", err)
	}

	path, err := ResolveBootstrapScript(root)
	if err != nil {
		t.Fatalf("ResolveBootstrapScript did not find the self-checkout script: %v", err)
	}
	if path != scriptPath {
		t.Fatalf("expected %s, got %s", scriptPath, path)
	}
}

func TestResolveBootstrapScript_MissingScriptErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := ResolveBootstrapScript(root); err == nil {
		t.Fatal("expected an error when the bootstrap script is absent")
	}
}
