package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writePersona creates dir/file with body, creating parent dirs, and
// returns the path.
func writePersona(t *testing.T, dir, file, body string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755), "mkdir persona dir")

	path := filepath.Join(dir, file)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644), "write persona")

	return path
}

// agentsDir is where EB-007 put the persona prints under a lightwave root.
func agentsDir(root string) string {
	return filepath.Join(root, "config", "agents")
}

// TestLoadPersonaPrompt_Resolution pins the lookup order after EB-007 moved
// the persona prints from ~/.brain/cortex/... to ~/.lightwave/config/agents/.
//
// No t.Parallel here or in the subtests: every case pins HOME, BRAIN and
// LW_PERSONA_DIR with t.Setenv, which panics under a parallel test.
//
//nolint:paralleltest // t.Setenv forbids t.Parallel
func TestLoadPersonaPrompt_Resolution(t *testing.T) {
	tests := []struct {
		// setup pins env for the case and returns the path that must win.
		setup   func(t *testing.T) string
		name    string
		persona string
	}{
		{
			name:    "bare name falls back to the v_ prefixed print under HOME",
			persona: "platform-developer",
			setup: func(t *testing.T) string {
				t.Helper()
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("BRAIN", "")
				t.Setenv("LW_PERSONA_DIR", "")

				return writePersona(t, agentsDir(filepath.Join(home, ".lightwave")),
					"v_platform-developer.yaml", "name: v_platform-developer\n")
			},
		},
		{
			name:    "exact v_ name resolves without fallback",
			persona: "v_qa-engineer",
			setup: func(t *testing.T) string {
				t.Helper()
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("BRAIN", "")
				t.Setenv("LW_PERSONA_DIR", "")

				return writePersona(t, agentsDir(filepath.Join(home, ".lightwave")),
					"v_qa-engineer.yaml", "name: v_qa-engineer\n")
			},
		},
		{
			name:    "BRAIN parent wins over HOME",
			persona: "qa-engineer",
			setup: func(t *testing.T) string {
				t.Helper()
				lightwaveRoot := t.TempDir()
				t.Setenv("HOME", t.TempDir())
				t.Setenv("BRAIN", filepath.Join(lightwaveRoot, "brain"))
				t.Setenv("LW_PERSONA_DIR", "")

				return writePersona(t, agentsDir(lightwaveRoot),
					"v_qa-engineer.yaml", "name: v_qa-engineer\n")
			},
		},
		{
			name:    "LW_PERSONA_DIR override wins over the print",
			persona: "qa-engineer",
			setup: func(t *testing.T) string {
				t.Helper()
				home := t.TempDir()
				override := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("BRAIN", "")
				t.Setenv("LW_PERSONA_DIR", override)
				writePersona(t, agentsDir(filepath.Join(home, ".lightwave")),
					"v_qa-engineer.yaml", "name: print\n")

				return writePersona(t, override, "qa-engineer.yaml", "name: override\n")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantPath := tt.setup(t)

			body, gotPath, err := agent.LoadPersonaPrompt(tt.persona)
			require.NoError(t, err, "LoadPersonaPrompt")
			assert.Equal(t, wantPath, gotPath)

			wantBody, err := os.ReadFile(wantPath)
			require.NoError(t, err, "read expected persona")
			assert.Equal(t, string(wantBody), body)
		})
	}
}

// TestLoadPersonaPrompt_NotFoundListsSearchedPaths proves the error names
// both candidate prints so the operator knows exactly where to stub one.
// No t.Parallel: t.Setenv.
//
//nolint:paralleltest // t.Setenv forbids t.Parallel
func TestLoadPersonaPrompt_NotFoundListsSearchedPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BRAIN", "")
	t.Setenv("LW_PERSONA_DIR", "")

	_, _, err := agent.LoadPersonaPrompt("missing")

	var notFound *agent.PersonaNotFoundError
	require.ErrorAs(t, err, &notFound)

	dir := agentsDir(filepath.Join(home, ".lightwave"))
	assert.Equal(t, []string{
		filepath.Join(dir, "missing.yaml"),
		filepath.Join(dir, "v_missing.yaml"),
	}, notFound.SearchedIn)
}
