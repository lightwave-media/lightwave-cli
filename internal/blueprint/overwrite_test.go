//nolint:testpackage // exercises unexported overwrite-guard helpers (collisions, copyTree)
package blueprint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Internal test (package blueprint): collisions and copyTree are the
// overwrite-guard primitives — unexported, so tested from inside the package.

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
}

func TestCollisions_ReportsOnlyExistingTargets(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dst := t.TempDir()

	// staged tree the blueprint produced
	writeFile(t, src, "README.md", "blueprint readme")
	writeFile(t, src, "spec/prd/0001.md", "prd")
	writeFile(t, src, "spec/adr/0001.md", "adr")

	// destination already has README.md (would be clobbered) but not the spec files
	writeFile(t, dst, "README.md", "the real repo readme")

	clashes, err := collisions(src, dst)
	require.NoError(t, err)
	assert.Equal(t, []string{"README.md"}, clashes, "only the pre-existing target collides")
}

func TestCollisions_NoneWhenDestEmpty(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeFile(t, src, "spec/prd/0001.md", "prd")

	clashes, err := collisions(src, filepath.Join(t.TempDir(), "does-not-exist"))
	require.NoError(t, err)
	assert.Empty(t, clashes)
}

func TestCopyTree_CopiesAllFilesPreservingLayout(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, src, "README.md", "readme body")
	writeFile(t, src, "spec/prd/0001.md", "prd body")

	require.NoError(t, copyTree(src, dst))

	got, err := os.ReadFile(filepath.Join(dst, "spec/prd/0001.md"))
	require.NoError(t, err)
	assert.Equal(t, "prd body", string(got))

	got, err = os.ReadFile(filepath.Join(dst, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "readme body", string(got))
}

func TestCopyTree_OverwritesExisting(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, src, "README.md", "new content")
	writeFile(t, dst, "README.md", "old content")

	require.NoError(t, copyTree(src, dst))

	got, err := os.ReadFile(filepath.Join(dst, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "new content", string(got), "copyTree overwrites once a collision is accepted (--force)")
}
