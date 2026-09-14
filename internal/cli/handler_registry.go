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

	// retiredFlagUsage maps "<domain>.<command>" to flag name to the help text
	// that flag should carry. See RegisterRetiredFlags.
	retiredFlagUsage = map[string]map[string]string{}
)

// RegisterRetiredFlags gives help text to flags a command's schema still
// declares but no longer implements.
//
// #351 retired the Paperclip leg and made its ten flags fail loudly rather than
// be silently ignored — deliberately NOT deleting them from commands.yaml,
// because which of them carry intent worth re-homing is a product call and
// deleting decides it by omission. That left `lw task create --help` listing
// ten flags that always error, indistinguishable from the ones that work:
// the dispatcher gives every schema flag an empty usage string, so `--prd` and
// `--label` printed identically.
//
// So the help says which is which. The flags stay declared, the decision stays
// open and visible, and nobody reads the surface as a promise it cannot keep.
// Hiding them (cobra's MarkDeprecated) was the alternative and is a softer form
// of the same omission — out of sight is not the same as decided.
//
// Call from a file-level init(), beside RegisterHandler. Registering a flag the
// schema does not declare is not an error here: the stamp is the authority for
// what exists, and a test asserts the two agree.
func RegisterRetiredFlags(key string, usageByFlag map[string]string) {
	handlerRegistryMu.Lock()
	defer handlerRegistryMu.Unlock()

	if retiredFlagUsage[key] == nil {
		retiredFlagUsage[key] = map[string]string{}
	}

	for name, usage := range usageByFlag {
		retiredFlagUsage[key][name] = usage
	}
}

// usageForFlag returns a retired flag's help text, or "" for a live flag.
func usageForFlag(key, name string) string {
	handlerRegistryMu.RLock()
	defer handlerRegistryMu.RUnlock()

	return retiredFlagUsage[key][name]
}

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
