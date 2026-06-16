// Package zodgen emits Zod TypeScript from lightwave-core's data/ui schema
// family and ui_* enum stamps. It is the Run 2 implementation of the
// generates: declarations stamped in Run 1 (lightwave-core PR #124/#133);
// the enforcement checklist lives in lightwave-cli#77 and ADR-0006.
package zodgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FieldDecl is one entry in required_fields/optional_fields or a section
// instance's props list. The same shape serves both because the SST uses one
// field-declaration grammar everywhere.
type FieldDecl struct {
	Default     any      `yaml:"default,omitempty"`
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Description string   `yaml:"description,omitempty"`
	ValuesRef   string   `yaml:"values_ref,omitempty"`
	Options     []string `yaml:"options,omitempty"`
	Nullable    bool     `yaml:"nullable,omitempty"`
	Required    bool     `yaml:"required,omitempty"`
}

// SubField is one field inside a sub_schemas block (keyed by field name).
type SubField struct {
	Default     any      `yaml:"default,omitempty"`
	Type        string   `yaml:"type"`
	Description string   `yaml:"description,omitempty"`
	ValuesRef   string   `yaml:"values_ref,omitempty"`
	Options     []string `yaml:"options,omitempty"`
	Nullable    bool     `yaml:"nullable,omitempty"`
}

// Meta is the _meta block shared by every SST schema file.
type Meta struct {
	Version   string   `yaml:"version"`
	SchemaID  string   `yaml:"schema_id"`
	Title     string   `yaml:"title"`
	Generates []string `yaml:"generates"`
}

// Schema is a data/ui contract file (component_contract, section_contract, …).
type Schema struct {
	Meta           Meta                           `yaml:"_meta"`
	RequiredFields []FieldDecl                    `yaml:"required_fields"`
	OptionalFields []FieldDecl                    `yaml:"optional_fields"`
	SubSchemas     map[string]map[string]SubField `yaml:"sub_schemas"`
	Example        yaml.Node                      `yaml:"example"`
}

// EnumOption is one option entry in a data/enums stamp.
type EnumOption struct {
	Value       string `yaml:"value"`
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
}

// EnumStamp is a data/enums/*.yaml file.
type EnumStamp struct {
	Name    string       `yaml:"name"`
	Default string       `yaml:"default"`
	Meta    Meta         `yaml:"_meta"`
	Options []EnumOption `yaml:"options"`
	Closed  bool         `yaml:"closed"`
}

// SectionInstance is a concrete section contract instance — today sourced
// from section_contract.yaml's example block (the Run 2 round-trip fixture);
// in Run 3+ from instance files under data/ui/sections/.
type SectionInstance struct {
	PropTypes map[string][]FieldDecl `yaml:"prop_types"`
	Key       string                 `yaml:"key"`
	Family    string                 `yaml:"family"`
	Variant   string                 `yaml:"variant"`
	Title     string                 `yaml:"title"`
	Props     []FieldDecl            `yaml:"props"`
}

// LoadSchema parses one data/ui contract file.
func LoadSchema(path string) (*Schema, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading schema: %w", err)
	}

	var s Schema
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
	}

	return &s, nil
}

// LoadEnums parses every ui_* enum stamp in dir, keyed by stamp name.
func LoadEnums(dir string) (map[string]*EnumStamp, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "ui_*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("globbing enums: %w", err)
	}

	enums := make(map[string]*EnumStamp, len(matches))
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		if err != nil {
			return nil, fmt.Errorf("reading enum stamp: %w", err)
		}

		var e EnumStamp
		if err := yaml.Unmarshal(raw, &e); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", filepath.Base(m), err)
		}

		if e.Name == "" {
			return nil, fmt.Errorf("%s: enum stamp has no name", filepath.Base(m))
		}

		enums[e.Name] = &e
	}

	return enums, nil
}

// SectionInstanceFromExample decodes a section contract's example block into
// a SectionInstance. The example is the stamped round-trip fixture.
func SectionInstanceFromExample(s *Schema) (*SectionInstance, error) {
	if s.Example.IsZero() {
		return nil, fmt.Errorf("schema %s has no example block", s.Meta.SchemaID)
	}

	var inst SectionInstance
	if err := s.Example.Decode(&inst); err != nil {
		return nil, fmt.Errorf("decoding example of %s: %w", s.Meta.SchemaID, err)
	}

	if inst.Key == "" {
		return nil, fmt.Errorf("example of %s has no key", s.Meta.SchemaID)
	}

	return &inst, nil
}

// TSName extracts the TypeScript binding name from a generates: list
// ("typescript:ComponentCategory" → "ComponentCategory"). Empty when the
// schema declares no typescript target — callers skip emission then.
func TSName(generates []string) string {
	for _, g := range generates {
		if rest, ok := strings.CutPrefix(g, "typescript:"); ok {
			return rest
		}
	}

	return ""
}

// sortedKeys returns map keys in stable order; YAML maps lose author order
// and generated output must be deterministic for golden tests and diffs.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
