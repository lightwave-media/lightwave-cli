package githuborg_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/lightwave-media/lightwave-cli/internal/githuborg"
)

// branchProtection is the slice of the governance stamp this test reads.
type branchProtection struct {
	Example struct {
		Excluded []struct {
			Repo   string `yaml:"repo"`
			Reason string `yaml:"reason"`
		} `yaml:"excluded"`
	} `yaml:"example"`
}

// excludedRepos reads the stamped governance list of repos deliberately outside
// CI protection.
//
// Entries spell several repos on one line ("nullboiler, nullbuilder, …"), so
// each value is split rather than compared whole.
func excludedRepos(t *testing.T) map[string]string {
	t.Helper()

	path := filepath.Join(
		"..", "corestamp", "schemas", "policy", "governance", "branch_protection.yaml")

	data, err := os.ReadFile(path)
	require.NoError(t, err, "the embedded stamp snapshot is part of this module")

	var doc branchProtection
	require.NoError(t, yaml.Unmarshal(data, &doc))
	require.NotEmpty(t, doc.Example.Excluded,
		"parsed no excluded repos — the stamp shape moved and this test would "+
			"silently pass against nothing")

	out := map[string]string{}

	for _, entry := range doc.Example.Excluded {
		for _, name := range strings.Split(entry.Repo, ",") {
			if name = strings.TrimSpace(name); name != "" {
				out[name] = entry.Reason
			}
		}
	}

	return out
}

// TestSwarmReposAreNotExcludedFromGovernance is the contradiction that let a
// deleted repo sit in this list.
//
// `homebrew-tap` was a SwarmRepo AND carried a branch_protection exclusion
// reading "GoReleaser pushes the formula directly; a PR requirement would break
// the release train" — i.e. the stamp already said this was not a repo with the
// full issue workflow the board is for. It stayed until the repo was deleted
// and sync started asking the API for a 404.
//
// A repo cannot be both on the swarm board and outside CI governance. Checking
// that against the stamp is better than a second hand-maintained list, which is
// what the old "mirrors bootstrap-github-org.sh" comment amounted to.
func TestSwarmReposAreNotExcludedFromGovernance(t *testing.T) {
	t.Parallel()

	excluded := excludedRepos(t)

	for _, repo := range githuborg.SwarmRepos {
		reason, isExcluded := excluded[repo]
		assert.False(t, isExcluded,
			"%s is on the swarm board and excluded from branch protection (%q) — "+
				"pick one", repo, reason)
	}
}

// TestSwarmReposHasNoDuplicates — the board is iterated per repo, so a
// duplicate doubles the API calls and the reported counts.
func TestSwarmReposHasNoDuplicates(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, repo := range githuborg.SwarmRepos {
		assert.False(t, seen[repo], "%s appears twice", repo)
		seen[repo] = true
	}

	assert.NotEmpty(t, githuborg.SwarmRepos, "an empty rollout set syncs nothing, quietly")
}

// TestGovernanceExclusionsAreReadable guards the reader itself.
//
// The check above is only as good as the parse: if the stamp's shape changed
// and `excluded` came back empty, every SwarmRepo would pass for the wrong
// reason. The require.NotEmpty in excludedRepos covers that, and this pins the
// one entry the parse must be able to see through — a multi-repo line.
func TestGovernanceExclusionsAreReadable(t *testing.T) {
	t.Parallel()

	excluded := excludedRepos(t)

	assert.Contains(t, excluded, "nullclaw",
		"a comma-separated repo line must split into its individual names")
}
