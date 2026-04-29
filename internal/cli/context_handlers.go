package cli

import (
	"context"
	"fmt"
)

// Schema-driven context handlers. commands.yaml v3.0.0 declares 3 commands:
// init, refresh, show. These compose the 6-layer system prompt described
// in packages/lightwave-core/lightwave/schema/definitions/ai/agents/context_layers.yaml
// (constitution -> agent -> platform -> user -> session -> search).
//
// The composer itself does not yet exist as a single entrypoint — the
// layers are assembled by Paperclip at heartbeat dispatch. These handlers
// are registered to keep the schema/registry symmetric, but surface the
// gap so the missing composer is visible rather than silently no-op'd.

func init() {
	RegisterHandler("context.init", contextInitHandler)
	RegisterHandler("context.refresh", contextRefreshHandler)
	RegisterHandler("context.show", contextShowHandler)
}

func contextInitHandler(_ context.Context, _ []string, _ map[string]any) error {
	return fmt.Errorf("context init: not yet wired (6-layer composer per ai/agents/context_layers.yaml — needs lightwave-core entrypoint before this can shell to it)")
}

func contextRefreshHandler(_ context.Context, _ []string, _ map[string]any) error {
	return fmt.Errorf("context refresh: not yet wired (depends on context init composer)")
}

func contextShowHandler(_ context.Context, _ []string, _ map[string]any) error {
	return fmt.Errorf("context show: not yet wired (depends on context init composer)")
}
