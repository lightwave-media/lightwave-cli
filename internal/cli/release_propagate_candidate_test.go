//nolint:testpackage // exercises the propagation producer with real Git history
package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/lightwave-media/lightwave-cli/internal/testutil/gitfixture"
)

func candidateGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...)
	c.Dir = dir
	c.Env = append(gitfixture.Env(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := c.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func propagationCandidate(t *testing.T) (string, string, string) {
	t.Helper()
	origin := newPropagateRepo(t, t.Context())
	require.NoError(t, os.Mkdir(filepath.Join(origin, "dev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(origin, "dev", "ci.sh"), []byte("#!/bin/sh\nset -eu\ngit merge-base HEAD origin/main > .ci-base\ngit rev-parse HEAD > .ci-head\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(origin, ".gitignore"), []byte(".ci-*\n"), 0o644))
	candidateGit(t, origin, "add", "-A")
	candidateGit(t, origin, "commit", "-qm", "add isolated CI")
	worktree := filepath.Join(t.TempDir(), "consumer")
	candidateGit(t, origin, "clone", "-q", origin, worktree)
	candidateGit(t, worktree, "checkout", "-qb", "feature/consumer")
	require.NoError(t, os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("consumer change"), 0o644))
	candidateGit(t, worktree, "add", "feature.txt")
	candidateGit(t, worktree, "commit", "-qm", "consumer change")
	require.NoError(t, os.WriteFile(filepath.Join(origin, "candidate.txt"), []byte("requested release"), 0o644))
	candidateGit(t, origin, "add", "candidate.txt")
	candidateGit(t, origin, "commit", "-qm", "requested release")
	requested := candidateGit(t, origin, "rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(origin, "future.txt"), []byte("later main"), 0o644))
	candidateGit(t, origin, "add", "future.txt")
	candidateGit(t, origin, "commit", "-qm", "later main")
	return worktree, requested, candidateGit(t, origin, "rev-parse", "HEAD")
}

func TestPropagateUsesRequestedCommitWhenMainAdvances(t *testing.T) {
	t.Parallel()
	dir, requested, latest := propagationCandidate(t)
	result := propagateWorktree(t.Context(), dir, "feature/consumer", requested, true)
	require.False(t, result.Blocked, result.Detail)
	require.True(t, result.CIOK)
	actualBase, err := os.ReadFile(filepath.Join(dir, ".ci-base"))
	require.NoError(t, err)
	require.Equal(t, requested, strings.TrimSpace(string(actualBase)), "CI must execute on the requested base, even after origin/main advances")
	require.NotEqual(t, latest, strings.TrimSpace(string(actualBase)))
	_, err = os.Stat(filepath.Join(dir, "future.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)

	// Feed the producer's actual Git/CI result into its normal report writer.
	reports := t.TempDir()
	requestedPath := filepath.Join(reports, "invocation.json")
	require.NoError(t, os.WriteFile(requestedPath, nil, 0o600))
	report := propagateReport{Status: "completed", Repo: "lightwave-cli", MainSHA: requested, ScanTimeISO: time.Now().UTC().Format(time.RFC3339), Worktrees: []propagateWorktreeResult{result}}
	require.NoError(t, writePropagateReportInDir(&report, requestedPath, reports))
	data, err := os.ReadFile(requestedPath)
	require.NoError(t, err)
	var decoded propagateReport
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, requested, decoded.MainSHA)
	require.True(t, decoded.Worktrees[0].CIOK)
	latestData, err := os.ReadFile(filepath.Join(reports, "release-propagate.latest.json"))
	require.NoError(t, err)
	require.JSONEq(t, string(data), string(latestData))
	compact, err := json.Marshal(decoded)
	require.NoError(t, err)
	t.Logf("PROPAGATE_REPORT %s", compact)
	if coreRoot := os.Getenv("LW_PROPAGATION_CORE_ROOT"); coreRoot != "" {
		// Optional paired-repository contract check. Run while the real fixture
		// and the producer-written report still exist; no live checkout moves.
		selected := filepath.Join(t.TempDir(), "selected-candidate")
		candidateGit(t, dir, "clone", "-q", dir, selected)
		candidateGit(t, selected, "checkout", "--detach", requested)
		validator := filepath.Join(coreRoot, "dev", "engineering", "release_evidence.py")
		command := exec.CommandContext(t.Context(), "uv", "run", "--no-sync", "--project", coreRoot, "python", validator,
			"propagation", "--repo-path", selected, "--candidate-sha", requested,
			"--repo-slug", "lightwave-media/lightwave-cli", "--evidence", requestedPath)
		command.Dir = coreRoot
		command.Env = gitfixture.Env()
		output, err := command.CombinedOutput()
		require.NoError(t, err, "Core consumer rejected actual producer receipt: %s", output)
		t.Logf("Core consumer accepted actual isolated producer receipt: %s", output)
	}
}

func TestPropagateRejectsConsumerAlreadyBeyondCandidate(t *testing.T) {
	t.Parallel()
	dir, requested, latest := propagationCandidate(t)
	candidateGit(t, dir, "fetch", "origin")
	candidateGit(t, dir, "rebase", "origin/main")
	before := candidateGit(t, dir, "rev-parse", "HEAD")
	result := propagateWorktree(t.Context(), dir, "feature/consumer", requested, true)
	require.True(t, result.Blocked)
	require.False(t, result.CIOK)
	require.Equal(t, before, candidateGit(t, dir, "rev-parse", "HEAD"))
	require.Equal(t, latest, candidateGit(t, dir, "merge-base", "HEAD", "origin/main"))
}

func TestPropagateInvalidatesProofWhenCIChangesSource(t *testing.T) {
	t.Parallel()
	dir, requested, _ := propagationCandidate(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dev", "ci.sh"), []byte("#!/bin/sh\nset -eu\nprintf changed >> seed.txt\n"), 0o755))
	candidateGit(t, dir, "add", "dev/ci.sh")
	candidateGit(t, dir, "commit", "-qm", "CI modifies tracked source")
	result := propagateWorktree(t.Context(), dir, "feature/consumer", requested, true)
	require.True(t, result.Blocked)
	require.False(t, result.CIOK)
	require.Contains(t, result.Detail, "source changed during CI")
}

func TestPropagateReportRequiresFullCandidateBeforeAnyOperation(t *testing.T) {
	t.Parallel()
	for _, candidate := range []string{"", "abc1234", "origin/main"} {
		err := releasePropagateHandler(t.Context(), nil, map[string]any{"repo": "lightwave-cli", "main-sha": candidate, "report-path": filepath.Join(t.TempDir(), "proof.json")})
		require.ErrorContains(t, err, "full commit SHA")
	}
}

func TestRequestedPropagationReportRejectsStaleMissingParentAndSymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stale := filepath.Join(root, "stale.json")
	require.NoError(t, os.WriteFile(stale, []byte("old receipt"), 0o600))
	require.Error(t, writeRequestedPropagateReport(stale, []byte("new receipt")))
	got, err := os.ReadFile(stale)
	require.NoError(t, err)
	require.Equal(t, "old receipt", string(got))
	require.Error(t, writeRequestedPropagateReport(filepath.Join(root, "missing", "report.json"), []byte("receipt")))
	link := filepath.Join(root, "linked.json")
	require.NoError(t, os.Symlink(stale, link))
	require.Error(t, writeRequestedPropagateReport(link, []byte("new receipt")))
	report := propagateReport{Status: "completed"}
	require.Error(t, writePropagateReportInDir(&report, stale, filepath.Join(root, "legacy")))
}

func TestPropagationMatchesFullRepositoryIdentity(t *testing.T) {
	t.Parallel()
	for _, remote := range []string{"git@github.com:lightwave-media/lightwave-cli.git", "https://github.com/lightwave-media/lightwave-cli.git", "ssh://git@github.com/lightwave-media/lightwave-cli"} {
		require.True(t, propagationRepoMatches(remote, "lightwave-media/lightwave-cli"))
	}
	for _, remote := range []string{"https://github.com/other/lightwave-cli.git", "https://github.com/lightwave-media/lightwave-cli-old.git", "https://elsewhere.test/lightwave-media/lightwave-cli.git"} {
		require.False(t, propagationRepoMatches(remote, "lightwave-media/lightwave-cli"))
	}
}
