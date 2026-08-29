package sst

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	lightwavecore "github.com/lightwave-media/lightwave-core/bindings/go"
)

// EnvLiveSchemas forces reads to come from a lightwave-core checkout even when
// the embedded binding could serve them. Set it while editing schemas so `lw`
// sees the edit without a dependency bump.
const EnvLiveSchemas = "LW_CLI_LIVE_SCHEMAS"

// SchemaBytes returns the raw YAML for a stamp schema by its registry key —
// the path under lightwave-core/src/schemas without the .yaml suffix, e.g.
// "interfaces/cli/commands".
//
// lw used to read the stamp only from a sibling checkout at
// <lightwaveRoot>/lightwave-core/src/schemas/... That single assumption is why
// the surface gate has to skip without a checkout, why schema-drift CI mints a
// token just to clone core, and why `lw` cannot run on a machine that has only
// `lw`. lightwave-core ships bindings/go, a module that embeds the whole schema
// tree, so the checkout can be a preference rather than a requirement.
//
// Resolution order, and why it is this way round:
//
//  1. A checkout, when present. The embedded copy is a snapshot pinned in
//     go.mod and CAN lag core; a checkout is by definition current. Preferring
//     it means adopting the binding never regresses anyone who has core.
//  2. The embedded binding otherwise, so a machine with no checkout still works.
//
// LW_CLI_LIVE_SCHEMAS=1 pins step 1 and makes a missing checkout an error
// rather than a silent fall-through to a possibly older snapshot.
func SchemaBytes(lightwaveRoot, key string) ([]byte, error) {
	live := filepath.Join(lightwaveRoot, "lightwave-core", "src", "schemas", key+".yaml")

	data, liveErr := os.ReadFile(live)
	if liveErr == nil {
		return data, nil
	}

	if os.Getenv(EnvLiveSchemas) == "1" {
		return nil, fmt.Errorf(
			"%s=1 but the lightwave-core checkout is unreadable at %s: %w",
			EnvLiveSchemas, live, liveErr)
	}

	embedded, embErr := lightwavecore.ReadSchema(key)
	if embErr != nil {
		// Report the checkout path too: "schema not found" is confusing when
		// the real cause is that core simply is not checked out here.
		return nil, fmt.Errorf("stamp %q unavailable: checkout %s: %w; embedded: %w",
			key, live, liveErr, embErr)
	}

	warnEmbeddedOnce()

	return embedded, nil
}

// EmbeddedStampVersion is the schema version the linked binding carries. It is
// what `lw` falls back to, so it belongs in `lw version` output and in any
// report that claims to describe the stamp.
func EmbeddedStampVersion() string { return lightwavecore.Version }

var embeddedWarn sync.Once

// warnEmbeddedOnce says, once per process, that the stamp came from the
// snapshot rather than a checkout.
//
// This is deliberately noisy. A snapshot that silently answers stamp questions
// is the failure this package exists to avoid: a gate reading an old stamp
// reports confidently and wrongly, which is worse than reporting nothing.
func warnEmbeddedOnce() {
	embeddedWarn.Do(func() {
		fmt.Fprintf(os.Stderr,
			"lw: no lightwave-core checkout; using the embedded stamp snapshot (v%s). "+
				"Schema-dependent results reflect that snapshot, not core's current state.\n",
			lightwavecore.Version)
	})
}
