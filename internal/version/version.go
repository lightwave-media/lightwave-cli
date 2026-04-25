package version

import (
	"maps"
	"sync"
)

// Set via ldflags at build time. GoReleaser injects these automatically from git tags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// API versions tracked per subsystem. Subsystems whose contract is consumed by
// external callers (plugins, scripts, CI) register a positive integer here and
// bump it on any breaking change to argument shape, output schema, or
// subcommand layout. Consumers compare against a pinned minimum.
//
// This is independent of the binary's release `Version` — release tags can
// move without breaking any subsystem, and subsystem APIs can break between
// patch releases.
var (
	apisMu sync.RWMutex
	apis   = map[string]int{}
)

// RegisterAPI declares the current API version of a subsystem. Call from
// init() in the subsystem's command file. Last write wins; usually called once.
func RegisterAPI(subsystem string, version int) {
	apisMu.Lock()
	defer apisMu.Unlock()
	apis[subsystem] = version
}

// APIs returns a copy of the registered API versions, safe for serialization.
func APIs() map[string]int {
	apisMu.RLock()
	defer apisMu.RUnlock()
	out := make(map[string]int, len(apis))
	maps.Copy(out, apis)
	return out
}
