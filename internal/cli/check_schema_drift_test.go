package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lightwave-media/lightwave-cli/internal/cli"
)

// The key strings recur across the table; naming them also makes the two
// denominators visually distinct at each call site.
const (
	taskList   = "task.list"
	taskCreate = "task.create"
	adrNew     = "adr.new" // declared under an in_development domain
)

// These tests drive ComputeSchemaDrift directly rather than through
// testutil.RunHandler, so they run everywhere — including CI, which has no
// lightwave-core checkout and therefore skips every handler-level schema test.
//
// That matters here specifically: the defect below was shipped and armed for
// months while the tests that could have caught it were skipping. A guard that
// only runs on a developer machine is not a guard (CLAUDE.md §18).

// TestComputeSchemaDrift_InDevelopmentHandlerIsNotOrphaned is the regression
// test for the defect that blocked the lightwave-core#647 cohort.
//
// `adr.new` is DECLARED in commands.yaml under an `_status: in_development`
// domain. It is therefore absent from KeysPublished and present in Keys. Before
// the fix the orphan direction measured against the published set, so
// registering the handler the declaration was waiting for made the armed gate
// fail and block the merge — the exact opposite of the mechanism's purpose.
func TestComputeSchemaDrift_InDevelopmentHandlerIsNotOrphaned(t *testing.T) {
	t.Parallel()

	published := []string{taskList, taskCreate}
	stamped := []string{taskList, taskCreate, adrNew} // adr is in_development
	registry := []string{taskList, taskCreate, adrNew}
	surface := []string{taskList, taskCreate}

	missing, orphaned, unstamped := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Empty(t, orphaned, "a handler whose command IS stamped must never be orphaned")
	assert.Empty(t, missing, "every published command has its handler")
	assert.Empty(t, unstamped, "nothing invocable is unstamped")
}

// TestComputeSchemaDrift_StillCatchesATrueOrphan is the other direction.
//
// Widening the orphan denominator could have silenced the check entirely. A
// handler with no declaration anywhere must still be reported — that is the
// invariant AGENTS.md states as "no registered handler without a schema entry".
func TestComputeSchemaDrift_StillCatchesATrueOrphan(t *testing.T) {
	t.Parallel()

	published := []string{taskList}
	stamped := []string{taskList}
	registry := []string{taskList, "ghost.verb"} // declared nowhere
	surface := []string{taskList}

	_, orphaned, _ := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Equal(t, []string{"ghost.verb"}, orphaned,
		"a handler with no declaration at all is still drift")
}

// TestComputeSchemaDrift_MissingStillSkipsInDevelopment pins the behaviour the
// narrowed set was introduced for, so the fix above cannot regress it.
func TestComputeSchemaDrift_MissingStillSkipsInDevelopment(t *testing.T) {
	t.Parallel()

	// `cron.sync` is declared in_development and has no handler yet. That is
	// the state the mechanism exists to permit.
	published := []string{taskList}
	stamped := []string{taskList, "cron.sync"}
	registry := []string{taskList}
	surface := []string{taskList}

	missing, orphaned, _ := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Empty(t, missing, "an in_development command may be declared ahead of its handler")
	assert.Empty(t, orphaned)
}

func TestComputeSchemaDrift_MissingHandlerIsReported(t *testing.T) {
	t.Parallel()

	published := []string{taskList, taskCreate}
	stamped := []string{taskList, taskCreate}
	registry := []string{taskList}
	surface := []string{taskList}

	missing, _, _ := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Equal(t, []string{taskCreate}, missing,
		"a published command with no handler is drift")
}

// TestComputeSchemaDrift_UnstampedUsesTheStampedSet guards the third direction
// against the same conflation. An in_development command that IS invocable
// (LW_CLI_DEV_DOMAINS=1) is stamped, so it is not part of the unstamped
// backlog.
func TestComputeSchemaDrift_UnstampedUsesTheStampedSet(t *testing.T) {
	t.Parallel()

	published := []string{taskList}
	stamped := []string{taskList, adrNew}
	registry := []string{taskList, adrNew}
	surface := []string{taskList, adrNew, "audit.run"} // audit is cobra-only

	_, _, unstamped := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Equal(t, []string{"audit.run"}, unstamped,
		"only the genuinely undeclared cobra-tree command is unstamped")
}

func TestComputeSchemaDrift_SortsEveryDirection(t *testing.T) {
	t.Parallel()

	published := []string{"b.two", "a.one"}
	stamped := []string{"b.two", "a.one"}
	registry := []string{"z.orphan", "m.orphan"}
	surface := []string{"z.surface", "a.surface"}

	missing, orphaned, unstamped := cli.ComputeSchemaDrift(published, stamped, registry, surface)

	assert.Equal(t, []string{"a.one", "b.two"}, missing)
	assert.Equal(t, []string{"m.orphan", "z.orphan"}, orphaned)
	assert.Equal(t, []string{"a.surface", "z.surface"}, unstamped)
}
