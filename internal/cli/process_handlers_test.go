package cli_test

import (
	"encoding/json"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
	"github.com/lightwave-media/lightwave-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePsOutput(t *testing.T) {
	t.Parallel()

	raw := "  123  4.5  1.2 bash\n" +
		"45678 12.0  3.4 com.apple.WebKit.WebContent\n" +
		"    9  0.0  0.1 my helper proc\n" + // command name with spaces
		"garbage line\n" + // < 4 fields → skipped
		"abc 1 2 notapid\n" + // non-numeric pid → skipped
		"\n" // empty → skipped

	got := cli.ParsePsOutput(raw)

	require.Len(t, got, 3, "three well-formed rows; malformed/empty lines skipped")

	assert.Equal(t, 123, got[0].PID)
	assert.InDelta(t, 4.5, got[0].CPU, 0.001)
	assert.InDelta(t, 1.2, got[0].Mem, 0.001)
	assert.Equal(t, "bash", got[0].Name)

	assert.Equal(t, "com.apple.WebKit.WebContent", got[1].Name)
	assert.Equal(t, "my helper proc", got[2].Name, "command name with spaces is preserved")
}

//nolint:paralleltest // serial — RunHandler swaps process-global os.Stdout
func TestProcessListHandler_JSONSmoke(t *testing.T) {
	// End-to-end: registration + real `ps` on the host + --json output. Any
	// host has running processes, so a valid non-empty JSON array is expected.
	out, err := testutil.RunHandler(t, "process.list", nil, map[string]any{"json": true})
	require.NoError(t, err, "process list --json should succeed against host ps")

	var procs []struct {
		Name string  `json:"name"`
		CPU  float64 `json:"cpu"`
		Mem  float64 `json:"mem"`
		PID  int     `json:"pid"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &procs), "output must be valid JSON")
	assert.NotEmpty(t, procs, "host should report at least one process")
}
