package gogen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/codegen/gogen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const entityYAML = `_meta:
  version: 1.0.0
  schema_id: lightwave://schemas/data/test/widget
  title: Widget
  table_kind: entity
  scope: local
  table_name: widgets
required_fields:
- name: label
  type: str
`

// newRepo builds a git repo whose committed content differs from its working
// tree, which is the exact condition --ref exists to handle.
func newRepo(t *testing.T) (root, dir string) {
	t.Helper()

	root = t.TempDir()
	dir = "src/schemas/data/test"
	require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))

	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	git("init", "-q")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")

	require.NoError(t, os.WriteFile(filepath.Join(root, dir, "widget.yaml"), []byte(entityYAML), 0o600))
	git("add", "-A")
	git("commit", "-qm", "committed")

	return root, dir
}

func TestGitSourceReadsTheCommittedRefNotTheWorkingTree(t *testing.T) {
	t.Parallel()

	root, dir := newRepo(t)

	// Dirty the working tree the way a sibling session would.
	require.NoError(t, os.WriteFile(filepath.Join(root, dir, "widget.yaml"),
		[]byte(entityYAML+"- name: uncommitted_field\n  type: str\n"), 0o600))
	// And add a file that exists only in the working tree.
	require.NoError(t, os.WriteFile(filepath.Join(root, dir, "ghost.yaml"), []byte(entityYAML), 0o600))

	gitSrc, err := gogen.NewSource(t.Context(), root, "HEAD")
	require.NoError(t, err)

	paths, err := gitSrc.List(t.Context(), dir)
	require.NoError(t, err)
	assert.Len(t, paths, 1, "the uncommitted ghost.yaml must not appear at a ref")

	data, err := gitSrc.Read(t.Context(), paths[0])
	require.NoError(t, err)
	assert.NotContains(t, string(data), "uncommitted_field",
		"a ref read must not see the working tree's edit")

	// The escape hatch must still see both.
	fsSrc, err := gogen.NewSource(t.Context(), root, gogen.WorktreeRef)
	require.NoError(t, err)

	fsPaths, err := fsSrc.List(t.Context(), dir)
	require.NoError(t, err)
	assert.Len(t, fsPaths, 2, "--ref worktree reads the working tree, ghost included")
}

func TestGitSourceDescribesTheResolvedSHA(t *testing.T) {
	t.Parallel()

	root, _ := newRepo(t)

	src, err := gogen.NewSource(t.Context(), root, "HEAD")
	require.NoError(t, err)
	assert.Contains(t, src.Describe(), "HEAD@", "the summary must name the stamp it read")

	fsSrc, err := gogen.NewSource(t.Context(), root, gogen.WorktreeRef)
	require.NoError(t, err)
	assert.Contains(t, fsSrc.Describe(), "UNPINNED",
		"reading a mutable tree must say so out loud")
}

func TestBadRefFailsBeforeGenerating(t *testing.T) {
	t.Parallel()

	root, _ := newRepo(t)

	_, err := gogen.NewSource(t.Context(), root, "no-such-ref")
	require.Error(t, err, "an unresolvable ref must fail up front, not generate nothing quietly")
	assert.Contains(t, err.Error(), gogen.WorktreeRef, "the error should name the escape hatch")
}

func TestLoadFromFiltersByScopeAndReportsWhy(t *testing.T) {
	t.Parallel()

	root, dir := newRepo(t)

	platform := `_meta:
  schema_id: lightwave://schemas/data/test/invoice
  table_kind: entity
  scope: platform
  table_name: invoices
required_fields:
- name: total
  type: int
`
	notATable := `_meta:
  schema_id: lightwave://schemas/data/test/contract_block
  table_kind: value_object
  scope: local
required_fields:
- name: body
  type: str
`
	for name, body := range map[string]string{"invoice.yaml": platform, "contract_block.yaml": notATable} {
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, name), []byte(body), 0o600))
	}

	src, err := gogen.NewSource(t.Context(), root, gogen.WorktreeRef)
	require.NoError(t, err)

	entities, skipped, err := gogen.LoadFrom(t.Context(), src, dir, gogen.ScopeLocal)
	require.NoError(t, err)

	require.Len(t, entities, 1, "only the local entity is in scope")
	assert.Equal(t, "widgets", entities[0].Meta.TableName)

	// The point of skipped: an undeclared schema, a deliberate value_object and
	// a parse error used to be one indistinguishable outcome — nothing.
	require.Len(t, skipped, 2)
	joined := skipped[0] + "|" + skipped[1]
	assert.Contains(t, joined, "value_object", "a non-table must say it is a non-table")
	assert.Contains(t, joined, "scope=platform", "an out-of-scope schema must say which scope it has")
}

func TestUndeclaredScopeIsTreatedAsPlatform(t *testing.T) {
	t.Parallel()

	root, dir := newRepo(t)

	// No `scope:` at all — the state 221 of 242 data schemas were in.
	require.NoError(t, os.WriteFile(filepath.Join(root, dir, "legacy.yaml"), []byte(`_meta:
  schema_id: lightwave://schemas/data/test/legacy
  table_kind: entity
  table_name: legacies
required_fields:
- name: name
  type: str
`), 0o600))

	src, err := gogen.NewSource(t.Context(), root, gogen.WorktreeRef)
	require.NoError(t, err)

	local, _, err := gogen.LoadFrom(t.Context(), src, dir, gogen.ScopeLocal)
	require.NoError(t, err)

	for _, e := range local {
		assert.NotEqual(t, "legacies", e.Meta.TableName,
			"an undeclared scope must not be pulled into a local print by default")
	}

	platform, _, err := gogen.LoadFrom(t.Context(), src, dir, gogen.ScopePlatform)
	require.NoError(t, err)

	var found bool

	for _, e := range platform {
		if e.Meta.TableName == "legacies" {
			found = true
		}
	}

	assert.True(t, found, "an undeclared scope defaults to platform, per the enum's own default")
}

func TestDocumentTablesIndexFilesRatherThanReplacingThem(t *testing.T) {
	t.Parallel()

	root, dir := newRepo(t)

	require.NoError(t, os.WriteFile(filepath.Join(root, dir, "adr.yaml"), []byte(`_meta:
  schema_id: lightwave://schemas/data/test/adr
  table_kind: document
  scope: local
  table_name: adrs
required_fields:
- name: title
  type: str
`), 0o600))

	src, err := gogen.NewSource(t.Context(), root, gogen.WorktreeRef)
	require.NoError(t, err)

	entities, _, err := gogen.LoadFrom(t.Context(), src, dir, gogen.ScopeLocal)
	require.NoError(t, err)
	require.Len(t, entities, 2, "document is tabled alongside entity")

	sql, err := gogen.EmitMigration(entities)
	require.NoError(t, err)

	assert.Contains(t, sql, "CREATE TABLE IF NOT EXISTS adrs")
	assert.Contains(t, sql, "source_path TEXT NOT NULL",
		"a document table must record which file it indexes")
	assert.Contains(t, sql, "content_sha256",
		"staleness must be detectable without reparsing every file")

	// The entity table must NOT get the indexing columns — it owns its rows.
	_, widgetsDDL, found := strings.Cut(sql, "CREATE TABLE IF NOT EXISTS widgets")
	require.True(t, found, "the widgets table must be in the migration")
	widgetsDDL, _, _ = strings.Cut(widgetsDDL, ");")
	assert.NotContains(t, widgetsDDL, "source_path",
		"an entity owns its rows and must not carry a file pointer")
}
