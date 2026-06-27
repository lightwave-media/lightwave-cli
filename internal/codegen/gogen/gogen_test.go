package gogen_test

import (
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
optional_fields:
  - name: description
    type: str
    storage: db
  - name: tags
    type: "list[str]"
    storage: db
    column_type: jsonb
`

const fixtureNonEntityYAML = `
_meta:
  version: "1.0.0"
  schema_id: lightwave://schemas/data/test/enum_thing
  title: EnumThing
  table_kind: enum
`

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestLoad_Entity(t *testing.T) {
	t.Parallel()
	p := writeFixture(t, "widget.yaml", fixtureEntityYAML)
	e, err := gogen.Load(p)
	require.NoError(t, err)
	assert.Equal(t, "Widget", e.Meta.Title)
	assert.Equal(t, "widgets", e.Meta.TableName)
	assert.Equal(t, "entity", e.Meta.TableKind)
	assert.Len(t, e.RequiredFields, 3)
	assert.Len(t, e.OptionalFields, 2)
}

func TestLoad_NonEntity_Rejected(t *testing.T) {
	t.Parallel()
	p := writeFixture(t, "enum_thing.yaml", fixtureNonEntityYAML)
	_, err := gogen.Load(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an entity schema")
}

func TestGenerate_GoFile(t *testing.T) {
	t.Parallel()
	p := writeFixture(t, "widget.yaml", fixtureEntityYAML)
	e, err := gogen.Load(p)
	require.NoError(t, err)

	out := gogen.Generate(e, "store")

	assert.Contains(t, out.GoFile, "package store")
	assert.Contains(t, out.GoFile, "type Widget struct")
	assert.Contains(t, out.GoFile, `db:"id"`)
	assert.Contains(t, out.GoFile, "uuid.UUID")
	assert.Contains(t, out.GoFile, `db:"slug"`)
	assert.Contains(t, out.GoFile, `db:"created_at"`)
	assert.Contains(t, out.GoFile, "time.Time")
	// optional string field uses pointer
	assert.Contains(t, out.GoFile, "*string")
	// optional jsonb field does not use pointer
	assert.Contains(t, out.GoFile, "json.RawMessage")
	assert.NotContains(t, out.GoFile, "*json.RawMessage")
	// DO NOT EDIT banner present
	assert.Contains(t, out.GoFile, "DO NOT EDIT")
}

func TestGenerate_SQLFile(t *testing.T) {
	t.Parallel()
	p := writeFixture(t, "widget.yaml", fixtureEntityYAML)
	e, err := gogen.Load(p)
	require.NoError(t, err)

	out := gogen.Generate(e, "store")

	assert.Contains(t, out.SQLFile, "CREATE TABLE IF NOT EXISTS widgets")
	assert.Contains(t, out.SQLFile, "gen_random_uuid()")
	assert.Contains(t, out.SQLFile, "slug")
	assert.Contains(t, out.SQLFile, "created_at")
	assert.Contains(t, out.SQLFile, "TIMESTAMPTZ")
	assert.Contains(t, out.SQLFile, "JSONB")
	assert.Contains(t, out.SQLFile, "ENABLE ROW LEVEL SECURITY")
	assert.Contains(t, out.SQLFile, "DO NOT EDIT")
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
	cases := []struct{ in, want string }{
		{"slug", "Slug"},
		{"user_story_ref", "UserStoryRef"},
		{"prd_ref", "PrdRef"},
		{"created_at", "CreatedAt"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, gogen.CamelCase(tc.in), "input: %q", tc.in)
	}
}

func TestTableFromFKRef(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "epics", gogen.TableFromFKRef("lightwave://schemas/data/agile_artifacts/epic"))
	assert.Equal(t, "sprints", gogen.TableFromFKRef("lightwave://schemas/data/agile_artifacts/sprint"))
	assert.Equal(t, "user_stories", gogen.TableFromFKRef("lightwave://schemas/data/agile_artifacts/user_story"))
	assert.Equal(t, "prds", gogen.TableFromFKRef("lightwave://schemas/data/agile_artifacts/prd"))
}
