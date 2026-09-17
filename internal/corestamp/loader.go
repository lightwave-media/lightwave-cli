// Package corestamp is a vendored copy of lightwave-core's Go binding for the
// SST (ADR-0003, Wave 1). It is a thin loader over the embedded schema YAML — no
// business logic, no codegen, no validation beyond a YAML parse.
//
// Why vendored rather than imported: lightwave-cli is public and lightwave-core
// is private. Depending on github.com/lightwave-media/lightwave-core/bindings/go
// breaks `go build`/`go install` for anyone outside the org and forces a
// private-module token into every CI job. Copying the binding in keeps the
// public build credential-free.
//
// The cost is that this is a snapshot: it can lag the canonical stamp. Refresh
// with scripts/sync-core-stamp.sh, which also rewrites Version below.
package corestamp

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// schemaRoot is the embed prefix; bindings/go/schemas mirrors src/schemas.
const schemaRoot = "schemas"

// Version is the lightwave-core release this binding embeds, as that release
// declares itself (bindings/go/loader.go at SourceTag).
//
// SourceTag is the git ref the mirror was extracted from. Version and SourceTag
// are read from independent places by scripts/sync-core-stamp.sh, which is the
// point: comparing them catches a tag published without its version bump. That
// is a live defect — bindings/go/v0.6.5 carries a tree declaring 0.6.4
// (lightwave-core#552) — and the previous guard could not see it, because it
// compared pyproject.toml against loader.go, two values one hand bumps together.
//
// SchemasSHA256 digests the embedded tree so a hand-edit under schemas/ is
// detectable without a lightwave-core checkout. All three are rewritten by
// scripts/sync-core-stamp.sh; do not edit them by hand.
const (
	Version       = "0.8.0"
	SourceTag     = "bindings/go/v0.8.0"
	SchemasSHA256 = "ac01a72d4f5c2a87a4f454a05d7d1f54b65c313bb2deda25f69f83e0b77b3737"
)

// indexFile is the registry index name (present at each tree level).
const indexFile = "__index.yaml"

// ReadSchema returns the raw YAML bytes for a registered schema. Prefer this
// when consumers need mapping key order preserved (e.g. commands.yaml domains).
func ReadSchema(name string) ([]byte, error) {
	raw, err := schemaFS.ReadFile(schemaRoot + "/" + name + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("lightwave-core: schema %q not found: %w", name, err)
	}
	return raw, nil
}

// LoadSchema returns the parsed schema registered under name — the registry key,
// i.e. the path under src/schemas without the .yaml suffix
// (e.g. "data/agile_artifacts/prd", "policy/validity/core-self").
func LoadSchema(name string) (map[string]any, error) {
	raw, err := ReadSchema(name)
	if err != nil {
		return nil, err
	}

	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("lightwave-core: parsing schema %q: %w", name, err)
	}

	return doc, nil
}

// ListSchemas enumerates every registered schema key, sorted. The __index.yaml
// registry files are excluded — they are the index, not schemas.
func ListSchemas() ([]string, error) {
	var keys []string

	err := fs.WalkDir(schemaFS, schemaRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}

		if d.Name() == indexFile {
			return nil
		}

		key := strings.TrimPrefix(path, schemaRoot+"/")
		keys = append(keys, strings.TrimSuffix(key, ".yaml"))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("lightwave-core: listing schemas: %w", err)
	}

	sort.Strings(keys)

	return keys, nil
}

// LoadIndex returns the master __index.yaml registry tree.
func LoadIndex() (map[string]any, error) {
	raw, err := schemaFS.ReadFile(schemaRoot + "/" + indexFile)
	if err != nil {
		return nil, fmt.Errorf("lightwave-core: %s not found: %w", indexFile, err)
	}

	var idx map[string]any
	if err := yaml.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("lightwave-core: parsing %s: %w", indexFile, err)
	}

	return idx, nil
}
