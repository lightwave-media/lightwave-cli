//nolint:testpackage // exercises the unexported handlers and errDjangoRetired
package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #318. Fourteen handlers across four files ran
// `docker compose exec backend python manage.py <verb>`. The platform is a Go
// monolith (ADR-0018): there is no manage.py and no `backend` compose service,
// so all fourteen had been dead since the migration.
//
// They did not fail cleanly. They failed as a docker error about a missing
// service, which reads like a broken local environment rather than a retired
// command — so the CLI was inviting people to debug their docker setup for a
// capability that no longer exists. These tests pin the replacement behaviour:
// fail immediately, say the backend is retired, and name what replaced it.

func TestDjangoBackedVerbsFailWithoutShellingOut(t *testing.T) {
	t.Parallel()

	// Every handler that used to call djangoManage.
	handlers := map[string]func(context.Context, []string, map[string]any) error{
		"db.migrate":          dbMigrateHandler,
		"db.makemigrations":   dbMakemigrationsHandler,
		"db.check":            dbCheckHandler,
		"db.schema-init":      dbSchemaInitHandler,
		"db.migrate-schemas":  dbMigrateSchemasHandler,
		"spec.list":           specListHandler,
		"spec.generate-tasks": specGenerateTasksHandler,
		"plan.sync":           planSyncHandler,
		"plan.generate":       planGenerateHandler,
		"schema.validate":     schemaValidateHandler,
		"schema.drift":        schemaDriftHandler,
		"schema.reconcile":    schemaReconcileHandler,
	}

	for key, h := range handlers {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			err := h(t.Context(), nil, map[string]any{})
			require.Error(t, err, "a verb with no implementation must not report success")

			msg := err.Error()

			assert.Contains(t, msg, "retired Django backend",
				"the error must say WHY, so nobody debugs their docker setup for it")
			assert.NotContains(t, msg, "docker",
				"failing as a docker error is the behaviour this replaced")

			// The message must carry guidance, not just a refusal. Length is a
			// crude proxy, but it catches a future edit that trims the
			// replacement text down to a bare "not supported".
			assert.Greater(t, len(msg), 120,
				"the error must name what replaced the verb, or say plainly that nothing did")
		})
	}
}

// TestDjangoBackedVerbsDoNotHang guards the property that made the old failure
// expensive: djangoManage wrapped a 15-minute timeout around a docker exec, so a
// dead verb could sit for a quarter of an hour before reporting. These return
// immediately.
func TestDjangoBackedVerbsDoNotHang(t *testing.T) {
	t.Parallel()

	// A cancelled context proves the handler never reaches an exec — if it
	// shelled out it would surface the cancellation, not the retirement notice.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := dbMigrateHandler(ctx, nil, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retired Django backend",
		"the handler must answer from its own knowledge, not from a subprocess")
}

// TestNoDjangoInvocationRemains is the class guard. The individual tests above
// pin the twelve handlers that exist today; this one fails if a fourteenth
// appears, or if someone reintroduces the helper under another name.
//
// Scans git-tracked files only, for the reason #404 had to be fixed: a
// filesystem walk descends into nested worktrees and lints another checkout.
func TestNoDjangoInvocationRemains(t *testing.T) {
	t.Parallel()

	// git knows the root, and this already shells to git. repoRoot lives in
	// package cli_test and is not reachable from here.
	topOut, err := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel").Output()
	require.NoError(t, err, "git rev-parse --show-toplevel")

	root := strings.TrimSpace(string(topOut))

	out, err := exec.CommandContext(t.Context(), "git", "-C", root, "ls-files", "-z", "*.go").Output()
	require.NoError(t, err)

	var offenders []string

	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" || strings.HasSuffix(rel, "django_retired_test.go") {
			continue
		}

		// Read from DISK, not from the index. `git show :<path>` reads the
		// staged blob, so an unstaged fix reads as if it had not been made —
		// the guard would then pass or fail on staging state rather than on
		// the code. git is used only for the file LIST, which is what
		// correctly excludes nested worktrees (#404).
		src, readErr := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // a git-tracked path in our own repo
		if readErr != nil {
			continue // tracked but absent from the worktree — nothing to judge
		}

		for _, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // comments recording the history are the point of the fix
			}

			if strings.Contains(line, "djangoManage") || strings.Contains(line, `"manage.py"`) {
				offenders = append(offenders, rel+": "+trimmed)
			}
		}
	}

	assert.Empty(t, offenders,
		"the Django backend is retired (#318); a handler shelling into manage.py "+
			"fails as a confusing docker error rather than an honest one")
}
