package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestComposeEnv(t *testing.T) {
	tests := []struct {
		name    string
		flags   map[string]any
		want    string
		wantErr bool
	}{
		{"default to local", map[string]any{}, "local", false},
		{"nil flags default to local", nil, "local", false},
		{"explicit local", map[string]any{"env": "local"}, "local", false},
		{"explicit staging", map[string]any{"env": "staging"}, "staging", false},
		{"explicit production", map[string]any{"env": "production"}, "production", false},
		{"empty string defaults to local", map[string]any{"env": ""}, "local", false},
		{"unknown env rejected", map[string]any{"env": "dev"}, "", true},
		{"typo rejected", map[string]any{"env": "prod"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := composeEnv(tt.flags)
			if (err != nil) != tt.wantErr {
				t.Fatalf("composeEnv() err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("composeEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComposeFilesEqual(t *testing.T) {
	dir := t.TempDir()

	a := filepath.Join(dir, "a.yml")
	b := filepath.Join(dir, "b.yml")
	c := filepath.Join(dir, "c.yml")

	if err := os.WriteFile(a, []byte("services:\n  web:\n    image: foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("services:\n  web:\n    image: foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c, []byte("services:\n  web:\n    image: bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := composeFilesEqual(a, b); err != nil {
		t.Errorf("equal files: composeFilesEqual returned %v, want nil", err)
	}

	err := composeFilesEqual(a, c)
	if !errors.Is(err, errComposeDrift) {
		t.Errorf("differing files: composeFilesEqual returned %v, want errComposeDrift", err)
	}

	err = composeFilesEqual(filepath.Join(dir, "missing.yml"), a)
	if err == nil || errors.Is(err, errComposeDrift) {
		t.Errorf("missing-A: composeFilesEqual returned %v, want non-nil non-drift error", err)
	}

	err = composeFilesEqual(a, filepath.Join(dir, "missing.yml"))
	if err == nil || errors.Is(err, errComposeDrift) {
		t.Errorf("missing-B: composeFilesEqual returned %v, want non-nil non-drift error", err)
	}
}

func TestComposeHandlersRegistered(t *testing.T) {
	for _, key := range []string{"compose.generate", "compose.verify", "check.compose"} {
		if _, ok := LookupHandler(key); !ok {
			t.Errorf("handler %q not registered", key)
		}
	}
}
