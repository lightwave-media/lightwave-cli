package knowledge //nolint:testpackage // Exercise private file recovery and HTTP transport seams.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func exampleContent(title, priority, body string) Content {
	return Content{Properties: map[string]json.RawMessage{
		"Title": json.RawMessage(title), "Priority": json.RawMessage(priority),
	}, Markdown: body}
}

func TestMergePreservesIndependentChanges(t *testing.T) {
	t.Parallel()
	base := exampleContent(`"original"`, `1`, "body")
	local := exampleContent(`"local title"`, `1`, "body")
	external := exampleContent(`"original"`, `2`, "body")
	merged, conflicts := Merge(base, local, external, nil)
	require.Empty(t, conflicts)
	assert.Equal(t, exampleContent(`"local title"`, `2`, "body"), merged)
}

func TestMergePreservesBothSidesOfConflict(t *testing.T) {
	t.Parallel()
	base := exampleContent(`"original"`, `1`, "body")
	local := exampleContent(`"local"`, `1`, "local body")
	external := exampleContent(`"external"`, `1`, "external body")
	_, conflicts := Merge(base, local, external, nil)
	require.Len(t, conflicts, 2)
	assert.JSONEq(t, `"local"`, string(conflicts[0].Local))
	assert.JSONEq(t, `"external"`, string(conflicts[0].External))
}

func TestRuntimeOwnershipRejectsExternalOnlyChange(t *testing.T) {
	t.Parallel()
	base := exampleContent(`"original"`, `1`, "body")
	external := exampleContent(`"original"`, `99`, "body")
	merged, conflicts := Merge(base, base, external, map[string]string{"Priority": "local"})
	require.Empty(t, conflicts)
	assert.Equal(t, base, merged)
}

func TestReplayedChangeDoesNotBecomeAnotherChange(t *testing.T) {
	t.Parallel()
	content := exampleContent(`"updated"`, `1`, "body")
	merged, conflicts := Merge(content, content, content, nil)
	require.Empty(t, conflicts)
	assert.True(t, equalContent(content, merged))
}

func TestStatusAndPeopleProviderMetadataDoNotCauseEcho(t *testing.T) {
	t.Parallel()
	provider := Page{PropertiesJson: ptr(`{"Status":{"id":"status-id","type":"status","status":{"id":"choice-id","name":"Done","color":"green"}},"Owner":{"id":"owner-id","type":"people","people":[{"object":"user","id":"person","name":"Display name","person":{"email":"private@example.test"}}]}}`)}
	local := Page{PropertiesJson: ptr(`{"Status":{"type":"status","status":{"name":"Done"}},"Owner":{"type":"people","people":[{"id":"person"}]}}`)}
	remoteContent, err := ContentOf(provider)
	require.NoError(t, err)
	localContent, err := ContentOf(local)
	require.NoError(t, err)
	assert.True(t, equalContent(remoteContent, localContent))
}
