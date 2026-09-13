// Package gogen generates Go structs and PostgreSQL DDL from SST entity
// YAML schemas declared in lightwave-core/src/schemas/data/.
package gogen

import (
	"context"
	"fmt"
	"os"
	"path"
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
		Scope     string `yaml:"scope"`
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

// Tabled reports whether this schema materialises as a table.
//
// `entity` is a record with its own lifecycle. `document` is content whose
// truth is a FILE — an ADR under spec/, a runbook body — and it gets a table
// too, but that table INDEXES the files: the row carries source_path and the
// file remains authoritative. That distinction is ADR-0049's, and collapsing
// the two is how a derived store quietly becomes a second truth.
//
// `map` and `value_object` never produce tables.
func (e *EntitySchema) Tabled() bool {
	return e.Meta.TableKind == KindEntity || e.Meta.TableKind == KindDocument
}

// IsDocument reports whether the table indexes files rather than owning rows.
func (e *EntitySchema) IsDocument() bool { return e.Meta.TableKind == KindDocument }

// Table kinds, bound to data/enums/table_kinds.yaml in lightwave-core.
const (
	KindEntity      = "entity"
	KindDocument    = "document"
	KindMap         = "map"
	KindValueObject = "value_object"
)

// Storage scopes, bound to data/enums/storage_scopes.yaml.
const (
	ScopePlatform = "platform"
	ScopeLocal    = "local"
	ScopeBoth     = "both"
)

// InScope reports whether this schema belongs in the store named by want.
// An empty want accepts everything, which is what preserves the pre-scope
// behaviour for callers that do not ask.
//
// A schema with no declared scope is treated as `platform`: that matches the
// enum's own default and means adding a scope filter cannot silently pull an
// undeclared schema onto a workstation.
func (e *EntitySchema) InScope(want string) bool {
	if want == "" {
		return true
	}

	got := e.Meta.Scope
	if got == "" {
		got = ScopePlatform
	}

	return got == want || got == ScopeBoth
}

// Load parses one schema. Returns an error if it does not materialise as a
// table, so callers can treat "not a table" and "unreadable" distinctly.
func Load(path string) (*EntitySchema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return Parse(filepath.Base(path), data)
}

// Parse is Load without the filesystem, so a Source can supply the bytes.
func Parse(name string, data []byte) (*EntitySchema, error) {
	var e EntitySchema

	if err := yaml.Unmarshal(data, &e); err != nil {
		return nil, err
	}

	if !e.Tabled() {
		return nil, fmt.Errorf("%s: not a tabled schema (table_kind=%q)", name, e.Meta.TableKind)
	}

	// Derive the table name when the schema does not declare one.
	//
	// Every agile_artifacts schema carries an explicit `table_name`, because
	// that family was hand-annotated when the convention was invented — so
	// nothing ever exercised the absent case. Pointing the generator at the
	// newly-declared families surfaced it immediately: 64 entities resolved,
	// one table was emitted, and a Go file was written to disk named ".go".
	// An empty table name is not a validation error anywhere downstream; it
	// just produces DDL for a table called "" and collides every entity into
	// one map key.
	//
	// TableFromFKRef already performs exactly this schema_id -> table
	// derivation for foreign keys, so reusing it keeps one pluralisation rule
	// rather than a second that can disagree with the first.
	if e.Meta.TableName == "" {
		if e.Meta.SchemaID == "" {
			// Nothing to derive from. TableFromFKRef would happily return "s"
			// for an empty id — a valid identifier, so every such schema would
			// collide into one table named `s` and the DDL would look fine.
			// Refuse instead: a name nobody chose is worse than an error.
			return nil, fmt.Errorf(
				"%s: tabled schema has neither _meta.table_name nor _meta.schema_id "+
					"to derive one from", name)
		}

		e.Meta.TableName = TableFromFKRef(e.Meta.SchemaID)
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

// LoadFrom resolves every tabled schema under dir from src, filtered by scope.
//
// Unlike FindEntities this reports what it REJECTED and why. The old path
// silently skipped anything that failed to parse as an entity, so an
// undeclared schema, a deliberate value_object and a YAML syntax error were
// one indistinguishable outcome — nothing. That is why 109 entity-shaped
// schemas sat undeclared without ever producing a signal.
func LoadFrom(ctx context.Context, src Source, dir, scope string) (entities []*EntitySchema, skipped []string, err error) {
	paths, err := src.List(ctx, dir)
	if err != nil {
		return nil, nil, fmt.Errorf("listing %s: %w", dir, err)
	}

	for _, p := range paths {
		data, readErr := src.Read(ctx, p)
		if readErr != nil {
			return nil, nil, readErr
		}

		e, parseErr := Parse(path.Base(p), data)
		if parseErr != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", p, parseErr))
			continue
		}

		if !e.InScope(scope) {
			skipped = append(skipped, fmt.Sprintf("%s: scope=%s, wanted %s", p, e.Meta.Scope, scope))
			continue
		}

		entities = append(entities, e)
	}

	return entities, skipped, nil
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

	// -y pluralises to -ies only after a CONSONANT. After a vowel it takes a
	// plain -s: day -> days, not daies. The rule was consonant-blind because
	// every name it had ever seen (story, category, identity) happened to end
	// in consonant+y; pointing the generator at data/cineos produced
	// `shoot_daies` on the first run.
	if strings.HasSuffix(name, "y") && len(name) > 1 && !isVowel(name[len(name)-2]) {
		return name[:len(name)-1] + "ies"
	}

	return name + "s"
}

func isVowel(c byte) bool {
	return strings.IndexByte("aeiouAEIOU", c) >= 0
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
