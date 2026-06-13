package docsfactory_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/docsfactory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHandoffYAML_WholeFile(t *testing.T) {
	t.Parallel()
	doc, err := docsfactory.ParseHandoffYAML([]byte("id: x\nstatus: received\nsteps:\n  - step_id: S1\n    kind: inputs\n"))
	require.NoError(t, err)
	assert.Equal(t, "x", doc.ID)
	require.Len(t, doc.Steps, 1)
	assert.Equal(t, "inputs", doc.Steps[0].Kind)
}

func TestParseHandoffYAML_ExampleKeyed(t *testing.T) {
	t.Parallel()
	// Mirrors the agent_handoff.yaml schema file: a top-level example: block.
	y := "_meta:\n  version: \"1.2.0\"\nexample:\n  id: ex\n  status: received\n  steps:\n    - step_id: S1\n      kind: command\n      success: ok\n      failure: bad\n"
	doc, err := docsfactory.ParseHandoffYAML([]byte(y))
	require.NoError(t, err)
	assert.Equal(t, "ex", doc.ID)
	require.Len(t, doc.Steps, 1)
	assert.Equal(t, "command", doc.Steps[0].Kind)
	require.NotNil(t, doc.Steps[0].Success)
	assert.Equal(t, "ok", *doc.Steps[0].Success)
}

func TestLoadHandoff_UnsupportedMarkdown(t *testing.T) {
	t.Parallel()
	_, err := docsfactory.LoadHandoff("/tmp/whatever.handoff.md")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet supported")
}

func TestLoadHandoff_YAMLFile(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "h.yaml")
	require.NoError(t, os.WriteFile(p, []byte("id: f\nstatus: received\n"), 0o644))
	doc, err := docsfactory.LoadHandoff(p)
	require.NoError(t, err)
	assert.Equal(t, "f", doc.ID)
}

func TestLoadHandoff_BadExtension(t *testing.T) {
	t.Parallel()
	_, err := docsfactory.LoadHandoff("/tmp/x.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported handoff file extension")
}
