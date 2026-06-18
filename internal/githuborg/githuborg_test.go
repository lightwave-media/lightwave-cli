package githuborg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBootstrapScript(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	root := filepath.Join(home, "dev")
	path, err := ResolveBootstrapScript(root)
	if err != nil {
		t.Skip("bootstrap script not present in dev tree:", err)
	}
	if path == "" {
		t.Fatal("expected path")
	}
}
