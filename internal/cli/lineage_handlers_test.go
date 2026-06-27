package cli_test

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLineage_MissingArg proves both handlers are registered and reject a
// missing epic-id with a usage error before any DB connection (so the check
// runs without LW_TEST_DB_URL). The connected check/fix paths are integration
// behavior exercised when a test DB is configured.
//
//nolint:paralleltest // RunHandler mutates process-global os.Stdout
func TestLineage_MissingArg(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"lineage.check", "usage: lw lineage check"},
		{"lineage.fix", "usage: lw lineage fix"},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			_, err := testutil.RunHandler(t, tc.key, nil, map[string]any{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
