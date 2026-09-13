package cli

import (
	"context"
	"errors"
)

// Schema-driven spec handlers. commands.yaml v3.0.0 declares 5 commands:
// list, show, generate-tasks, coverage, history.
//
// list + generate-tasks shell to lightwave-core management commands. show,
// coverage, history are not yet backed by management commands — surface the
// gap rather than no-op so the missing entrypoint is visible.

func init() {
	RegisterHandler("spec.list", specListHandler)
	RegisterHandler("spec.show", specShowHandler)
	RegisterHandler("spec.generate-tasks", specGenerateTasksHandler)
	RegisterHandler("spec.coverage", specCoverageHandler)
	RegisterHandler("spec.history", specHistoryHandler)
}

func specListHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errDjangoRetired("spec list", "The agile-artifact store is PostgreSQL behind the Go API; the spec domain is already listed in DecommissionedCommands as a legacy parked tree pending schema merge.")
}

func specShowHandler(_ context.Context, args []string, _ map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw spec show <requirement_id>")
	}

	return errors.New("spec show: not yet wired (no spec_show management command — filter `lw spec list` output for now)")
}

func specGenerateTasksHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errDjangoRetired("spec generate-tasks", "The agile-artifact store is PostgreSQL behind the Go API; the spec domain is already listed in DecommissionedCommands as a legacy parked tree pending schema merge.")
}

func specCoverageHandler(_ context.Context, args []string, _ map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw spec coverage <domain>")
	}

	return errors.New("spec coverage: not yet wired (no spec_coverage management command — track via `lw schema coverage`)")
}

func specHistoryHandler(_ context.Context, args []string, _ map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw spec history <spec_path>")
	}

	return errors.New("spec history: not yet wired (no spec_history management command — use `git log` against the spec path for now)")
}
