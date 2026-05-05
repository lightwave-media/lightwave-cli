package cli

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Handler is the canonical signature for a schema-driven `lw` command.
// All schema-registered commands implement this. The flags map is populated
// by the dispatcher from cobra-parsed values; key is the long flag name
// without the leading "--".
type Handler func(ctx context.Context, args []string, flags map[string]any) error

var (
	handlerRegistryMu sync.RWMutex
	handlerRegistry   = map[string]Handler{}
)

// RegisterHandler binds a Handler to a "<domain>.<command>" key.
// Call from a file-level init(): RegisterHandler("task.list", taskList).
// Duplicate registration panics — drift between source files would be silent
// and dangerous, so we fail fast at startup.
func RegisterHandler(key string, h Handler) {
	handlerRegistryMu.Lock()
	defer handlerRegistryMu.Unlock()

	if _, exists := handlerRegistry[key]; exists {
		panic(fmt.Sprintf("RegisterHandler: duplicate key %q", key))
	}
	if h == nil {
		panic(fmt.Sprintf("RegisterHandler: nil handler for %q", key))
	}
	handlerRegistry[key] = h
}

// LookupHandler returns the registered Handler for a key, or (nil, false).
func LookupHandler(key string) (Handler, bool) {
	handlerRegistryMu.RLock()
	defer handlerRegistryMu.RUnlock()
	h, ok := handlerRegistry[key]
	return h, ok
}

// RegisteredKeys returns a sorted snapshot of all registered handler keys.
// Used by `lw check schema` to compute drift against the SST.
func RegisteredKeys() []string {
	handlerRegistryMu.RLock()
	defer handlerRegistryMu.RUnlock()

	keys := make([]string, 0, len(handlerRegistry))
	for k := range handlerRegistry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
