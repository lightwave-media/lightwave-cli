package corestamp

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSchema(t *testing.T) {
	raw, err := ReadSchema("data/ui/site_config")
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty schema bytes")
	}

	doc, err := LoadSchema("data/ui/site_config")
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}

	if len(doc) == 0 {
		t.Fatal("expected non-empty schema")
	}

	// Intentional: site_config is a stable sentinel that exercises the loader on
	// a real schema AND pins the R-6 invariant (every schema declares
	// required_fields). If site_config ever evolves, update this sentinel.
	if _, ok := doc["required_fields"]; !ok {
		t.Errorf("site_config should expose required_fields")
	}
}

func TestLoadSchema_NotFound(t *testing.T) {
	_, err := LoadSchema("data/does/not-exist")
	if err == nil {
		t.Fatal("expected error for a missing schema")
	}
	if strings.Count(err.Error(), "not found") != 1 {
		t.Fatalf("expected single not-found wrap, got: %v", err)
	}
}

func TestListSchemas(t *testing.T) {
	keys, err := ListSchemas()
	if err != nil {
		t.Fatalf("ListSchemas: %v", err)
	}

	if len(keys) == 0 {
		t.Fatal("expected at least one schema")
	}

	var found bool
	for _, k := range keys {
		if strings.HasSuffix(k, "__index") {
			t.Errorf("__index registry files must be excluded; found %q", k)
		}

		if k == "data/ui/site_config" {
			found = true
		}
	}

	if !found {
		t.Error("expected data/ui/site_config among listed schemas")
	}
}

func TestLoadIndex(t *testing.T) {
	idx, err := LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}

	if len(idx) == 0 {
		t.Fatal("expected a non-empty __index.yaml tree")
	}
}

// coreCheckout resolves the lightwave-core checkout the way lw does —
// LW_LIGHTWAVE_ROOT, then LW_DEV_ROOT, then the flat ~/dev sibling layout.
// The names match viper.BindEnv in internal/config; a different spelling would
// fail silently back to the default, which is how you get a plausible wrong
// answer with no error.
func coreCheckout(t *testing.T) string {
	t.Helper()

	root := os.Getenv("LW_LIGHTWAVE_ROOT")
	if root == "" {
		root = os.Getenv("LW_DEV_ROOT")
	}

	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}

		root = filepath.Join(home, "dev")
	}

	core := filepath.Join(root, "lightwave-core")
	if _, err := os.Stat(filepath.Join(core, ".git")); err != nil {
		return ""
	}

	return core
}

// TestNoDrift fails if the vendored mirror has fallen out of sync with the
// lightwave-core tree it claims to come from.
//
// Content is read from SourceTag via `git archive`, never from core's working
// tree: that tree is whatever branch another agent has out, uncommitted changes
// included, and reading it once embedded an unrelated session's in-flight state
// (lightwave-cli#383).
//
// It skips only when there is no lightwave-core checkout to compare against —
// the genuine "cannot know" case, which is normal in this public repo's CI since
// lightwave-core is private. That is the ONLY sanctioned skip here: the
// checkout-free half of the guarantee (TestVerifyEmbeddedDigest) always runs, so
// a hand-edited mirror is caught with or without core.
func TestNoDrift(t *testing.T) {
	core := coreCheckout(t)
	if core == "" {
		t.Skip("no lightwave-core checkout reachable via LW_LIGHTWAVE_ROOT/LW_DEV_ROOT/~/dev; " +
			"digest guard (TestVerifyEmbeddedDigest) covers the in-repo half")
	}

	if err := exec.Command("git", "-C", core, "rev-parse", "--verify", "--quiet",
		SourceTag+"^{commit}").Run(); err != nil {
		t.Skipf("SourceTag %q does not resolve in %s (shallow clone or missing tags)", SourceTag, core)
	}

	// Releases up to bindings/go/v0.8.0 mirrored the Go binding's copy; core
	// retired the binding, and from v0.9.0 src/schemas is the only copy. The
	// same per-ref choice scripts/sync-core-stamp.sh makes, so the guard
	// compares against what the script actually extracted.
	subtree := "src/schemas"
	if exec.Command("git", "-C", core, "cat-file", "-e", SourceTag+":bindings/go/schemas").Run() == nil {
		subtree = "bindings/go/schemas"
	}

	archived, err := exec.Command("git", "-C", core, "archive", SourceTag, "--", subtree).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("git archive %s: %v: %s", SourceTag, err, exitErr.Stderr)
		}

		t.Fatalf("git archive %s: %v", SourceTag, err)
	}

	want := map[string][]byte{}
	reader := tar.NewReader(bytes.NewReader(archived))

	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}

		if nextErr != nil {
			t.Fatalf("reading archive of %s: %v", SourceTag, nextErr)
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		body, readErr := io.ReadAll(reader)
		if readErr != nil {
			t.Fatalf("reading %s from archive: %v", header.Name, readErr)
		}

		want[strings.TrimPrefix(header.Name, subtree+"/")] = body
	}

	if len(want) == 0 {
		t.Fatalf("%s:%s is empty — wrong ref or wrong subtree", SourceTag, subtree)
	}

	got := map[string][]byte{}
	if err := fs.WalkDir(schemaFS, schemaRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}

		b, readErr := schemaFS.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		got[strings.TrimPrefix(path, schemaRoot+"/")] = b

		return nil
	}); err != nil {
		t.Fatalf("walking embedded: %v", err)
	}

	if len(want) != len(got) {
		t.Errorf("file count drift vs %s: canonical %d, vendored %d — run scripts/sync-core-stamp.sh",
			SourceTag, len(want), len(got))
	}

	for name, wb := range want {
		gb, ok := got[name]
		if !ok {
			t.Errorf("vendored mirror missing %s — run scripts/sync-core-stamp.sh", name)

			continue
		}

		if !bytes.Equal(wb, gb) {
			t.Errorf("vendored %s differs from %s — run scripts/sync-core-stamp.sh", name, SourceTag)
		}
	}

	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("vendored mirror has %s, absent from %s — run scripts/sync-core-stamp.sh", name, SourceTag)
		}
	}
}
