package gogen_test

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/codegen/gogen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureEntityYAML = `
_meta:
  version: "1.0.0"
  schema_id: lightwave://schemas/data/test/widget
  title: Widget
  table_kind: entity
  table_name: widgets
primary_key:
  column: id
  type: uuid
natural_key:
  column: slug
  unique: true
required_fields:
  - name: slug
    type: str
    storage: db
  - name: name
    type: str
    storage: db
  - name: created_at
    type: datetime
    storage: db
  - name: updated_at
    type: datetime
    storage: db
optional_fields:
  - name: description
    type: str
    storage: db
  - name: tags
    type: "list[str]"
    storage: db
    column_type: jsonb
`

// Multi-word title — must yield a valid Go identifier (#227 P0-1 / blocker).
const fixtureMultiWordYAML = `
_meta:
  version: "1.0.0"
  schema_id: lightwave://schemas/data/test/api_spec
  title: API Specification
  table_kind: entity
  table_name: api_specs
natural_key:
  column: slug
  unique: true
required_fields:
  - name: slug
    type: str
    storage: db
`

// Parent (zebras) + child (ants) exercise FK ordering. Names are chosen so
// alphabetical order (ants < zebras) is the OPPOSITE of dependency order
// (zebras first) — proving the migration is FK-topologically sorted, not just
// alphabetical.
const fixtureParentYAML = `
_meta:
  version: "1.0.0"
  schema_id: lightwave://schemas/data/test/zebra
  title: Zebra
  table_kind: entity
  table_name: zebras
natural_key:
  column: slug
  unique: true
required_fields:
  - name: slug
    type: str
    storage: db
`

const fixtureChildYAML = `
_meta:
  version: "1.0.0"
  schema_id: lightwave://schemas/data/test/ant
  title: Ant
  table_kind: entity
  table_name: ants
natural_key:
  column: slug
  unique: true
required_fields:
  - name: slug
    type: str
    storage: db
  - name: zebra_ref
    type: str
    storage: db
    fk_ref: lightwave://schemas/data/test/zebra
    fk_column: slug
relations:
  parent: zebra
  parent_fk:
    column: zebra_ref
    references: zebras.slug
    on_delete: CASCADE
`

const fixtureNonEntityYAML = `
_meta:
  version: "1.0.0"
  schema_id: lightwave://schemas/data/test/enum_thing
  title: EnumThing
  table_kind: enum
`

func loadFixture(t *testing.T, name, content string) *gogen.EntitySchema {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	e, err := gogen.Load(p)
	require.NoError(t, err)
	return e
}

func TestLoad_NonEntity_Rejected(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "enum_thing.yaml")
	require.NoError(t, os.WriteFile(p, []byte(fixtureNonEntityYAML), 0o644))
	_, err := gogen.Load(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an entity schema")
}

func TestGenerateGo_Struct(t *testing.T) {
	t.Parallel()
	got, err := gogen.GenerateGo(loadFixture(t, "widget.yaml", fixtureEntityYAML), "store")
	require.NoError(t, err)

	for _, want := range []string{
		"package store",
		"type Widget struct",
		`db:"id"`,
		`db:"tenant_id"`, // multi-tenant: tenant_id on every struct (#227 P0-1)
		"uuid.UUID",
		`db:"slug"`,
		"time.Time",
		"*string",         // optional string → pointer
		"json.RawMessage", // jsonb list
		"DO NOT EDIT",
	} {
		assert.Contains(t, got, want)
	}

	assert.NotContains(t, got, "*json.RawMessage")
}

// TestGenerateGo_MultiWordTitle pins the #227 blocker: a multi-word _meta.title
// must produce a valid, gofmt-clean Go identifier, not "type API Specification".
func TestGenerateGo_MultiWordTitle(t *testing.T) {
	t.Parallel()
	got, err := gogen.GenerateGo(loadFixture(t, "api_spec.yaml", fixtureMultiWordYAML), "store")
	require.NoError(t, err, "multi-word title must produce compilable Go")
	assert.Contains(t, got, "type APISpecification struct")

	formatted, ferr := format.Source([]byte(got))
	require.NoError(t, ferr)
	assert.Equal(t, string(formatted), got, "generated Go must be gofmt-clean")
}

// TestEmitMigration_Tenancy pins #227 P0-1 (tenant_id + policy + FORCE),
// P2-4 (DEFAULT now()), and per-tenant composite UNIQUE (P0-2).
func TestEmitMigration_Tenancy(t *testing.T) {
	t.Parallel()
	mig, err := gogen.EmitMigration([]*gogen.EntitySchema{loadFixture(t, "widget.yaml", fixtureEntityYAML)})
	require.NoError(t, err)

	for _, want := range []string{
		`CREATE EXTENSION IF NOT EXISTS "pgcrypto";`,
		"CREATE TABLE IF NOT EXISTS widgets",
		"id UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY",
		"tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE",
		"UNIQUE (tenant_id, slug)", // per-tenant natural key, not a global UNIQUE
		"created_at TIMESTAMPTZ NOT NULL DEFAULT now()",
		"updated_at TIMESTAMPTZ NOT NULL DEFAULT now()",
		"ALTER TABLE widgets ENABLE ROW LEVEL SECURITY;",
		"ALTER TABLE widgets FORCE ROW LEVEL SECURITY;",
		"DROP POLICY IF EXISTS widgets_tenant_isolation ON widgets;", // idempotency guard (#245)
		"CREATE POLICY widgets_tenant_isolation ON widgets",
		"current_setting('app.current_org', true)",
	} {
		assert.Contains(t, mig, want)
	}

	// Must NOT emit a bare global UNIQUE on slug (the cross-tenant-leak bug).
	assert.NotContains(t, mig, "slug TEXT NOT NULL UNIQUE")
}

// TestEmitMigration_IdempotentPolicy pins #245: CREATE POLICY has no IF NOT
// EXISTS, and the platform re-applies migrations on every boot, so EACH policy
// must be preceded by a matching DROP POLICY IF EXISTS or the second apply
// crashes with 42710. Uses two entities so a single guard can't accidentally
// satisfy the assertion for both tables.
func TestEmitMigration_IdempotentPolicy(t *testing.T) {
	t.Parallel()
	child := loadFixture(t, "ant.yaml", fixtureChildYAML)
	parent := loadFixture(t, "zebra.yaml", fixtureParentYAML)

	mig, err := gogen.EmitMigration([]*gogen.EntitySchema{child, parent})
	require.NoError(t, err)

	for _, table := range []string{"ants", "zebras"} {
		drop := fmt.Sprintf("DROP POLICY IF EXISTS %s_tenant_isolation ON %s;", table, table)
		create := fmt.Sprintf("CREATE POLICY %s_tenant_isolation ON %s", table, table)

		dropAt := strings.Index(mig, drop)
		createAt := strings.Index(mig, create)

		require.NotEqualf(t, -1, dropAt, "missing drop guard for %s", table)
		require.NotEqualf(t, -1, createAt, "missing CREATE POLICY for %s", table)
		assert.Lessf(t, dropAt, createAt, "DROP POLICY IF EXISTS must precede CREATE POLICY for %s", table)
	}

	// Universal invariant: EVERY CREATE POLICY has a matching DROP guard, so a
	// future edit that emits an extra (e.g. write-side) policy without a guard
	// can't pass while the generated schema.sql still 42710s on re-apply.
	assert.Equal(t, strings.Count(mig, "CREATE POLICY "), strings.Count(mig, "DROP POLICY IF EXISTS "),
		"every CREATE POLICY must have a matching DROP POLICY IF EXISTS guard")
}

// TestEmitMigration_FKOrder pins #227 P0-2: parent created before child, with a
// tenant-scoped composite FK. Fixture names make alphabetical order the inverse
// of dependency order, so passing proves topological (not lexical) sorting.
func TestEmitMigration_FKOrder(t *testing.T) {
	t.Parallel()
	child := loadFixture(t, "ant.yaml", fixtureChildYAML)
	parent := loadFixture(t, "zebra.yaml", fixtureParentYAML)

	mig, err := gogen.EmitMigration([]*gogen.EntitySchema{child, parent})
	require.NoError(t, err)

	zebraAt := strings.Index(mig, "CREATE TABLE IF NOT EXISTS zebras")
	antAt := strings.Index(mig, "CREATE TABLE IF NOT EXISTS ants")
	require.NotEqual(t, -1, zebraAt)
	require.NotEqual(t, -1, antAt)
	assert.Less(t, zebraAt, antAt, "parent zebras must precede child ants (topological, not alphabetical)")

	assert.Contains(t, mig, "FOREIGN KEY (tenant_id, zebra_ref) REFERENCES zebras(tenant_id, slug) ON DELETE CASCADE")
}

// TestEmitMigration_UnknownFKSkipped pins the P0-2 guard: a FK whose target is
// not in the entity set is skipped, never emitting a dangling REFERENCES.
func TestEmitMigration_UnknownFKSkipped(t *testing.T) {
	t.Parallel()
	mig, err := gogen.EmitMigration([]*gogen.EntitySchema{loadFixture(t, "ant.yaml", fixtureChildYAML)})
	require.NoError(t, err)
	assert.Contains(t, mig, "CREATE TABLE IF NOT EXISTS ants")
	assert.NotContains(t, mig, "REFERENCES zebras", "FK to an out-of-set table must be skipped")
}

func TestFindEntities(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "widget.yaml"), []byte(fixtureEntityYAML), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "not_entity.yaml"), []byte(fixtureNonEntityYAML), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "__index.yaml"), []byte("index: true"), 0o644))

	paths, err := gogen.FindEntities(dir)
	require.NoError(t, err)
	require.Len(t, paths, 1)
	assert.True(t, strings.HasSuffix(paths[0], "widget.yaml"))
}

func TestCamelCase(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"slug":                        "Slug",
		"user_story_ref":              "UserStoryRef",
		"created_at":                  "CreatedAt",
		"API Specification":           "APISpecification",
		"Non-Functional Requirements": "NonFunctionalRequirements",
	}
	for in, want := range cases {
		assert.Equal(t, want, gogen.CamelCase(in), "input: %q", in)
	}
}

func TestTableFromFKRef(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "epics", gogen.TableFromFKRef("lightwave://schemas/data/agile_artifacts/epic"))
	assert.Equal(t, "user_stories", gogen.TableFromFKRef("lightwave://schemas/data/agile_artifacts/user_story"))
	assert.Equal(t, "prds", gogen.TableFromFKRef("lightwave://schemas/data/agile_artifacts/prd"))
}
