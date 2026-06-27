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

func specListHandler(ctx context.Context, _ []string, flags map[string]any) error {
	args := []string{"spec_list"}
	if t := flagStr(flags, "type"); t != "" {
		args = append(args, "--type", t)
	}

	if d := flagStr(flags, "domain"); d != "" {
		args = append(args, "--domain", d)
	}

	if flagBool(flags, "compliance") {
		args = append(args, "--compliance")
	}

	return djangoManage(ctx, args...)
}

func specShowHandler(_ context.Context, args []string, _ map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw spec show <requirement_id>")
	}

	return errors.New("spec show: not yet wired (no spec_show management command — filter `lw spec list` output for now)")
}

func specGenerateTasksHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return errors.New("usage: lw spec generate-tasks <spec_path>")
	}

	mgmt := []string{"spec_generate_tasks", args[0]}
	if flagBool(flags, "dry-run") {
		mgmt = append(mgmt, "--dry-run")
	}

	if flagBool(flags, "with-goal-tests") {
		mgmt = append(mgmt, "--with-goal-tests")
	}

	if e := flagStr(flags, "epic"); e != "" {
		mgmt = append(mgmt, "--epic", e)
	}

	if s := flagStr(flags, "story"); s != "" {
		mgmt = append(mgmt, "--story", s)
	}

	return djangoManage(ctx, mgmt...)
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
