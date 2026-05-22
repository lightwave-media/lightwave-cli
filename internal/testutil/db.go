package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// EnvTestDBURL is the env var consumed by NewPool. Setting it points
// the test pool at a Postgres instance (typically a dedicated test
// schema or an ephemeral container). Unset → tests skip rather than
// fail, so the suite stays portable across machines that don't have
// the dev stack running.
const EnvTestDBURL = "LW_TEST_DB_URL"

// NewPool opens a pgx pool using the URL in LW_TEST_DB_URL and
// registers a cleanup that closes it when the test exits. If the env
// var is empty, NewPool calls t.Skip — DB-backed tests are opt-in.
//
// Tests that need per-row isolation use the Make* fixtures below;
// each Make* registers its own delete-on-cleanup so rows don't
// accumulate across test runs.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv(EnvTestDBURL)
	if url == "" {
		t.Skipf("%s not set; skipping DB-backed test", EnvTestDBURL)
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err, "pgxpool.New(%s)", url)
	require.NoError(t, pool.Ping(ctx), "pgx ping")

	t.Cleanup(pool.Close)

	return pool
}

// randSuffix returns 8 hex chars suitable for keeping fixture names
// unique across parallel tests. Fixture names like
// "test-epic-3a4b5c6d" are easy to spot + grep for in DB cleanup.
func randSuffix() string {
	var b [4]byte

	_, _ = rand.Read(b[:])

	return hex.EncodeToString(b[:])
}
