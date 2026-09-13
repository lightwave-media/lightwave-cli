package runbook_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/runbook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The discovery surface behind `lw runbook list` / `search` (#377, #417).
//
// The property worth pinning is not "it lists things" but that it distinguishes
// REGISTERED from RUNNABLE. lightwave-core's registry and its filesystem
// disagree — 56 indexed, 51 with a runbook.mdx — and a catalog that reported
// membership would advertise five runbooks whose `start` dies on a missing
// edition, sending the caller to debug a checkout for something never written.

// writeCategorised writes an index with several categories. engine_test.go's
// writeCatalog covers the single-category case and is reused below; grouping
// and sort order need more than one category to mean anything.
func writeCategorised(t *testing.T, core string, cats map[string]map[string]string, mdx map[string]string) {
	t.Helper()

	var idx strings.Builder

	idx.WriteString("_meta:\n  version: \"0.1.0\"\ncategories:\n")

	for cat, slugs := range cats {
		idx.WriteString("  " + cat + ":\n")

		for slug, dir := range slugs {
			idx.WriteString("    " + slug + ": " + dir + "\n")

			if body, ok := mdx[slug]; ok {
				writeFile(t, core, filepath.Join("src/runbooks", dir, "runbook.mdx"), body)
			}
		}
	}

	writeFile(t, core, "src/runbooks/__index.yaml", idx.String())
}

func TestLoadCatalog_SeparatesRegisteredFromRunnable(t *testing.T) {
	t.Parallel()

	core := t.TempDir()
	writeCatalog(t, core,
		map[string]string{
			"has-file": "test/has-file",
			"no-file":  "test/no-file", // indexed, never written
		},
		map[string]string{
			"has-file": "---\ndescription: Rolls a service\nstatus: active\n---\n# x\n",
		})

	records, err := runbook.LoadCatalog(core)
	require.NoError(t, err)
	require.Len(t, records, 2, "both registry entries must be REPORTED")

	byslug := map[string]runbook.Record{}
	for _, r := range records {
		byslug[r.Slug] = r
	}

	assert.True(t, byslug["has-file"].Reachable)
	assert.Equal(t, "Rolls a service", byslug["has-file"].Description)
	assert.Equal(t, "active", byslug["has-file"].Status)

	// The whole point: present in the listing, marked unrunnable. Dropping it
	// silently would hide a registry fault; marking it runnable would send the
	// caller to a `start` that fails on a missing edition.
	assert.False(t, byslug["no-file"].Reachable)
	assert.Empty(t, byslug["no-file"].Description)
}

func TestLoadCatalog_SortsByCategoryThenSlug(t *testing.T) {
	t.Parallel()

	core := t.TempDir()
	body := "---\ndescription: d\n---\n"
	writeCategorised(t, core, map[string]map[string]string{
		"zeta":  {"b-two": "zeta/b-two", "a-one": "zeta/a-one"},
		"alpha": {"only": "alpha/only"},
	}, map[string]string{"b-two": body, "a-one": body, "only": body})

	records, err := runbook.LoadCatalog(core)
	require.NoError(t, err)

	got := make([]string, 0, len(records))
	for _, r := range records {
		got = append(got, r.Category+"/"+r.Slug)
	}

	// Map iteration is random; a listing that reorders between runs is unusable
	// for diffing and for agents.
	assert.Equal(t, []string{"alpha/only", "zeta/a-one", "zeta/b-two"}, got)
}

// TestLoadCatalog_MissingIndexIsNamed is the rejection path. An unreachable
// catalog must surface ErrCatalogUnreachable rather than an empty list — an
// empty list reads as "there are no runbooks", which is the plausible-wrong
// answer this surface exists to avoid.
func TestLoadCatalog_MissingIndexIsNamed(t *testing.T) {
	t.Parallel()

	_, err := runbook.LoadCatalog(t.TempDir())
	require.Error(t, err)
	require.ErrorIs(t, err, runbook.ErrCatalogUnreachable)
}

func TestLoadCatalog_MalformedFrontMatterStillLists(t *testing.T) {
	t.Parallel()

	core := t.TempDir()
	writeCatalog(t, core,
		map[string]string{"broken": "test/broken"},
		map[string]string{"broken": "---\ndescription: [unclosed\nstatus:\n---\n# x\n"})

	records, err := runbook.LoadCatalog(core)
	require.NoError(t, err, "one unparsable description must not take down the catalog")
	require.Len(t, records, 1)

	// Reachability comes from the file existing, not from parsing it: the
	// runbook runs whether or not its description is well-formed.
	assert.True(t, records[0].Reachable)
	assert.Empty(t, records[0].Description)
}

func TestSearch(t *testing.T) {
	t.Parallel()

	records := []runbook.Record{
		{Slug: "deploy-fargate-service", Category: "deploy", Description: "Rolls an ECS service", Reachable: true},
		{Slug: "bootstrap-aws-rds-postgres", Category: "infra", Description: "Stands up a database", Reachable: true},
		{Slug: "ci-gate", Category: "quality", Status: "draft", Reachable: true},
	}

	cases := map[string]struct {
		query string
		want  []string
	}{
		"by slug":              {"fargate", []string{"deploy-fargate-service"}},
		"by category":          {"infra", []string{"bootstrap-aws-rds-postgres"}},
		"by description":       {"database", []string{"bootstrap-aws-rds-postgres"}},
		"by status":            {"draft", []string{"ci-gate"}},
		"case insensitive":     {"FARGATE", []string{"deploy-fargate-service"}},
		"empty matches all":    {"", []string{"deploy-fargate-service", "bootstrap-aws-rds-postgres", "ci-gate"}},
		"no match is no match": {"zzzznope", nil},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := make([]string, 0)
			for _, r := range runbook.Search(records, tc.query) {
				got = append(got, r.Slug)
			}

			if tc.want == nil {
				assert.Empty(t, got)

				return
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

// TestSearch_DoesNotMatchAcrossFieldBoundaries guards the concatenation. The
// haystack joins slug, category, description and status; joining them with an
// empty string would let a query straddle two fields and match a runbook that
// contains the text in neither.
func TestSearch_DoesNotMatchAcrossFieldBoundaries(t *testing.T) {
	t.Parallel()

	records := []runbook.Record{{Slug: "abc", Category: "def"}}

	assert.Empty(t, runbook.Search(records, "abcdef"),
		"a query spanning two fields must not match")
	assert.Len(t, runbook.Search(records, "abc"), 1)
}
