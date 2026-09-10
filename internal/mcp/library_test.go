//nolint:testpackage,goconst // exercises unexported stamp-library helpers; fixture keys repeat by shape
package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStampLibraryListReadAndJail(t *testing.T) {
	t.Parallel()

	s := fixtureLibraryServer(t)

	listed := s.stampList(map[string]string{"kind": "schemas"})
	require.False(t, listed.IsError, contentText(listed))
	assert.Contains(t, contentText(listed), "data/agile/epic.yaml")
	assert.Contains(t, contentText(listed), "policy/validity/core-self.yaml")

	filtered := s.stampList(map[string]string{"kind": "schemas", "category": "data"})
	require.False(t, filtered.IsError, contentText(filtered))
	assert.Contains(t, contentText(filtered), "data/agile/epic.yaml")
	assert.NotContains(t, contentText(filtered), "policy/validity/core-self.yaml")

	queried := s.stampList(map[string]string{"kind": "schemas", "query": "fixture epic"})
	require.False(t, queried.IsError, contentText(queried))
	assert.Contains(t, contentText(queried), "data/agile/epic.yaml")
	assert.Equal(t, 1, strings.Count(contentText(queried), `"path":`))

	templates := s.stampList(map[string]string{"kind": "templates"})
	require.False(t, templates.IsError, contentText(templates))
	assert.Contains(t, contentText(templates), "agents/persona.yaml")

	knowledge := s.stampList(map[string]string{"kind": "knowledge"})
	require.False(t, knowledge.IsError, contentText(knowledge))
	assert.Contains(t, contentText(knowledge), "notes/hello.yaml")

	unknown := s.stampList(map[string]string{"kind": "nope"})
	require.True(t, unknown.IsError)
	assert.Contains(t, contentText(unknown), "unknown kind")

	read := s.stampRead(map[string]string{"kind": "schemas", "path": "data/agile/epic.yaml"})
	require.False(t, read.IsError, contentText(read))
	assert.Contains(t, contentText(read), "lightwave://schemas/data/agile/epic")

	escaped := s.stampRead(map[string]string{"kind": "schemas", "path": "../secret.txt"})
	require.True(t, escaped.IsError)
	assert.Contains(t, contentText(escaped), "escapes the library root")
}

func TestStampLibrarySchemaHelpers(t *testing.T) {
	t.Parallel()

	s := fixtureLibraryServer(t)

	fields := s.schemaFields(map[string]string{"schema_path": "data/agile/epic", "resolve_enums": "true"})
	require.False(t, fields.IsError, contentText(fields))
	assert.Contains(t, contentText(fields), `"name": "status"`)
	assert.Contains(t, contentText(fields), `"enum_ref": "fixture_statuses"`)
	assert.Contains(t, contentText(fields), `"value": "open"`)

	enum := s.enumRead(map[string]string{"enum_name": "fixture_statuses"})
	require.False(t, enum.IsError, contentText(enum))
	assert.Contains(t, contentText(enum), `"default": "open"`)
	assert.Contains(t, contentText(enum), `"value": "closed"`)

	missing := s.enumRead(map[string]string{"enum_name": "does-not-exist"})
	require.True(t, missing.IsError)
	assert.Contains(t, contentText(missing), "schema not found")

	rules := s.rulesList()
	require.False(t, rules.IsError, contentText(rules))
	assert.Contains(t, contentText(rules), `"id": "R-TEST-1"`)
	assert.Contains(t, contentText(rules), `"id": "R-TEST-2"`)
}

func TestCallToolStampListOnDeveloperTier(t *testing.T) {
	t.Parallel()

	s := fixtureLibraryServer(t)
	out := s.callTool(t.Context(), TierDeveloper, mustJSON(t, callParams{
		Name:      "stamp_list",
		Arguments: mustJSON(t, map[string]string{"kind": "schemas"}),
	}))
	require.False(t, out.IsError, contentText(out))
	assert.Contains(t, contentText(out), "data/agile/epic.yaml")
}

func TestJailJoinRejectsEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, err := jailJoin(root, "../secret.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the library root")

	joined, err := jailJoin(root, "data/agile/epic.yaml")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "data/agile/epic.yaml"), joined)
}

func fixtureLibraryServer(t *testing.T) Server {
	t.Helper()

	core := t.TempDir()
	home := t.TempDir()
	schemas := filepath.Join(core, "src", "schemas")

	writeFixture(t, filepath.Join(schemas, "data", "agile", "epic.yaml"), `
_meta:
  schema_id: "lightwave://schemas/data/agile/epic"
  title: "Fixture Epic"
  version: "1.0.0"
  description: "fixture epic used by stamp_list query"
required_fields:
  - name: status
    type: string
    description: "Lifecycle status"
    status_enum: fixture_statuses
optional_fields:
  - name: note
    type: string
    description: "Free text"
`)
	writeFixture(t, filepath.Join(schemas, "data", "enums", "fixture_statuses.yaml"), `
name: fixture_statuses
default: open
closed: true
options:
  - value: open
    label: Open
    description: In flight
  - value: closed
    label: Closed
    description: Done
`)
	writeFixture(t, filepath.Join(schemas, "policy", "validity", "core-self.yaml"), `
rules:
  - id: R-TEST-1
    name: Fixture rule one
    severity: high
    enforced_by: ["test"]
`)
	writeFixture(t, filepath.Join(schemas, "policy", "validity", "core-self-continued.yaml"), `
rules:
  - id: R-TEST-2
    name: Fixture rule two
    severity: medium
    enforced_by: ["test"]
`)
	writeFixture(t, filepath.Join(core, "src", "boilerplate", "templates", "__index.yaml"), `
templates:
  agents:
    persona: agents/persona.yaml
`)
	writeFixture(t, filepath.Join(core, "src", "boilerplate", "blueprints", "__index.yaml"), `
blueprints:
  demo: demo
`)
	writeFixture(t, filepath.Join(core, "src", "runbooks", "__index.yaml"), `
categories:
  quality:
    ci-gate: quality/ci-gate
`)
	writeFixture(t, filepath.Join(schemas, "workflows", "__index.yaml"), `
schemas:
  sops: sops/__index.yaml
`)
	writeFixture(t, filepath.Join(schemas, "workflows", "sops", "__index.yaml"), `
schemas:
  ship: ship.yaml
`)
	writeFixture(t, filepath.Join(home, ".lightwave", "brain", "memory", "notes", "hello.yaml"), "title: hello\n")
	writeFixture(t, filepath.Join(core, "secret.txt"), "should-not-be-readable\n")

	return Server{HomeDir: home, CoreRoot: core}
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}
