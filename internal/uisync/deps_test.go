package uisync_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/uisync"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// depFixture builds a lightwave-ui checkout that reproduces the real transitive
// import graph the copy-in used to break on: Avatar → tooltip (component) +
// cx (util); dropdown → avatar/checkbox/radio/toggle (siblings) + cx; badges →
// dot-icon (a loose category-level component file) + cx.
func depFixture(t *testing.T) (uiRepo string) {
	t.Helper()
	uiRepo = t.TempDir()

	comp := func(rel, body string) {
		writeFile(t, filepath.Join(uiRepo, "src", "components", filepath.FromSlash(rel)), body)
	}

	comp("base/avatar/avatar.tsx", `import { cx } from "@/utils/cx";
import { AvatarAddButton } from "./base-components/avatar-add-button";
export const Avatar = () => null;
`)
	comp("base/avatar/base-components/avatar-add-button.tsx", `import {
  Tooltip as AriaTooltip,
} from "@/components/base/tooltip/tooltip";
import { cx } from "@/utils/cx";
export const AvatarAddButton = () => null;
`)
	comp("base/tooltip/tooltip.tsx", `import { cx } from "@/utils/cx";
export const Tooltip = () => null;
`)
	comp("base/dropdown/dropdown.tsx", `import { cx } from "@/utils/cx";
import { Avatar } from "../avatar/avatar";
import { CheckboxBase } from "../checkbox/checkbox";
import { RadioButtonBase } from "../radio-buttons/radio-buttons";
import { ToggleBase } from "../toggle/toggle";
export const Dropdown = () => null;
`)
	comp("base/checkbox/checkbox.tsx", `import { cx } from "@/utils/cx";
export const CheckboxBase = () => null;
`)
	comp("base/radio-buttons/radio-buttons.tsx", `import { cx } from "@/utils/cx";
export const RadioButtonBase = () => null;
`)
	comp("base/toggle/toggle.tsx", `import { cx } from "@/utils/cx";
export const ToggleBase = () => null;
`)
	// Badge's directory and exported symbol are plural; it also depends on a
	// loose single-file component that lives directly under a category.
	comp("base/badges/badges.tsx", `import { Dot } from "@/components/foundations/dot-icon";
import { cx } from "@/utils/cx";
export const Badge = () => null;
`)
	comp("foundations/dot-icon.tsx", `import { cx } from "@/utils/cx";
export const Dot = () => null;
`)

	writeFile(t, filepath.Join(uiRepo, "src", "utils", "cx.ts"), `import { extendTailwindMerge } from "tailwind-merge";
export const cx = extendTailwindMerge({});
`)

	return uiRepo
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)

	return err == nil
}

func TestAddResolvesTransitiveComponentAndUtilDeps(t *testing.T) {
	t.Parallel()
	uiRepo := depFixture(t)
	siteDir := t.TempDir()

	copied, err := uisync.Add(uiRepo, siteDir, "Avatar", "8.0.0", false, false, fixedNow)
	require.NoError(t, err)

	// Component dep (via @/components import) and its own transitive util dep
	// land without a single manual add.
	assert.True(t, exists(t, filepath.Join(siteDir, "src", "components", "base", "avatar", "avatar.tsx")), "named component copied")
	assert.True(t, exists(t, filepath.Join(siteDir, "src", "components", "base", "tooltip", "tooltip.tsx")), "transitive component dep copied")
	assert.True(t, exists(t, filepath.Join(siteDir, "src", "utils", "cx.ts")), "util dep copied")

	// Every component (named + transitive) is pinned; utils are not.
	lock, err := uisync.ReadLock(siteDir)
	require.NoError(t, err)

	_, ok := lock.Find("component", "Avatar")
	assert.True(t, ok, "named component pinned")

	tooltipPin, ok := lock.Find("component", "base/tooltip")
	assert.True(t, ok, "transitive component pinned by unit path")
	assert.Equal(t, "8.0.0", tooltipPin.LightwaveUIVersion)

	for _, p := range lock.Components {
		assert.NotEqual(t, "utils/cx.ts", p.Name, "utils are copied, not pinned")
	}

	assert.NotEmpty(t, copied)
}

func TestAddCascadesThroughRelativeSiblingImports(t *testing.T) {
	t.Parallel()
	uiRepo := depFixture(t)
	siteDir := t.TempDir()

	_, err := uisync.Add(uiRepo, siteDir, "base/dropdown", "8.0.0", false, false, fixedNow)
	require.NoError(t, err)

	// dropdown.tsx's relative sibling imports (../checkbox, ../radio-buttons,
	// ../toggle, ../avatar) all resolve — and avatar drags in tooltip + cx.
	for _, unit := range []string{"base/dropdown", "base/avatar", "base/checkbox", "base/radio-buttons", "base/toggle", "base/tooltip"} {
		assert.True(t, exists(t, filepath.Join(siteDir, "src", "components", filepath.FromSlash(unit))), unit+" copied")
	}

	assert.True(t, exists(t, filepath.Join(siteDir, "src", "utils", "cx.ts")), "util dep copied")

	lock, err := uisync.ReadLock(siteDir)
	require.NoError(t, err)

	for _, name := range []string{"base/dropdown", "base/avatar", "base/checkbox", "base/radio-buttons", "base/toggle", "base/tooltip"} {
		_, ok := lock.Find("component", name)
		assert.True(t, ok, name+" pinned")
	}
}

func TestAddLooseCategoryFileDep(t *testing.T) {
	t.Parallel()
	uiRepo := depFixture(t)
	siteDir := t.TempDir()

	_, err := uisync.Add(uiRepo, siteDir, "base/badges", "8.0.0", false, false, fixedNow)
	require.NoError(t, err)

	// A single-file component directly under a category (foundations/dot-icon)
	// is copied as a file, not as the whole foundations category.
	assert.True(t, exists(t, filepath.Join(siteDir, "src", "components", "foundations", "dot-icon.tsx")), "loose component file copied")
	assert.False(t, exists(t, filepath.Join(siteDir, "src", "components", "foundations", "featured-icon")), "sibling category entries not dragged in")

	lock, err := uisync.ReadLock(siteDir)
	require.NoError(t, err)

	_, ok := lock.Find("component", "base/badges")
	assert.True(t, ok, "named component pinned")

	for _, p := range lock.Components {
		assert.NotEqual(t, "foundations/dot-icon", p.Name, "loose file deps are copied, not pinned")
		assert.NotEqual(t, "components/foundations/dot-icon.tsx", p.Name)
	}
}

func TestAddNoDepsSkipsResolution(t *testing.T) {
	t.Parallel()
	uiRepo := depFixture(t)
	siteDir := t.TempDir()

	_, err := uisync.Add(uiRepo, siteDir, "Avatar", "8.0.0", false, true, fixedNow)
	require.NoError(t, err)

	assert.True(t, exists(t, filepath.Join(siteDir, "src", "components", "base", "avatar", "avatar.tsx")), "named component still copied")
	assert.False(t, exists(t, filepath.Join(siteDir, "src", "components", "base", "tooltip", "tooltip.tsx")), "--no-deps skips transitive components")
	assert.False(t, exists(t, filepath.Join(siteDir, "src", "utils", "cx.ts")), "--no-deps skips util deps")

	lock, err := uisync.ReadLock(siteDir)
	require.NoError(t, err)
	assert.Len(t, lock.Components, 1, "only the named component is pinned")
}

func TestResolveComponentDirPluralization(t *testing.T) {
	t.Parallel()
	uiRepo := depFixture(t)

	got, err := uisync.ResolveComponentDir(uiRepo, "Badge")
	require.NoError(t, err)
	assert.Equal(t, "base/badges", got, "singular PascalCase resolves to the plural directory")
}

func TestResolveComponentDirExportedSymbol(t *testing.T) {
	t.Parallel()
	uiRepo := t.TempDir()
	// Directory and file basenames match neither the kebab nor its plural; only
	// the exported symbol identifies the unit.
	writeFile(t, filepath.Join(uiRepo, "src", "components", "application", "grid", "layout.tsx"),
		"export const DataGrid = () => null;\n")

	got, err := uisync.ResolveComponentDir(uiRepo, "DataGrid")
	require.NoError(t, err)
	assert.Equal(t, "application/grid", got, "name resolves via the exported-symbol map")
}
