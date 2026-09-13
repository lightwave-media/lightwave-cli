package gogen_test

import (
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/codegen/gogen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every case here comes from applying the generated local print to a real
// Postgres 17 and reading the error. None was reachable while the generator
// only ever saw data/agile_artifacts — that family annotates every field,
// declares every table_name, uses no reserved words and has no `id` field, so
// it exercised none of these paths.

func parse(t *testing.T, yaml string) *gogen.EntitySchema {
	t.Helper()

	e, err := gogen.Parse("x.yaml", []byte(yaml))
	require.NoError(t, err)

	return e
}

func TestAbsentStorageMeansStored(t *testing.T) {
	t.Parallel()

	// `storage:` appears in 21 of 242 data schemas and every one of its 294
	// uses says "db". Requiring it made silence mean "drop the column", so the
	// newly declared families generated tables with no columns at all.
	e := parse(t, `_meta:
  schema_id: lightwave://schemas/data/meta/audio_manifest
  table_kind: entity
required_fields:
- name: codec
  type: str
optional_fields:
- name: channels
  type: int
`)

	sql, err := gogen.EmitMigration([]*gogen.EntitySchema{e})
	require.NoError(t, err)
	assert.Contains(t, sql, "codec TEXT NOT NULL")
	assert.Contains(t, sql, "channels BIGINT")
}

func TestGeneratorOwnedColumnsAreNotDuplicated(t *testing.T) {
	t.Parallel()

	// Postgres: `column "id" specified more than once`.
	e := parse(t, `_meta:
  schema_id: lightwave://schemas/data/meta/failure_record
  table_kind: entity
required_fields:
- name: id
  type: str
- name: tenant_id
  type: str
- name: summary
  type: str
`)

	sql, err := gogen.EmitMigration([]*gogen.EntitySchema{e})
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(sql, "id UUID NOT NULL DEFAULT gen_random_uuid()"))
	assert.NotContains(t, sql, "id TEXT", "a schema-declared id describes the same column, not a second one")
	assert.Contains(t, sql, "summary TEXT NOT NULL")
}

func TestReservedAndNonBareColumnNamesAreQuoted(t *testing.T) {
	t.Parallel()

	// `references JSONB,` is a syntax error; so is `allowed-tools TEXT,`,
	// which parses as a subtraction. Both are real fields in the stamp
	// (agent_handoff.references, skill_definition.allowed-tools).
	e := parse(t, `_meta:
  schema_id: lightwave://schemas/data/meta/agent_handoff
  table_kind: entity
optional_fields:
- name: references
  type: list[str]
- name: allowed-tools
  type: str
- name: ordinary_field
  type: str
`)

	sql, err := gogen.EmitMigration([]*gogen.EntitySchema{e})
	require.NoError(t, err)
	assert.Contains(t, sql, `"references" JSONB`)
	assert.Contains(t, sql, `"allowed-tools" TEXT`)
	assert.Contains(t, sql, "ordinary_field TEXT",
		"quoting only where required keeps existing generated output byte-identical")
}

func TestNaturalKeyOnAMissingColumnDoesNotEmitInvalidDDL(t *testing.T) {
	t.Parallel()

	e := parse(t, `_meta:
  schema_id: lightwave://schemas/data/meta/architectural_fit
  table_kind: entity
natural_key:
  column: slug
  unique: true
required_fields:
- name: verdict
  type: str
`)

	sql, err := gogen.EmitMigration([]*gogen.EntitySchema{e})
	require.NoError(t, err)
	assert.NotContains(t, sql, "UNIQUE (tenant_id, slug)",
		"a UNIQUE on an absent column only fails at apply time, naming the column and not the schema")
	assert.Contains(t, sql, "natural_key \"slug\" is declared but no such field exists",
		"the mismatch belongs in the generated file, next to the table")
}

func TestUnrenderableGoIsRefusedRatherThanWritten(t *testing.T) {
	t.Parallel()

	// _meta.title becomes the Go type name via CamelCase. A title with no
	// alphanumeric content yields an empty identifier and `type  struct {…}`,
	// which does not compile. Writing that to disk would break the consumer's
	// build with an error pointing at generated code rather than at the schema
	// that caused it (lightwave-cli#227).
	e := parse(t, `_meta:
  schema_id: lightwave://schemas/data/meta/broken
  title: "---"
  table_kind: entity
required_fields:
- name: field
  type: str
`)

	_, err := gogen.GenerateGo(e, "store")
	require.Error(t, err, "uncompilable generated source must be refused, not written")
	assert.Contains(t, err.Error(), "formatting generated Go")
}

func TestTenantsIsCreatedFirstAndIsolatesOnItsOwnKey(t *testing.T) {
	t.Parallel()

	// `zebra` sorts last alphabetically and `tenants` would sort mid-list, but
	// createTable hardcodes `REFERENCES tenants(id)` on every table, so tenants
	// must come first regardless. That dependency is invisible to
	// referencedTables and never mattered while tenants came from 001_init.sql.
	tenants := parse(t, `_meta:
  schema_id: lightwave://schemas/data/ui/tenant
  table_kind: entity
  table_name: tenants
  scope: both
required_fields:
- name: slug
  type: str
`)
	other := parse(t, `_meta:
  schema_id: lightwave://schemas/data/meta/agent_action
  table_kind: entity
  table_name: agent_actions
required_fields:
- name: verb
  type: str
`)

	sql, err := gogen.EmitMigration([]*gogen.EntitySchema{other, tenants})
	require.NoError(t, err)

	posTenants := strings.Index(sql, "CREATE TABLE IF NOT EXISTS tenants")
	posOther := strings.Index(sql, "CREATE TABLE IF NOT EXISTS agent_actions")
	require.Positive(t, posTenants)
	assert.Less(t, posTenants, posOther, "tenants must be created before anything that references it")

	// tenants has no tenant_id: it IS the tenant.
	tenantsDDL, _, _ := strings.Cut(sql[posTenants:], ");")
	assert.NotContains(t, tenantsDDL, "tenant_id UUID NOT NULL REFERENCES",
		"tenants must not hold a self-referencing FK no row could satisfy")
	assert.Contains(t, sql, "CREATE POLICY tenants_tenant_isolation ON tenants\n    USING (id::text",
		"tenants isolates on its own primary key, not on a tenant_id it does not have")
}
