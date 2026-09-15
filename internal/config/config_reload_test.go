//nolint:testpackage // resets the unexported cfg singleton
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfigHome creates a HOME containing ~/.config/lw/config.yaml with the
// given database host, and returns it.
func writeConfigHome(t *testing.T, host string) string {
	t.Helper()

	home := t.TempDir()
	dir := filepath.Join(home, ".config", "lw")

	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "config.yaml"),
		[]byte("database:\n  host: "+host+"\n"),
		0o600,
	))

	return home
}

// TestReloadReadsTheCurrentHomeNotThePreviousOne is the #459 regression pin.
//
// Load() called viper.AddConfigPath on the package-global viper, and
// AddConfigPath APPENDS — it never replaces. Reset() clears only the cfg
// singleton, so a second load kept the first load's directories in the search
// list AHEAD of its own, and ReadInConfig takes the first match. Loading under
// a second HOME therefore returned the FIRST HOME's config.
//
// This is not a test-only concern. `Set()` writes the config file and then
// calls Reset() precisely so the next Get() re-reads it, and that contract
// depends on the second load searching the same place as the first. It stayed
// invisible in production only because $HOME does not change inside a normal
// `lw` invocation.
//
// It was found the way these usually are — an unrelated test in this package
// started failing while passing in isolation.
//
//nolint:paralleltest // t.Setenv and the package singleton
func TestReloadReadsTheCurrentHomeNotThePreviousOne(t *testing.T) {
	first := writeConfigHome(t, "10.0.0.1")
	second := writeConfigHome(t, "10.0.0.2")

	t.Cleanup(Reset)

	t.Setenv("HOME", first)
	Reset()

	loaded, err := Load()
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", loaded.Database.Host, "the first load must read the first HOME")

	// Exactly what Set() does: change what is on disk, then Reset so the next
	// caller re-reads it.
	t.Setenv("HOME", second)
	Reset()

	reloaded, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.2", reloaded.Database.Host,
		"the reload resolved the PREVIOUS home's config; viper's search path accumulated across loads")
}

// TestLoadLeavesTheGlobalViperAlone — the mechanism, pinned directly rather
// than only through its symptom. Nothing outside this package reads the viper
// global, so a load that writes to it can only leak into other tests; the
// per-load instance is what stops that.
//
//nolint:paralleltest // t.Setenv and the package singleton
func TestLoadLeavesTheGlobalViperAlone(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Cleanup(Reset)

	t.Setenv("HOME", writeConfigHome(t, "10.0.0.3"))
	Reset()

	_, err := Load()
	require.NoError(t, err)

	assert.Empty(t, viper.GetString("database.host"),
		"Load must not write the viper package global; it did, and that is how one test's HOME reached another's")
	assert.Empty(t, viper.ConfigFileUsed(),
		"nor may it leave the global pointed at a config file")
}

// TestLoadReportsAnUnreadableConfigRatherThanIgnoringIt covers the one branch
// in Load that distinguishes "there is no config" from "the config is broken":
//
//	if err := v.ReadInConfig(); err != nil {
//	    var notFound viper.ConfigFileNotFoundError
//	    if !errors.As(err, &notFound) { return nil, … }
//	    // absent is fine — defaults + env
//	}
//
// Absent must fall through to defaults; malformed must not. Collapsing the two
// would mean a config file with a typo in it silently becomes "no config", and
// the operator sees defaults with no indication their file was ignored — the
// same shape as every other defect in this package's history, where a refusal
// to answer got read as an answer.
//
// This also pins the errors.As rewrite: the old code type-asserted the error
// directly, which misses a wrapped ConfigFileNotFoundError and would send an
// absent config down the failure path.
//
//nolint:paralleltest // t.Setenv and the package singleton
func TestLoadReportsAnUnreadableConfigRatherThanIgnoringIt(t *testing.T) {
	t.Cleanup(Reset)

	home := t.TempDir()
	dir := filepath.Join(home, ".config", "lw")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "config.yaml"),
		[]byte("database:\n  host: [unclosed\n   nope: : :\n"),
		0o600,
	))

	t.Setenv("HOME", home)
	Reset()

	loaded, err := Load()
	require.Error(t, err, "a malformed config must fail the load, not be mistaken for an absent one")
	assert.Nil(t, loaded, "nothing may be published from a load that did not complete")
	assert.Contains(t, err.Error(), "error reading config",
		"the error must say the file could not be read, so the operator looks at the file")

	// And the other direction: genuinely absent is NOT an error.
	t.Setenv("HOME", t.TempDir())
	Reset()

	fromDefaults, err := Load()
	require.NoError(t, err, "no config file at all is the normal case and must fall through to defaults")
	require.NotNil(t, fromDefaults)
	assert.Equal(t, "localhost", fromDefaults.Database.Host, "defaults still apply when no file exists")
}
