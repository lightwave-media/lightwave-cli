//nolint:testpackage // drives rawCLIConfig.decode and flattenCommandKeys, both unexported
package sst

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// listVerb recurs across these fixtures; naming it makes the shared shape
// explicit.
const listVerb = "list"

// publishedDomainWithOneUnbuiltVerb is the case command-level `_status` exists
// for: a live domain gaining a verb nobody has written yet.
//
// Marking the DOMAIN in_development would work for the gate and be wrong for
// users — it hides `validate`, `drift` and every other working sibling to
// shelter the one unbuilt verb.
func publishedDomainWithOneUnbuiltVerb() *CLIConfig {
	return &CLIConfig{
		Domains: []CLIDomain{{
			Name: "schema",
			Commands: []CLICommand{
				{Name: "new", Status: StatusInDevelopment},
				{Name: "validate"},
				{Name: "drift"},
			},
		}},
	}
}

func TestKeysPublishedExcludesAnInDevelopmentCommand(t *testing.T) {
	t.Parallel()

	cfg := publishedDomainWithOneUnbuiltVerb()

	assert.Equal(t, []string{"schema.validate", "schema.drift"}, cfg.KeysPublished(),
		"the unbuilt verb is not shipped, so strict must not demand a handler for it")

	assert.Equal(t, []string{"schema.new", "schema.validate", "schema.drift"}, cfg.Keys(),
		"but it IS stamped — Keys is what stops the handler reading as an orphan")
}

// TestInDevelopmentCommandIsStampedButNotPublished states the distinction in
// one assertion, because conflating the two is what made the whole
// lightwave-core#647 cohort unlandable (fixed in #454, same root cause).
func TestInDevelopmentCommandIsStampedButNotPublished(t *testing.T) {
	t.Parallel()

	cfg := publishedDomainWithOneUnbuiltVerb()

	stamped := make(map[string]bool)
	for _, k := range cfg.Keys() {
		stamped[k] = true
	}

	published := make(map[string]bool)
	for _, k := range cfg.KeysPublished() {
		published[k] = true
	}

	require.True(t, stamped["schema.new"], "stamped")
	require.False(t, published["schema.new"], "not published")
}

// TestKeysPublishedDropsAnInDevelopmentGroupsWholeSubtree — a verb under an
// unbuilt group is not reachable either, so counting it as published would
// demand a handler for something nothing can call.
func TestKeysPublishedDropsAnInDevelopmentGroupsWholeSubtree(t *testing.T) {
	t.Parallel()

	cfg := &CLIConfig{Domains: []CLIDomain{{
		Name: "voice",
		Commands: []CLICommand{
			{
				Name:   "profile",
				Status: StatusInDevelopment,
				Commands: []CLICommand{
					{Name: listVerb},
					{Name: "show"},
				},
			},
			{Name: "say"},
		},
	}}}

	assert.Equal(t, []string{"voice.say"}, cfg.KeysPublished())
	assert.Equal(t, []string{"voice.profile.list", "voice.profile.show", "voice.say"}, cfg.Keys())
}

// TestDomainStatusStillWins keeps the older mechanism intact: a published
// command inside an in_development domain is still unpublished.
func TestDomainStatusStillWins(t *testing.T) {
	t.Parallel()

	cfg := &CLIConfig{Domains: []CLIDomain{{
		Name:     "cron",
		Status:   StatusInDevelopment,
		Commands: []CLICommand{{Name: listVerb}},
	}}}

	assert.Empty(t, cfg.KeysPublished())
	assert.Equal(t, []string{"cron.list"}, cfg.Keys())
}

// TestCommandStatusDecodesFromYAML pins the wire name. The field is only ever
// set by the stamp, so a silent tag typo would make every command read as
// published and the whole mechanism would be inert — the defect class §18
// calls a gate that is active and unreachable.
func TestCommandStatusDecodesFromYAML(t *testing.T) {
	t.Parallel()

	var raw rawCLIConfig
	require.NoError(t, yaml.Unmarshal([]byte(`
_meta:
  version: "1.7.0"
domains:
  schema:
    description: "Schema validation and code generation"
    commands:
      - name: new
        _status: in_development
        description: "Scaffold a new schema YAML"
      - name: validate
        description: "Validate YAML schema structure"
`), &raw))

	cfg, err := raw.decode()
	require.NoError(t, err)

	domain := cfg.FindDomain("schema")
	require.NotNil(t, domain)
	require.Len(t, domain.Commands, 2)

	assert.True(t, domain.Commands[0].InDevelopment(), "`new` is declared in_development")
	assert.False(t, domain.Commands[1].InDevelopment(), "`validate` carries no status, so it ships")
	assert.Equal(t, []string{"schema.validate"}, cfg.KeysPublished())
}

// TestDecodeRejectsADomainsBlockThatIsNotAMapping is the rejection path of the
// decoder these tests otherwise only exercise on the happy side.
//
// It matters for the same reason the rest of this file does: a stamp that
// parses into nothing, quietly, is how a command surface reads as empty and
// every handler reads as an orphan. The decoder refusing is what turns that
// into a message instead of a confident wrong answer.
func TestDecodeRejectsADomainsBlockThatIsNotAMapping(t *testing.T) {
	t.Parallel()

	var raw rawCLIConfig
	require.NoError(t, yaml.Unmarshal([]byte(`
_meta:
  version: "1.7.0"
domains:
  - schema
  - check
`), &raw))

	_, err := raw.decode()
	require.Error(t, err, "a sequence is not a domain mapping")
	assert.Contains(t, err.Error(), "expected mapping")
}
