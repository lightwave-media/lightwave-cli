package uisync_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/uisync"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// importRe mirrors the package's import scanner for assertion purposes.
var importRe = regexp.MustCompile(`(?:\bfrom|\bimport|\bexport)\s*\(?\s*['"]([^'"]+)['"]`)

var acceptanceExts = []string{".ts", ".tsx", ".jsx", ".js", ".mts", ".cts"}

// TestAcceptanceRealCheckout is the end-to-end proof of the fix: against the
// actual lightwave-ui checkout, `lw ui add Avatar` and `lw ui add base/dropdown`
// each produce a consumer (with the `@/` → src alias) in which every internal
// `@/...` and relative import resolves to a copied file — i.e. the project
// builds with zero manual dependency or util additions. Skips when the checkout
// is absent (CI, fresh machines), like testutil's DB-backed tests.
func TestAcceptanceRealCheckout(t *testing.T) {
	t.Parallel()

	uiRepo := lightwaveUICheckout(t)
	version := realVersion(t, uiRepo)

	for _, ref := range []string{"Avatar", "base/dropdown"} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			siteDir := t.TempDir()

			_, err := uisync.Add(uiRepo, siteDir, ref, version, false, false, fixedNow)
			require.NoError(t, err, "lw ui add %s", ref)

			siteSrc := filepath.Join(siteDir, "src")

			var dangling []string

			err = filepath.WalkDir(siteSrc, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}

				if d.IsDir() || !hasExt(path, acceptanceExts) {
					return nil
				}

				raw, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}

				for _, m := range importRe.FindAllSubmatch(raw, -1) {
					spec := string(m[1])
					if !resolvesInSite(siteSrc, path, spec) {
						dangling = append(dangling, filepath.Base(path)+" → "+spec)
					}
				}

				return nil
			})
			require.NoError(t, err)

			assert.Empty(t, dangling, "every internal import must resolve to a copied file; unresolved: %v", dangling)
		})
	}
}

// resolvesInSite reports whether an import specifier resolves to a file or
// directory that was copied into the consumer. Bare specifiers (node_modules)
// are out of scope and always pass.
func resolvesInSite(siteSrc, importerPath, spec string) bool {
	var modRel string

	switch {
	case strings.HasPrefix(spec, "@/"):
		modRel = strings.TrimPrefix(spec, "@/")
	case strings.HasPrefix(spec, "./"), strings.HasPrefix(spec, "../"):
		abs := filepath.Join(filepath.Dir(importerPath), filepath.FromSlash(spec))

		rel, err := filepath.Rel(siteSrc, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return true // escapes the tree → not an internal dependency
		}

		modRel = filepath.ToSlash(rel)
	default:
		return true // external package
	}

	base := filepath.Join(siteSrc, filepath.FromSlash(modRel))

	if info, err := os.Stat(base); err == nil && info.IsDir() {
		return true // directory import (resolved via its own index/barrel)
	}

	candidates := []string{}
	if filepath.Ext(modRel) != "" {
		candidates = append(candidates, base)
	}

	for _, ext := range acceptanceExts {
		candidates = append(candidates, base+ext)
	}

	for _, idx := range []string{"index.ts", "index.tsx", "index.jsx", "index.js"} {
		candidates = append(candidates, filepath.Join(base, idx))
	}

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return true
		}
	}

	return false
}

func hasExt(path string, exts []string) bool {
	ext := filepath.Ext(path)
	for _, e := range exts {
		if ext == e {
			return true
		}
	}

	return false
}

func lightwaveUICheckout(t *testing.T) string {
	t.Helper()

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	uiRepo := filepath.Join(home, "dev", "lightwave-ui")
	if _, err := os.Stat(filepath.Join(uiRepo, "src", "components", "base", "avatar")); err != nil {
		t.Skipf("lightwave-ui checkout not present at %s — skipping end-to-end acceptance", uiRepo)
	}

	return uiRepo
}

func realVersion(t *testing.T, uiRepo string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(uiRepo, "package.json"))
	require.NoError(t, err)

	var pkg struct {
		Version string `json:"version"`
	}

	require.NoError(t, json.Unmarshal(raw, &pkg))
	require.NotEmpty(t, pkg.Version)

	return pkg.Version
}
