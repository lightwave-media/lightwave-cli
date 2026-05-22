// Package testutil provides shared helpers for tests across the
// lightwave-cli codebase: a Postgres pool opener that skips when no
// fixture DB is available, builders for createOS planning entities
// (Epic / Sprint / Story / Task), and a handler invoker that captures
// stdout so dispatcher-driven handlers can be exercised without
// spinning up the full cobra tree.
//
// Conventions for tests that use this package (mirrored in CLAUDE.md):
//
//   - Every new test calls t.Parallel().
//   - Setup-fatal assertions use stretchr/testify/require; content
//     assertions use stretchr/testify/assert.
//   - DB-backed tests open a pool via NewPool(t) — which calls t.Skip
//     when LW_TEST_DB_URL is empty, so tests stay portable across
//     workstations and CI runners that don't have Postgres.
//   - Fixture rows from MakeEpic / MakeSprint / MakeStory clean
//     themselves up via t.Cleanup; tests don't need to track IDs.
//   - Handler invocation goes through RunHandler(t, key, args, flags)
//     — direct cobra.Execute is reserved for end-to-end smoke tests.
package testutil
