package githuborg_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/githuborg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// archivedNullStandalones are the six null* repos folded into
// lightwave-ai/src/<module>/ and archived read-only (lightwave-ai#44). Archived
// repos reject label and milestone writes, so bootstrapping them fails noisily.
var archivedNullStandalones = []string{
	"nullclaw", "nullhub", "nullbuilder", "nulltickets", "nullwatch", "nullboiler",
}

// swarmReposInScript extracts the SWARM_REPOS bash array from the bootstrap
// script so the test reads the same list the script actually rolls out over.
func swarmReposInScript(t *testing.T) []string {
	t.Helper()

	const scriptPath = "../../scripts/bootstrap-github-org.sh"
	raw, err := os.ReadFile(scriptPath)
	require.NoError(t, err, "read %s", scriptPath)

	block := regexp.MustCompile(`(?s)\nSWARM_REPOS=\((.*?)\n\)`).FindSubmatch(raw)
	require.Len(t, block, 2, "SWARM_REPOS=( ... ) array not found in %s", scriptPath)

	var repos []string
	for line := range strings.SplitSeq(string(block[1]), "\n") {
		if name := strings.TrimSpace(line); name != "" && !strings.HasPrefix(name, "#") {
			repos = append(repos, name)
		}
	}

	return repos
}

// TestBootstrapScriptExcludesArchivedNullRepos pins lightwave-cli#294: the
// script enumerated all six archived null* standalones long after they were
// folded into lightwave-ai, so every bootstrap run tried to write labels and
// milestones to read-only repos.
func TestBootstrapScriptExcludesArchivedNullRepos(t *testing.T) {
	t.Parallel()

	repos := swarmReposInScript(t)

	for _, archived := range archivedNullStandalones {
		assert.NotContains(t, repos, archived,
			"%s is archived read-only (folded into lightwave-ai/src/%s) — "+
				"bootstrapping it fails; its issues live on lightwave-ai under package:%s",
			archived, archived, archived)
	}
}

// TestBootstrapScriptMatchesSwarmRepos pins the script and the Go constant to
// the same estate. They drifted apart once already (#294) because nothing
// compared them; this fails on the next divergence in either direction.
func TestBootstrapScriptMatchesSwarmRepos(t *testing.T) {
	t.Parallel()

	assert.ElementsMatch(t, githuborg.SwarmRepos, swarmReposInScript(t),
		"scripts/bootstrap-github-org.sh SWARM_REPOS and githuborg.SwarmRepos must "+
			"describe the same estate — the script comment says it mirrors the constant")
}
