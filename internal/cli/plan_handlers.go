package cli

import (
	"context"
)

// Schema-driven plan handlers. commands.yaml v3.0.0 declares 2 commands:
// sync, generate. Both shell to Django management commands that own the
// canonical sync logic between .claude/plans/ and createOS task plans.

func init() {
	RegisterHandler("plan.sync", planSyncHandler)
	RegisterHandler("plan.generate", planGenerateHandler)
}

func planSyncHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errDjangoRetired("plan sync", "Plans are agile artifacts in PostgreSQL behind the Go API, not Django management commands. No lw verb owns the sync yet.")
}

func planGenerateHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errDjangoRetired("plan generate", "Plans are agile artifacts in PostgreSQL behind the Go API, not Django management commands. No lw verb owns generation yet.")
}
