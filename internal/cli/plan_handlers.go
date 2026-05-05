package cli

import (
	"context"
	"fmt"
)

// Schema-driven plan handlers. commands.yaml v3.0.0 declares 2 commands:
// sync, generate. Both shell to Django management commands that own the
// canonical sync logic between .claude/plans/ and createOS task plans.

func init() {
	RegisterHandler("plan.sync", planSyncHandler)
	RegisterHandler("plan.generate", planGenerateHandler)
}

func planSyncHandler(ctx context.Context, _ []string, flags map[string]any) error {
	args := []string{"plan_sync"}
	if t := flagStr(flags, "task"); t != "" {
		args = append(args, "--task", t)
	}
	if flagBool(flags, "pull") {
		args = append(args, "--pull")
	}
	if flagBool(flags, "push") {
		args = append(args, "--push")
	}
	return djangoManage(ctx, args...)
}

func planGenerateHandler(ctx context.Context, args []string, flags map[string]any) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: lw plan generate <task_id> [--from-prelim]")
	}
	mgmt := []string{"plan_generate", args[0]}
	if flagBool(flags, "from-prelim") {
		mgmt = append(mgmt, "--from-prelim")
	}
	return djangoManage(ctx, mgmt...)
}
