package sitegen_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/sitegen"
	"github.com/lightwave-media/lightwave-cli/internal/uisync"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

var fixedNow = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestInitScaffoldsStampConformantConfig(t *testing.T) {
	t.Parallel()

	uiRepo, siteDir := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(uiRepo, "src", "components", "base", "buttons", "button.tsx"), "button\n")

	written, err := sitegen.Init(uiRepo, siteDir, "2.0.0", sitegen.Options{Domain: "joelschaeffer.site"}, fixedNow)
	require.NoError(t, err)
	assert.Len(t, written, 3, "config + copied component + lock")

	raw, err := os.ReadFile(filepath.Join(siteDir, sitegen.ConfigFile))
	require.NoError(t, err)

	var cfg sitegen.SiteConfig
	require.NoError(t, yaml.Unmarshal(raw, &cfg))

	assert.Equal(t, "joelschaeffer.site", cfg.Domain)
	assert.Equal(t, "joelschaeffer.site", cfg.SiteName, "site_name defaults from domain")
	assert.Equal(t, "en-GB", cfg.Locale, "locale defaults per the stamp")
	assert.Nil(t, cfg.Tenant, "standalone sites have null tenant")
	assert.Equal(t, "https://media.joelschaeffer.site", cfg.DataSources.MediaBase)
	assert.Equal(t, []string{""}, cfg.Pages, "manifest starts with the home page")

	require.Len(t, cfg.UIRelease.Components, 1, "ui_release must be min(1)-valid from birth")
	assert.Equal(t, "Button", cfg.UIRelease.Components[0].Name)
	assert.Equal(t, "2.0.0", cfg.UIRelease.LightwaveUIVersion)

	lock, err := uisync.ReadLock(siteDir)
	require.NoError(t, err)
	assert.Equal(t, cfg.UIRelease, *lock, "config snapshot must equal the lock at init")
}

func TestInitRefusesReinit(t *testing.T) {
	t.Parallel()

	uiRepo, siteDir := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(uiRepo, "src", "components", "base", "buttons", "button.tsx"), "button\n")

	_, err := sitegen.Init(uiRepo, siteDir, "2.0.0", sitegen.Options{Domain: "a.site"}, fixedNow)
	require.NoError(t, err)

	_, err = sitegen.Init(uiRepo, siteDir, "2.0.0", sitegen.Options{Domain: "b.site"}, fixedNow)
	require.Error(t, err, "re-init over a configured site must refuse")
	assert.Contains(t, err.Error(), "already initialized")
}

// A real mid-migration site already has the first component vendored. init
// must refuse to clobber it by default, but --force graduates it (the explicit
// adoption) and still scaffolds the config.
func TestInitForceGraduatesExistingVendoredComponent(t *testing.T) {
	t.Parallel()

	uiRepo, siteDir := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(uiRepo, "src", "components", "base", "buttons", "button.tsx"), "fork-button\n")
	writeFile(t, filepath.Join(siteDir, "src", "components", "base", "buttons", "button.tsx"), "vendored-button\n")

	_, err := sitegen.Init(uiRepo, siteDir, "2.0.0", sitegen.Options{Domain: "a.site"}, fixedNow)
	require.Error(t, err, "init must not silently overwrite a vendored component")
	assert.Contains(t, err.Error(), "force", "the error must point at --force")

	written, err := sitegen.Init(uiRepo, siteDir, "2.0.0", sitegen.Options{Domain: "a.site", Force: true}, fixedNow)
	require.NoError(t, err)
	assert.Contains(t, written, filepath.Join(siteDir, sitegen.ConfigFile))

	lock, err := uisync.ReadLock(siteDir)
	require.NoError(t, err)
	require.Len(t, lock.Components, 1, "ui_release stays min(1)-valid")
	assert.Equal(t, "Button", lock.Components[0].Name)
}

// When the first component is already pinned (e.g. a prior `lw ui add`), init
// must be idempotent: detect the pin, skip the add (so it doesn't collide on
// the existing dir), and still write a min(1)-valid config.
func TestInitSkipsAddWhenComponentAlreadyPinned(t *testing.T) {
	t.Parallel()

	uiRepo, siteDir := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(uiRepo, "src", "components", "base", "buttons", "button.tsx"), "button\n")
	writeFile(t, filepath.Join(siteDir, "src", "components", "base", "buttons", "button.tsx"), "button\n")

	seeded := &uisync.Lock{}
	seeded.Upsert(uisync.Pin{Kind: "component", Name: "Button", LightwaveUIVersion: "2.0.0", SyncedAt: "2026-06-12T12:00:00Z"})
	require.NoError(t, uisync.WriteLock(siteDir, seeded))

	written, err := sitegen.Init(uiRepo, siteDir, "2.0.0", sitegen.Options{Domain: "a.site"}, fixedNow)
	require.NoError(t, err, "init must be idempotent when the first component is already pinned")
	assert.Contains(t, written, filepath.Join(siteDir, sitegen.ConfigFile))

	raw, err := os.ReadFile(filepath.Join(siteDir, sitegen.ConfigFile))
	require.NoError(t, err)

	var cfg sitegen.SiteConfig
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	require.Len(t, cfg.UIRelease.Components, 1)
	assert.Equal(t, "Button", cfg.UIRelease.Components[0].Name)
}
