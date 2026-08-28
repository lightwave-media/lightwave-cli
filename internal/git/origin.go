package git

import (
	"path/filepath"
	"strings"
)

// OriginSlug returns the "owner/repo" slug for the repository's origin remote,
// or "" when there is no origin (or no repository at all).
//
// Both remote spellings resolve to the same slug:
//
//	git@github.com:lightwave-media/lightwave-cli.git  -> lightwave-media/lightwave-cli
//	https://github.com/lightwave-media/lightwave-cli  -> lightwave-media/lightwave-cli
//
// This is the Go counterpart of the shell derivation in dev/hooks/pre-push,
// added in #330 for the same reason: a checkout's directory name is arbitrary
// (worktrees are named after tickets, clones get renamed), so the remote is the
// only trustworthy statement of which repository you are standing in.
func (g *Git) OriginSlug() string {
	url, err := g.run("remote", "get-url", "origin")
	if err != nil {
		return ""
	}

	return OriginSlugFromURL(url)
}

// RepoRoot returns the absolute path of the current working tree's top level,
// or "" when dir is not inside a repository. Inside a linked worktree this is
// the worktree's own root, not the primary checkout's.
func (g *Git) RepoRoot() string {
	out, err := g.run("rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}

	return strings.TrimSpace(out)
}

// MainRepoRoot returns the primary checkout's top level, the same value from
// anywhere in the repository — including from inside a linked worktree, where
// RepoRoot would answer with the worktree instead.
//
// This is the one that resolves `<repo>/.worktrees` (ADR-0047 / CORE-0047):
// worktrees hang off the primary checkout, so asking a worktree for its own root
// yields `<worktree>/.worktrees`, which never exists. That is why
// `lw worktree status --current` reported "not inside a worktree" from inside
// every real worktree.
//
// --git-common-dir is shared by every worktree and points at the primary .git,
// whose parent is the primary checkout.
func (g *Git) MainRepoRoot() string {
	out, err := g.run("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}

	commonDir := strings.TrimSpace(out)
	if commonDir == "" {
		return ""
	}

	return filepath.Dir(commonDir)
}

// OriginSlugFromURL extracts "owner/repo" from a git remote URL. It is split
// out from OriginSlug so the parsing can be tested without a repository.
func OriginSlugFromURL(url string) string {
	url = strings.TrimSpace(url)
	url = strings.TrimSuffix(url, "/")
	url = strings.TrimSuffix(url, ".git")

	// scp-style remotes (git@host:owner/repo) put the path after a colon;
	// everything before it is user@host and carries no owner information.
	if _, path, found := strings.Cut(url, ":"); found && !strings.HasPrefix(url, "http") {
		url = path
	}

	// A usable slug needs both halves: the owner and the repo.
	const ownerRepoSegments = 2

	// Take the last two segments so https://host/owner/repo and owner/repo
	// both land on owner/repo. Anything shallower is not a usable slug.
	segments := strings.Split(strings.Trim(url, "/"), "/")
	if len(segments) < ownerRepoSegments {
		return ""
	}

	owner, repo := segments[len(segments)-2], segments[len(segments)-1]
	if owner == "" || repo == "" {
		return ""
	}

	return owner + "/" + repo
}
