package cli

import (
	"context"
	"errors"
)

// The handler stays registered because the embedded lightwave-core schema still
// declares `scrum sync`; dropping it would read as schema↔handler drift.
func init() {
	RegisterHandler("scrum.sync", scrumSyncHandler)
}

func scrumSyncHandler(_ context.Context, _ []string, _ map[string]any) error {
	return errors.New("scrum sync is decommissioned: the Lightwave Swarm project board it reconciled is closed; nothing was synced")
}
