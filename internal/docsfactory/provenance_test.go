package docsfactory_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/docsfactory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #313. spec-lint reported `unknown kind "technical_study"` against a stamp
// checkout six minutes behind origin. That message is indistinguishable from
// "you used a kind that does not exist"; the kind existed upstream and the
// whole cure was `git pull`. These pin that a verdict now says what contract it
// was reached against, and warns when that contract may be stale.

func TestProvenanceString(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		prov     docsfactory.Provenance
		contains []string
	}{
		"full": {
			docsfactory.Provenance{
				Root: "/x/lightwave-core", Commit: "d442ea5",
				SpecKindsVersion: "0.5.0", Behind: 0,
			},
			[]string{"lightwave-core@d442ea5", "spec_artifact_kinds v0.5.0"},
		},
		"behind is qualified by last fetch": {
			docsfactory.Provenance{Root: "/x/lightwave-core", Commit: "abc1234", Behind: 3},
			[]string{"3 behind upstream as of last fetch"},
		},
		"dirty is marked": {
			docsfactory.Provenance{Root: "/x/lightwave-core", Commit: "abc1234", Dirty: true, Behind: 0},
			[]string{"abc1234-dirty"},
		},
		"no upstream says so rather than claiming current": {
			docsfactory.Provenance{Root: "/x/lightwave-core", Commit: "abc1234", Behind: -1},
			[]string{"upstream unknown"},
		},
		"not a git checkout still names the root": {
			docsfactory.Provenance{Root: "/x/lightwave-core", Behind: -1},
			[]string{"lightwave-core"},
		},
	}

	for name, testCase := range cases {
		tt := testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tt.prov.String()
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
		})
	}
}

// TestProvenanceStale is the both-directions control on the warning.
//
// "Unknown upstream" is deliberately NOT stale. A vendored copy or a detached
// HEAD is an ordinary way to run this, and warning every time is how a warning
// stops being read — which would cost more than the case it catches.
func TestProvenanceStale(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		prov      docsfactory.Provenance
		wantStale bool
	}{
		"behind":             {docsfactory.Provenance{Behind: 2}, true},
		"dirty":              {docsfactory.Provenance{Dirty: true}, true},
		"behind and dirty":   {docsfactory.Provenance{Behind: 2, Dirty: true}, true},
		"current":            {docsfactory.Provenance{Behind: 0}, false},
		"upstream unknown":   {docsfactory.Provenance{Behind: -1}, false},
		"not a git checkout": {docsfactory.Provenance{Behind: -1, Commit: ""}, false},
	}

	for name, testCase := range cases {
		tt := testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantStale, tt.prov.Stale())

			if tt.wantStale {
				assert.NotEmpty(t, tt.prov.StaleWarning(),
					"a stale stamp must say so, and say what to do about it")

				return
			}

			assert.Empty(t, tt.prov.StaleWarning())
		})
	}
}

// TestStaleWarningNamesTheCure — "may be stale" without a cure sends the reader
// hunting a content bug, which is the original failure.
func TestStaleWarningNamesTheCure(t *testing.T) {
	t.Parallel()

	warn := docsfactory.Provenance{Root: "/x/lightwave-core", Behind: 3}.StaleWarning()

	assert.Contains(t, warn, "pull", "the warning must name the fix")
	assert.Contains(t, warn, "/x/lightwave-core", "and which checkout to run it in")
}

func gitAt(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// TestReadProvenance_DetectsBehindUpstream drives the detector against a real
// checkout that is genuinely behind, rather than only against a struct literal.
func TestReadProvenance_DetectsBehindUpstream(t *testing.T) {
	t.Parallel()

	upstream := t.TempDir()
	gitAt(t, upstream, "init", "-q", "-b", "main")
	gitAt(t, upstream, "config", "user.email", "t@t.com")
	gitAt(t, upstream, "config", "user.name", "T")
	require.NoError(t, os.WriteFile(filepath.Join(upstream, "a"), []byte("1\n"), 0o600))
	gitAt(t, upstream, "add", ".")
	gitAt(t, upstream, "commit", "-qm", "one")

	clone := filepath.Join(t.TempDir(), "lightwave-core")
	gitAt(t, ".", "clone", "-q", upstream, clone)

	// Upstream moves; the clone does not.
	require.NoError(t, os.WriteFile(filepath.Join(upstream, "b"), []byte("2\n"), 0o600))
	gitAt(t, upstream, "add", ".")
	gitAt(t, upstream, "commit", "-qm", "two")
	gitAt(t, clone, "fetch", "-q")

	prov := docsfactory.ReadProvenance(t.Context(), clone)

	assert.Equal(t, 1, prov.Behind, "one commit landed upstream since this checkout")
	assert.True(t, prov.Stale())
	assert.NotEmpty(t, prov.Commit)
}

// TestReadProvenance_NonRepoDegrades — a vendored copy is legitimate. Refusing
// to lint because git is unavailable would trade a little context for a total
// outage, so every field degrades instead.
func TestReadProvenance_NonRepoDegrades(t *testing.T) {
	t.Parallel()

	prov := docsfactory.ReadProvenance(t.Context(), t.TempDir())

	assert.Empty(t, prov.Commit)
	assert.Equal(t, -1, prov.Behind)
	assert.False(t, prov.Stale(), "absence of git is not evidence of staleness")
}

// TestLoadSchemas_RejectsAnUnreadableStamp is the rejection path, and the
// boundary that makes provenance meaningful.
//
// Provenance is attached only after the contracts parse. If LoadSchemas
// returned empty contracts instead of an error, every file would lint as
// "unknown kind" and the run would name a checkout as the authority for a
// verdict that checkout never produced — a more confident version of exactly
// the #313 failure.
//
//nolint:paralleltest // t.Setenv is incompatible with t.Parallel
func TestLoadSchemas_RejectsAnUnreadableStamp(t *testing.T) {
	// A directory with no src/schemas: resolution can name it, but nothing
	// there parses.
	t.Setenv("LW_LIGHTWAVE_CORE", t.TempDir())

	_, err := docsfactory.LoadSchemas(t.TempDir())
	require.Error(t, err, "a stamp that does not parse must not read as zero contracts")
}
