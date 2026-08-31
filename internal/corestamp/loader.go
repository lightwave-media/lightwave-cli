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

// Version is the lightwave-core release this binding embeds. The release train
// keeps it in lockstep with pyproject.toml#version — one git tag stamps every
// binding, so a consumer verifies SST alignment with a single comparison.
// Update this together with pyproject.toml#version; TestVersionLockstep enforces
// the match in CI.
const Version = "0.6.4"

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
