// Package gogen generates Go structs and PostgreSQL DDL from SST entity
// YAML schemas declared in lightwave-core/src/schemas/data/.
package gogen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// EntitySchema holds the parsed SST entity YAML (table_kind: entity).
type EntitySchema struct {
	Meta struct {
		Version   string `yaml:"version"`
		SchemaID  string `yaml:"schema_id"`
		Title     string `yaml:"title"`
		TableKind string `yaml:"table_kind"`
		TableName string `yaml:"table_name"`
	} `yaml:"_meta"`
	PrimaryKey struct {
		Column string `yaml:"column"`
		Type   string `yaml:"type"`
	} `yaml:"primary_key"`
	NaturalKey struct {
		Column string `yaml:"column"`
		Unique bool   `yaml:"unique"`
	} `yaml:"natural_key"`
	RequiredFields []FieldDef `yaml:"required_fields"`
	OptionalFields []FieldDef `yaml:"optional_fields"`
	Relations      Relations  `yaml:"relations"`
}

// FieldDef is a single field entry from required_fields / optional_fields.
// Fields are ordered largest-first to minimise struct padding.
type FieldDef struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
	Storage    string `yaml:"storage"`
	FKRef      string `yaml:"fk_ref"`
	FKColumn   string `yaml:"fk_column"`
	ColumnType string `yaml:"column_type"`
	Indexed    bool   `yaml:"indexed"`
}

// Relations holds the parent/children FK metadata.
type Relations struct {
	Parent   string `yaml:"parent"`
	ParentFK struct {
		Column     string `yaml:"column"`
		References string `yaml:"references"`
		OnDelete   string `yaml:"on_delete"`
	} `yaml:"parent_fk"`
	Children []string `yaml:"children"`
}

// Load parses one entity YAML file. Returns an error if the file is not
// an entity schema (table_kind != "entity").
func Load(path string) (*EntitySchema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var e EntitySchema

	if err := yaml.Unmarshal(data, &e); err != nil {
		return nil, err
	}

	if e.Meta.TableKind != "entity" {
		return nil, fmt.Errorf("%s: not an entity schema (table_kind=%q)", filepath.Base(path), e.Meta.TableKind)
	}

	return &e, nil
}

// FindEntities returns paths to all entity YAML files under dir.
// Non-entity files (wrong table_kind or parse errors) are silently skipped.
func FindEntities(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var out []string

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") || entry.Name() == "__index.yaml" {
			continue
		}

		p := filepath.Join(dir, entry.Name())

		if _, err := Load(p); err == nil {
			out = append(out, p)
		}
	}

	return out, nil
}

// referencedTables returns the distinct table names this entity's fields
// reference via fk_ref (resolved through TableFromFKRef), for FK-topological
// ordering of the migration.
func referencedTables(e *EntitySchema) []string {
	seen := map[string]bool{}

	var out []string

	for _, f := range append(e.RequiredFields, e.OptionalFields...) {
		if f.FKRef == "" {
			continue
		}

		t := TableFromFKRef(f.FKRef)
		if !seen[t] {
			seen[t] = true

			out = append(out, t)
		}
	}

	return out
}

// GoType maps an SST type string to a Go type string.
func GoType(sstType string) string {
	if strings.HasPrefix(sstType, "list[") {
		return "json.RawMessage"
	}

	switch sstType {
	case "str":
		return "string"
	case "int":
		return "int64"
	case "float":
		return "float64"
	case "bool":
		return "bool"
	case "date", "datetime":
		return "time.Time"
	case "uuid":
		return "uuid.UUID"
	case "object":
		return "json.RawMessage"
	default:
		return "string"
	}
}

// SQLType maps an SST type string to a PostgreSQL column type.
// column_type on the field takes precedence (e.g. "jsonb").
func SQLType(sstType, columnType string) string {
	if columnType != "" {
		return strings.ToUpper(columnType)
	}

	if strings.HasPrefix(sstType, "list[") {
		return "JSONB"
	}

	switch sstType {
	case "str":
		return "TEXT"
	case "int":
		return "BIGINT"
	case "float":
		return "FLOAT8"
	case "bool":
		return "BOOLEAN"
	case "date":
		return "DATE"
	case "datetime":
		return "TIMESTAMPTZ"
	case "uuid":
		return "UUID"
	case "object":
		return "JSONB"
	default:
		return "TEXT"
	}
}

// TableFromFKRef derives a table name from a schema ID such as
// "lightwave://schemas/data/agile_artifacts/user_story" → "user_stories".
// NOTE: handles -y→-ies only; irregular plurals (child→children) are not covered.
func TableFromFKRef(fkRef string) string {
	parts := strings.Split(strings.TrimRight(fkRef, "/"), "/")
	name := parts[len(parts)-1]

	if strings.HasSuffix(name, "y") {
		return name[:len(name)-1] + "ies"
	}

	return name + "s"
}

// CamelCase converts a snake_case / space- / hyphen-separated label into a Go
// identifier. It splits on any run of non-alphanumeric runes so multi-word
// _meta.title values ("API Specification", "Non-Functional Requirements")
// yield a valid identifier (APISpecification, NonFunctionalRequirements), not
// a string with embedded spaces that fails to compile (lightwave-cli#227).
func CamelCase(s string) string {
	var b strings.Builder

	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}

	return b.String()
}
