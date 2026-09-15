//nolint:testpackage // resets the unexported cfg singleton, which is the whole point
package config

import (
	"sync"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolate gives a test its own HOME and leaves viper as it found it.
//
// Reset() clears the cfg singleton and NOT viper's package globals, and Load()
// calls viper.AddConfigPath, which APPENDS. So a test that loads under its own
// HOME permanently adds that directory to viper's search order, and a later
// test loading under a different HOME can resolve to the earlier one's config.
// That is a real defect in Load rather than a test artifact — tracked
// separately — but it is not this file's subject, so these tests do not leave
// it behind for the next one.
func isolate(t *testing.T) {
	t.Helper()

	t.Setenv("HOME", t.TempDir())

	viper.Reset()
	Reset()

	t.Cleanup(func() {
		viper.Reset()
		Reset()
	})
}

// #400: Get() was an unguarded lazy singleton.
//
//	func Get() *Config {
//	    if cfg == nil {
//	        cfg, _ = Load()
//	    }
//	    return cfg
//	}
//
// No mutex and no sync.Once existed anywhere in config.go, so two goroutines
// whose FIRST Get() landed together both saw nil, both called Load(), and both
// wrote the package global plus viper's own global state.
//
// It stayed hidden because the race needs two concurrent first calls: in a full
// package run some earlier test almost always loads config single-threaded,
// after which cfg is non-nil forever. The reproduction on the issue
// (`go test -race -run TestMCP ./internal/cli/`, 83 race reports) no longer
// fires — the test set around it shifted — which is exactly why this control
// lives next to the code instead of depending on the ordering of a package
// three directories away. A detector that stopped firing is not a fixed bug.
//
//nolint:paralleltest // mutates the package-level singleton
func TestGetIsSafeWhenFirstCallsRaceEachOther(t *testing.T) {
	isolate(t)

	const callers = 32

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []*Config
	)

	for range callers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			got := Get()

			mu.Lock()
			results = append(results, got)
			mu.Unlock()
		}()
	}

	wg.Wait()

	require.Len(t, results, callers)

	// One config, shared. Anything else means two loads published two objects
	// and half the callers are reading a different one from the other half.
	first := results[0]
	require.NotNil(t, first, "Get() must never hand back nil")

	for i, got := range results {
		assert.Samef(t, first, got, "caller %d got a different *Config; the singleton loaded twice", i)
	}
}

// TestLoadRacingGetPublishesOneConfig covers the other entry point. root.go
// calls Load() directly at startup while every handler calls Get(), so the two
// paths can be in flight at once and both used to write cfg.
//
//nolint:paralleltest // mutates the package-level singleton
func TestLoadRacingGetPublishesOneConfig(t *testing.T) {
	isolate(t)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen []*Config
	)

	record := func(c *Config) {
		mu.Lock()
		seen = append(seen, c)
		mu.Unlock()
	}

	for i := range 16 {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			if n%2 == 0 {
				loaded, err := Load()
				if err == nil {
					record(loaded)
				}

				return
			}

			record(Get())
		}(i)
	}

	wg.Wait()

	require.NotEmpty(t, seen)

	for i, got := range seen {
		assert.Samef(t, seen[0], got, "caller %d saw a second *Config; Load and Get both published", i)
	}
}

// TestFailedLoadIsNotCachedAsValid — the publish-too-early half.
//
// Load assigned `cfg = &Config{}` and THEN unmarshalled and validated into it,
// returning the error but leaving the half-built value cached. The next Load()
// short-circuits on `cfg != nil` and returns it with a nil error, so a config
// that failed validation is served as valid from then on, silently, for the
// life of the process.
//
//nolint:paralleltest // mutates the package-level singleton
func TestFailedLoadIsNotCachedAsValid(t *testing.T) {
	isolate(t)

	// A port that cannot parse takes Database.Validate down the failure path.
	t.Setenv("LW_DB_PORT", "not-a-port")

	if _, err := Load(); err == nil {
		t.Skip("this environment's config still validates; the caching claim needs a failing load to be meaningful")
	}

	// The failure must not have left anything behind for the next caller to
	// find and trust.
	second, err := Load()
	require.Error(t, err, "a load that failed once must fail again, not return a cached half-built config")
	assert.Nil(t, second, "nothing may be published from a load that did not complete")
}
