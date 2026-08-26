package cli_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrePushBypassRequiresReasonAndRecordsEvidence(t *testing.T) {
	t.Parallel()

	_, err := exec.LookPath("jq")
	require.NoError(t, err, "jq is part of the bypass evidence boundary")

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	hook := filepath.Join(root, "dev", "hooks", "pre-push")
	home := t.TempDir()

	run := func(reason string) *exec.Cmd {
		t.Helper()
		command := exec.CommandContext(context.Background(), "sh", hook)
		command.Dir = root
		command.Stdin = strings.NewReader("")
		command.Env = append(os.Environ(),
			"HOME="+home,
			"LW_SKIP_PRE_PUSH=1",
			"LW_SKIP_PRE_PUSH_REASON="+reason,
		)
		return command
	}

	missingReason := run("")
	output, runErr := missingReason.CombinedOutput()
	require.Error(t, runErr)
	assert.Contains(t, string(output), "bypass denied")

	authorized := run("incident CLI-TEST: operator-authorized recovery")
	output, runErr = authorized.CombinedOutput()
	require.NoError(t, runErr, string(output))
	assert.Contains(t, string(output), "required server checks still apply")

	eventPath := filepath.Join(home, ".lightwave", "observability", "bypass-events.jsonl")
	eventFile, err := os.Open(eventPath)
	require.NoError(t, err)
	defer eventFile.Close()

	scanner := bufio.NewScanner(eventFile)
	require.True(t, scanner.Scan())
	var event map[string]string
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
	assert.Equal(t, filepath.Base(root), event["repo"])
	assert.Equal(t, "pre-push", event["source"])
	assert.Contains(t, event["reason"], "CLI-TEST")
}
