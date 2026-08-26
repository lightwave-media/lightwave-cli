//nolint:testpackage // exercises the failure-record handler directly
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFailureRecordIsAppendOnlyAndActionable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	flags := map[string]any{
		"kind":               "test-failure",
		"summary":            "JSON mode returned a false green",
		"repo":               "lightwave-cli",
		"exit-code":          1,
		"invariant":          "JSON output preserves violation exit status.",
		"expected-structure": "Encode the report, then evaluate violations.",
		"cure-command":       "Fix handler flow and add a known-bad fixture.",
		"next-verification":  "go test ./internal/cli",
	}

	require.NoError(t, failureRecordHandler(context.Background(), nil, flags))
	require.NoError(t, failureRecordHandler(context.Background(), nil, flags))

	logPath := filepath.Join(home, ".lightwave", "observability", "failures", "failure-records.jsonl")
	logFile, err := os.Open(logPath)
	require.NoError(t, err)
	defer logFile.Close()

	var records []map[string]any
	scanner := bufio.NewScanner(logFile)
	for scanner.Scan() {
		var record map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &record))
		records = append(records, record)
	}
	require.NoError(t, scanner.Err())
	require.Len(t, records, 2, "recording a second failure must append, not overwrite")

	required := []string{
		"id",
		"kind",
		"summary",
		"detected_at",
		"repo",
		"exit_code",
		"signal_class",
		"violated_invariant",
		"expected_structure",
		"cure_command",
		"do_not",
		"next_verification",
		"fingerprint",
	}
	for _, field := range required {
		assert.NotEmpty(t, records[0][field], "required development-signal field %s", field)
	}
	assert.NotEqual(t, records[0]["id"], records[1]["id"])
	assert.Equal(t, records[0]["fingerprint"], records[1]["fingerprint"],
		"the same failure class must deduplicate across invocations")
	assert.Contains(t, records[0]["do_not"], "Do not skip")
}
