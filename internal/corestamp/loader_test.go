package corestamp

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// TestNoDrift fails if the vendored mirror (bindings/go/schemas) has fallen out
// of sync with the canonical src/schemas. It runs in the lightwave-core repo
// CI; it skips when the canonical tree isn't reachable (e.g. a consumer running
// the module from their go module cache).
func TestNoDrift(t *testing.T) {
	canonical := filepath.Join("..", "..", "src", "schemas")
	if _, err := os.Stat(canonical); err != nil {
		t.Skip("canonical src/schemas not reachable; drift guard runs in lightwave-core CI")
	}

	// Compare EVERY file (not just .yaml) so the byte-for-byte guarantee holds
	// for .gitkeep and any non-yaml asset that lands under src/schemas.
	want := map[string][]byte{}
	if err := filepath.WalkDir(canonical, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		rel, relErr := filepath.Rel(canonical, path)
		if relErr != nil {
			return relErr
		}

		want[filepath.ToSlash(rel)] = b

		return nil
	}); err != nil {
		t.Fatalf("walking canonical: %v", err)
	}

	got := map[string][]byte{}
	if err := fs.WalkDir(schemaFS, schemaRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
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
		t.Fatalf("file count drift: canonical %d, vendored %d — run scripts/sync-go-binding.sh", len(want), len(got))
	}

	for name, wb := range want {
		gb, ok := got[name]
		if !ok {
			t.Errorf("vendored mirror missing %s — run scripts/sync-go-binding.sh", name)

			continue
		}

		if !bytes.Equal(wb, gb) {
			t.Errorf("vendored %s differs from canonical — run scripts/sync-go-binding.sh", name)
		}
	}
}

// TestVersionLockstep enforces the release-train rule: the binding's Version
// must equal pyproject.toml#version, so one git tag stamps every binding.
func TestVersionLockstep(t *testing.T) {
	pyproject := filepath.Join("..", "..", "pyproject.toml")

	raw, err := os.ReadFile(pyproject)
	if err != nil {
		t.Skip("pyproject.toml not reachable; lockstep guard runs in lightwave-core CI")
	}

	// Anchor to the [project] table so a `version = "…"` in another table
	// (e.g. a pinned dep block above [project]) can't shadow the package version.
	proj := regexp.MustCompile(`(?ms)^\[project\]\s*$(.*?)(?:^\[|\z)`).FindSubmatch(raw)
	if proj == nil {
		t.Fatal("could not find [project] table in pyproject.toml")
	}

	m := regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`).FindSubmatch(proj[1])
	if m == nil {
		t.Fatal("could not find version in [project] table")
	}

	if got := string(m[1]); got != Version {
		t.Errorf("version drift: pyproject.toml is %q but binding Version is %q — keep them in lockstep (release train)", got, Version)
	}
}
