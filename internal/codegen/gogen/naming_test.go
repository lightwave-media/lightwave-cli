package gogen_test

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/codegen/gogen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTableFromFKRefPluralisation(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		// consonant + y -> ies
		"lightwave://schemas/data/agile_artifacts/user_story": "user_stories",
		"lightwave://schemas/data/meta/agent_identity":        "agent_identities",
		"lightwave://schemas/data/test/category":              "categories",
		// vowel + y -> s. This is the case that produced `shoot_daies` on the
		// first run against data/cineos: the rule was consonant-blind because
		// every name it had seen until then ended in consonant+y.
		"lightwave://schemas/data/cineos/shoot_day": "shoot_days",
		"lightwave://schemas/data/test/survey":      "surveys",
		// plain
		"lightwave://schemas/data/agile_artifacts/epic": "epics",
		"lightwave://schemas/data/cineos/doc":           "docs",
		// a bare "y" must not index out of bounds
		"lightwave://schemas/data/test/y": "ys",
	}

	for ref, want := range cases {
		assert.Equal(t, want, gogen.TableFromFKRef(ref), "pluralising %s", ref)
	}
}

func TestTableNameIsDerivedWhenNotDeclared(t *testing.T) {
	t.Parallel()

	// No table_name. Every agile_artifacts schema declares one, so the absent
	// case went unexercised until the newly-declared families were generated:
	// 64 entities resolved, one table was emitted, and a Go file was written
	// to disk named ".go".
	e, err := gogen.Parse("ledger_event.yaml", []byte(`_meta:
  schema_id: lightwave://schemas/data/meta/ledger_event
  table_kind: entity
  scope: local
required_fields:
- name: action
  type: str
`))
	require.NoError(t, err)
	assert.Equal(t, "ledger_events", e.Meta.TableName)
}

func TestUnnameableTableIsRejected(t *testing.T) {
	t.Parallel()

	// Neither table_name nor schema_id. The derivation would have returned "s"
	// — a perfectly valid identifier — so every such schema would have
	// collided into one table named `s` and produced DDL that looked fine.
	_, err := gogen.Parse("nameless.yaml", []byte(`_meta:
  table_kind: entity
  scope: local
required_fields:
- name: field
  type: str
`))
	require.Error(t, err, "a table nobody can name must be refused, not guessed")
	assert.Contains(t, err.Error(), "neither _meta.table_name nor _meta.schema_id")
}

func TestDeclaredTableNameWins(t *testing.T) {
	t.Parallel()

	// ddd -> "ddds", not the derivation's guess. An explicit declaration must
	// never be overwritten by the fallback.
	e, err := gogen.Parse("ddd.yaml", []byte(`_meta:
  schema_id: lightwave://schemas/data/agile_artifacts/ddd
  table_kind: entity
  table_name: ddds
required_fields:
- name: name
  type: str
`))
	require.NoError(t, err)
	assert.Equal(t, "ddds", e.Meta.TableName)
}
