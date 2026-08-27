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

	event := readFirstBypassEvent(t, home)
	assert.Equal(t, "lightwave-cli", event["repo"])
	assert.Equal(t, "pre-push", event["source"])
	assert.Contains(t, event["reason"], "CLI-TEST")
}

// TestPrePushBypassRepoNameIsWorktreeSafe pins that the bypass event's repo
// name comes from the origin remote, not the checkout's directory basename —
// git worktrees have arbitrary directory names.
func TestPrePushBypassRepoNameIsWorktreeSafe(t *testing.T) {
	t.Parallel()

	_, err := exec.LookPath("jq")
	require.NoError(t, err, "jq is part of the bypass evidence boundary")

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	hook := filepath.Join(root, "dev", "hooks", "pre-push")

	scratch := t.TempDir()
	checkout := filepath.Join(scratch, "scratch-checkout")
	worktree := filepath.Join(scratch, "oddly-named-worktree")

	git := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), "git", args...)
		command.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_CONFIG_SYSTEM="+os.DevNull,
		)
		output, gitErr := command.CombinedOutput()
		require.NoError(t, gitErr, string(output))
	}
	git("init", checkout)
	git("-C", checkout, "-c", "user.name=lw-test", "-c", "user.email=lw-test@example.com",
		"commit", "--allow-empty", "-m", "init")
	git("-C", checkout, "remote", "add", "origin",
		"https://github.com/lightwave-media/scratch-repo.git")
	git("-C", checkout, "worktree", "add", worktree)

	home := t.TempDir()
	command := exec.CommandContext(t.Context(), "sh", hook)
	command.Dir = worktree
	command.Stdin = strings.NewReader("")
	command.Env = append(os.Environ(),
		"HOME="+home,
		"LW_SKIP_PRE_PUSH=1",
		"LW_SKIP_PRE_PUSH_REASON=incident CLI-TEST: worktree repo-name check",
	)
	output, runErr := command.CombinedOutput()
	require.NoError(t, runErr, string(output))

	event := readFirstBypassEvent(t, home)
	assert.Equal(t, "scratch-repo", event["repo"],
		"repo name must come from the origin remote, not the worktree directory basename")
}

func readFirstBypassEvent(t *testing.T, home string) map[string]string {
	t.Helper()
	eventPath := filepath.Join(home, ".lightwave", "observability", "bypass-events.jsonl")
	eventFile, err := os.Open(eventPath)
	require.NoError(t, err)
	defer eventFile.Close()

	scanner := bufio.NewScanner(eventFile)
	require.True(t, scanner.Scan())
	var event map[string]string
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
	return event
}
