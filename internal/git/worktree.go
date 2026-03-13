package git

import (
	"fmt"
	"strings"
)

// Worktree represents a git worktree.
type Worktree struct {
	Path   string
	Branch string
	Commit string
}

// WorktreeAdd creates a new worktree at the given path with a new branch.
// The new branch is created from the current HEAD.
// Skips LFS smudge filter during checkout.
func (g *Git) WorktreeAdd(path, branch string) error {
	_, err := g.runWithEnv(
		[]string{"worktree", "add", "-b", branch, path},
		[]string{"GIT_LFS_SKIP_SMUDGE=1"},
	)
	return err
}

// WorktreeAddFromRef creates a new worktree at the given path with a new branch
// starting from the specified ref (e.g., "origin/main").
// Skips LFS smudge filter during checkout to avoid downloading large LFS objects.
func (g *Git) WorktreeAddFromRef(path, branch, startPoint string) error {
	_, err := g.runWithEnv(
		[]string{"worktree", "add", "-b", branch, path, startPoint},
		[]string{"GIT_LFS_SKIP_SMUDGE=1"},
	)
	return err
}

// WorktreeAddExisting creates a new worktree at the given path for an existing branch.
// Skips LFS smudge filter during checkout.
func (g *Git) WorktreeAddExisting(path, branch string) error {
	_, err := g.runWithEnv(
		[]string{"worktree", "add", path, branch},
		[]string{"GIT_LFS_SKIP_SMUDGE=1"},
	)
	return err
}

// WorktreeRemove removes a worktree.
func (g *Git) WorktreeRemove(path string, force bool) error {
	args := []string{"worktree", "remove", path}
	if force {
		args = append(args, "--force")
	}
	_, err := g.run(args...)
	return err
}

// WorktreePrune removes worktree entries for deleted paths.
func (g *Git) WorktreePrune() error {
	_, err := g.run("worktree", "prune")
	return err
}

// WorktreeList returns all worktrees for this repository.
func (g *Git) WorktreeList() ([]Worktree, error) {
	out, err := g.run("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var worktrees []Worktree
	var current Worktree

	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			if current.Path != "" {
				worktrees = append(worktrees, current)
				current = Worktree{}
			}
			continue
		}

		switch {
		case strings.HasPrefix(line, "worktree "):
			current.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			current.Commit = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}

	// Don't forget the last one
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

// BranchCreatedDate returns the date when a branch was created.
// Uses the committer date of the first commit on the branch.
// Returns date in YYYY-MM-DD format.
func (g *Git) BranchCreatedDate(branch string) (string, error) {
	defaultBranch := g.RemoteDefaultBranch()
	mergeBase, err := g.run("merge-base", defaultBranch, branch)
	if err != nil {
		out, err := g.run("log", "-1", "--format=%cs", branch)
		if err != nil {
			return "", err
		}
		return out, nil
	}

	out, err := g.run("log", "--format=%cs", "--reverse", mergeBase+".."+branch)
	if err != nil {
		return "", err
	}

	lines := strings.Split(out, "\n")
	if len(lines) > 0 && lines[0] != "" {
		return lines[0], nil
	}

	out, err = g.run("log", "-1", "--format=%cs", mergeBase)
	if err != nil {
		return "", err
	}
	return out, nil
}

// CommitsAhead returns the number of commits that branch has ahead of base.
func (g *Git) CommitsAhead(base, branch string) (int, error) {
	out, err := g.run("rev-list", "--count", base+".."+branch)
	if err != nil {
		return 0, err
	}

	var count int
	_, err = fmt.Sscanf(out, "%d", &count)
	if err != nil {
		return 0, fmt.Errorf("parsing commit count: %w", err)
	}

	return count, nil
}

// UnpushedCommits returns the number of commits that are not pushed to the remote.
// Returns 0 if there is no upstream configured.
func (g *Git) UnpushedCommits() (int, error) {
	upstream, err := g.run("rev-parse", "--abbrev-ref", "@{u}")
	if err != nil {
		return 0, nil
	}

	out, err := g.run("rev-list", "--count", upstream+"..HEAD")
	if err != nil {
		return 0, err
	}

	var count int
	_, err = fmt.Sscanf(out, "%d", &count)
	if err != nil {
		return 0, fmt.Errorf("parsing unpushed count: %w", err)
	}

	return count, nil
}

// StashCount returns the number of stashes belonging to the current branch.
// Filters by current branch name to only count stashes that belong to this
// worktree, since git stashes are repo-wide (.git/refs/stash).
func (g *Git) StashCount() (int, error) {
	out, err := g.run("stash", "list")
	if err != nil {
		return 0, err
	}

	if out == "" {
		return 0, nil
	}

	branch, branchErr := g.CurrentBranch()
	filterByBranch := branchErr == nil && branch != "" && branch != "HEAD"

	wipPrefix := ": WIP on " + branch + ":"
	onPrefix := ": On " + branch + ":"

	lines := strings.Split(out, "\n")
	count := 0
	for _, line := range lines {
		if line == "" {
			continue
		}
		if filterByBranch {
			if !strings.Contains(line, wipPrefix) && !strings.Contains(line, onPrefix) {
				continue
			}
		}
		count++
	}
	return count, nil
}

// UncommittedWorkStatus contains information about uncommitted work in a repo.
type UncommittedWorkStatus struct {
	HasUncommittedChanges bool
	StashCount            int
	UnpushedCommits       int
	ModifiedFiles         []string
	UntrackedFiles        []string
}

// Clean returns true if there is no uncommitted work.
func (s *UncommittedWorkStatus) Clean() bool {
	return !s.HasUncommittedChanges && s.StashCount == 0 && s.UnpushedCommits == 0
}

// String returns a human-readable summary of uncommitted work.
func (s *UncommittedWorkStatus) String() string {
	var issues []string
	if s.HasUncommittedChanges {
		issues = append(issues, fmt.Sprintf("%d uncommitted change(s)", len(s.ModifiedFiles)+len(s.UntrackedFiles)))
	}
	if s.StashCount > 0 {
		issues = append(issues, fmt.Sprintf("%d stash(es)", s.StashCount))
	}
	if s.UnpushedCommits > 0 {
		issues = append(issues, fmt.Sprintf("%d unpushed commit(s)", s.UnpushedCommits))
	}
	if len(issues) == 0 {
		return "clean"
	}
	return strings.Join(issues, ", ")
}

// CheckUncommittedWork performs a comprehensive check for uncommitted work.
func (g *Git) CheckUncommittedWork() (*UncommittedWorkStatus, error) {
	status := &UncommittedWorkStatus{}

	gitStatus, err := g.Status()
	if err != nil {
		return nil, fmt.Errorf("checking git status: %w", err)
	}
	status.HasUncommittedChanges = !gitStatus.Clean
	status.ModifiedFiles = append(gitStatus.Modified, gitStatus.Added...)
	status.ModifiedFiles = append(status.ModifiedFiles, gitStatus.Deleted...)
	status.UntrackedFiles = gitStatus.Untracked

	stashCount, err := g.StashCount()
	if err != nil {
		return nil, fmt.Errorf("checking stashes: %w", err)
	}
	status.StashCount = stashCount

	unpushed, err := g.UnpushedCommits()
	if err != nil {
		return nil, fmt.Errorf("checking unpushed commits: %w", err)
	}
	status.UnpushedCommits = unpushed

	return status, nil
}

// PrunedBranch represents a local branch that was pruned (or would be pruned in dry-run).
type PrunedBranch struct {
	Name   string // Branch name (e.g., "lw/feature-abc123")
	Reason string // Why it was pruned: "merged", "no-remote", "no-remote-merged"
}

// IsAncestor checks if ancestor is an ancestor of descendant.
func (g *Git) IsAncestor(ancestor, descendant string) (bool, error) {
	_, err := g.run("merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		if strings.Contains(err.Error(), "exit status 1") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RemoteTrackingBranchExists checks if a remote-tracking branch ref exists locally.
func (g *Git) RemoteTrackingBranchExists(remote, branch string) (bool, error) {
	ref := fmt.Sprintf("refs/remotes/%s/%s", remote, branch)
	_, err := g.run("show-ref", "--verify", "--quiet", ref)
	if err != nil {
		if strings.Contains(err.Error(), "exit status 1") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// PruneStaleBranches finds and deletes local branches matching a pattern that are
// stale -- either fully merged to the default branch or whose remote tracking branch
// no longer exists (indicating the remote branch was deleted after merge).
//
// Safety: never deletes the current branch or the default branch.
// Uses git branch -d (not -D), so only fully-merged branches are deleted.
func (g *Git) PruneStaleBranches(pattern string, dryRun bool) ([]PrunedBranch, error) {
	if pattern == "" {
		pattern = "lw/*"
	}

	currentBranch, _ := g.CurrentBranch()
	defaultBranch := g.RemoteDefaultBranch()

	branches, err := g.ListBranches(pattern)
	if err != nil {
		return nil, fmt.Errorf("listing branches: %w", err)
	}

	var pruned []PrunedBranch
	for _, branch := range branches {
		branch = strings.TrimSpace(branch)
		if branch == "" || branch == currentBranch || branch == defaultBranch {
			continue
		}

		hasRemote, err := g.RemoteTrackingBranchExists("origin", branch)
		if err != nil {
			continue
		}

		merged, err := g.IsAncestor(branch, "origin/"+defaultBranch)
		if err != nil {
			continue
		}

		var reason string
		if merged && !hasRemote {
			reason = "no-remote-merged"
		} else if merged {
			reason = "merged"
		} else if !hasRemote {
			reason = "no-remote"
		} else {
			continue
		}

		if !dryRun {
			if err := g.DeleteBranch(branch, false); err != nil {
				continue
			}
		}

		pruned = append(pruned, PrunedBranch{
			Name:   branch,
			Reason: reason,
		})
	}

	return pruned, nil
}
