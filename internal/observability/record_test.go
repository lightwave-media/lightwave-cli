package observability_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/observability"
	"github.com/stretchr/testify/require"
)

func TestRecordOperatorCLI_AppendsAndFeedbackOnFail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LW_HOME_PRINT", filepath.Join(home, ".lightwave"))
	t.Setenv("LW_OBSERVABILITY_DIR", filepath.Join(home, ".lightwave", "observability"))

	require.NoError(t, observability.RecordOperatorCLI(&observability.OperatorCLIEvent{
		Verb:         "home.sync",
		Outcome:      "pass",
		DurationMS:   3,
		ExitCode:     0,
		Measurements: map[string]any{"files_updated": 1},
	}))

	require.NoError(t, observability.RecordOperatorCLI(&observability.OperatorCLIEvent{
		Verb:     "release.flag",
		Outcome:  "fail",
		ExitCode: 1,
		Detail:   "unknown feature flag",
	}))

	obsPath := filepath.Join(home, ".lightwave", "observability", "operator-cli.jsonl")
	data, err := os.ReadFile(obsPath)
	require.NoError(t, err)
	require.Contains(t, string(data), `"verb":"home.sync"`)
	require.Contains(t, string(data), `"outcome":"fail"`)

	fbPath := filepath.Join(home, ".lightwave", "brain", "tool-feedback", "lw", time.Now().UTC().Format("2006-01-02")+".jsonl")
	fb, err := os.ReadFile(fbPath)
	require.NoError(t, err)

	var lesson observability.ToolFeedbackLesson
	require.NoError(t, json.Unmarshal(fb[:len(fb)-1], &lesson))
	require.Equal(t, "agent-harness", lesson.Audience)
}

func TestRecentOperatorEvents(t *testing.T) {
	home := t.TempDir()
	obs := filepath.Join(home, ".lightwave", "observability")
	require.NoError(t, os.MkdirAll(obs, 0o755))
	t.Setenv("LW_OBSERVABILITY_DIR", obs)

	require.NoError(t, observability.RecordOperatorCLI(&observability.OperatorCLIEvent{Verb: "version", Outcome: "pass"}))
	require.NoError(t, observability.RecordOperatorCLI(&observability.OperatorCLIEvent{Verb: "home.sync", Outcome: "pass"}))

	events, err := observability.RecentOperatorEvents(10)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "home.sync", events[1].Verb)
}
