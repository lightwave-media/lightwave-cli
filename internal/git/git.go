// Package git provides a wrapper for git operations via subprocess.
package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitError contains raw output from a git command for observation.
// Callers observe the raw output and decide what to do.
// The error interface methods provide human-readable messages, but callers
// should use Stdout/Stderr for programmatic observation.
type GitError struct {
	Command string // The git command that failed (e.g., "push", "checkout")
	Args    []string
	Stdout  string // Raw stdout output
	Stderr  string // Raw stderr output
	Err     error  // Underlying error (e.g., exit code)
}

func (e *GitError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("git %s: %s", e.Command, e.Stderr)
	}
	return fmt.Sprintf("git %s: %v", e.Command, e.Err)
}

func (e *GitError) Unwrap() error {
	return e.Err
}

// Git wraps git operations for a working directory.
type Git struct {
	workDir string
	gitDir  string // Optional: explicit git directory (for bare repos)
}

// NewGit creates a new Git wrapper for the given directory.
func NewGit(workDir string) *Git {
	return &Git{workDir: workDir}
}

// NewGitWithDir creates a Git wrapper with an explicit git directory.
// This is used for bare repos where gitDir points to the .git directory
// and workDir may be empty or point to a worktree.
func NewGitWithDir(gitDir, workDir string) *Git {
	return &Git{gitDir: gitDir, workDir: workDir}
}

// WorkDir returns the working directory for this Git instance.
func (g *Git) WorkDir() string {
	return g.workDir
}

// IsRepo returns true if the workDir is a git repository.
func (g *Git) IsRepo() bool {
	_, err := g.run("rev-parse", "--git-dir")
	return err == nil
}

// run executes a git command and returns stdout.
func (g *Git) run(args ...string) (string, error) {
	if g.gitDir != "" {
		args = append([]string{"--git-dir=" + g.gitDir}, args...)
	}

	cmd := exec.Command("git", args...)
	if g.workDir != "" {
		cmd.Dir = g.workDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", g.wrapError(err, stdout.String(), stderr.String(), args)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// runWithEnv executes a git command with additional environment variables.
func (g *Git) runWithEnv(args []string, extraEnv []string) (string, error) {
	if g.gitDir != "" {
		args = append([]string{"--git-dir=" + g.gitDir}, args...)
	}
	cmd := exec.Command("git", args...)
	if g.workDir != "" {
		cmd.Dir = g.workDir
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", g.wrapError(err, stdout.String(), stderr.String(), args)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// wrapError wraps git errors with context.
// Returns GitError with raw output for observation.
func (g *Git) wrapError(err error, stdout, stderr string, args []string) error {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)

	command := ""
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			command = arg
			break
		}
	}
	if command == "" && len(args) > 0 {
		command = args[0]
	}

	return &GitError{
		Command: command,
		Args:    args,
		Stdout:  stdout,
		Stderr:  stderr,
		Err:     err,
	}
}

// cloneOptions configures a clone operation.
type cloneOptions struct {
	bare         bool
	singleBranch bool
	depth        int
	branch       string
}

// cloneInternal runs `git clone` in an isolated temp directory, then moves
// the result to dest. Configures refspec for bare repos.
func (g *Git) cloneInternal(url, dest string, opts cloneOptions) error {
	destParent := filepath.Dir(dest)
	if err := os.MkdirAll(destParent, 0755); err != nil {
		return fmt.Errorf("creating destination parent: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "lw-clone-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tmpDest := filepath.Join(tmpDir, filepath.Base(dest))

	args := []string{"clone"}
	if opts.bare {
		args = append(args, "--bare")
	}
	if opts.singleBranch {
		args = append(args, "--single-branch")
	}
	if opts.depth > 0 {
		args = append(args, "--depth", fmt.Sprintf("%d", opts.depth))
	}
	if opts.branch != "" {
		args = append(args, "--branch", opts.branch)
	}
	args = append(args, url, tmpDest)

	cmd := exec.Command("git", args...)
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), "GIT_CEILING_DIRECTORIES="+tmpDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return g.wrapError(err, stdout.String(), stderr.String(), args)
	}

	if err := os.Rename(tmpDest, dest); err != nil {
		return fmt.Errorf("moving clone to destination: %w", err)
	}

	if opts.bare {
		return configureRefspec(dest, opts.singleBranch)
	}

	return nil
}

// Clone clones a repository to the destination.
// Uses --single-branch --depth 1 for efficiency.
func (g *Git) Clone(url, dest string) error {
	return g.cloneInternal(url, dest, cloneOptions{singleBranch: true, depth: 1})
}

// CloneBare clones a repository as a bare repo (no working directory).
// Used for the shared repo architecture where all worktrees share a single git database.
func (g *Git) CloneBare(url, dest string) error {
	return g.cloneInternal(url, dest, cloneOptions{bare: true, singleBranch: true, depth: 1})
}

// configureRefspec sets remote.origin.fetch to the standard refspec for bare repos.
// Bare clones don't have this set by default, which breaks worktrees that need to
// fetch and see origin/* refs.
func configureRefspec(repoPath string, singleBranch bool) error {
	gitDir := repoPath
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
		gitDir = filepath.Join(repoPath, ".git")
	}
	gitDir = filepath.Clean(gitDir)

	var stderr bytes.Buffer
	configCmd := exec.Command("git", "--git-dir", gitDir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	configCmd.Stderr = &stderr
	if err := configCmd.Run(); err != nil {
		return fmt.Errorf("configuring refspec: %s", strings.TrimSpace(stderr.String()))
	}

	if singleBranch {
		var headOut bytes.Buffer
		headCmd := exec.Command("git", "--git-dir", gitDir, "symbolic-ref", "HEAD")
		headCmd.Stdout = &headOut
		headCmd.Stderr = &stderr
		if err := headCmd.Run(); err != nil {
			fetchCmd := exec.Command("git", "--git-dir", gitDir, "fetch", "--depth", "1", "origin")
			fetchCmd.Stderr = &stderr
			if fetchErr := fetchCmd.Run(); fetchErr != nil {
				return fmt.Errorf("fetching origin: %s", strings.TrimSpace(stderr.String()))
			}
			return nil
		}
		headRef := strings.TrimSpace(headOut.String())
		branch := strings.TrimPrefix(headRef, "refs/heads/")
		refspec := branch + ":refs/remotes/origin/" + branch

		fetchCmd := exec.Command("git", "--git-dir", gitDir, "fetch", "--depth", "1", "origin", refspec)
		fetchCmd.Stderr = &stderr
		if err := fetchCmd.Run(); err != nil {
			return fmt.Errorf("fetching origin %s: %s", branch, strings.TrimSpace(stderr.String()))
		}
		return nil
	}

	fetchCmd := exec.Command("git", "--git-dir", gitDir, "fetch", "origin")
	fetchCmd.Stderr = &stderr
	if err := fetchCmd.Run(); err != nil {
		return fmt.Errorf("fetching origin: %s", strings.TrimSpace(stderr.String()))
	}

	return nil
}

// Fetch fetches from the remote.
func (g *Git) Fetch(remote string) error {
	_, err := g.run("fetch", remote)
	return err
}

// FetchBranch fetches a specific branch from the remote.
func (g *Git) FetchBranch(remote, branch string) error {
	_, err := g.run("fetch", remote, branch)
	return err
}

// GitStatus represents the status of the working directory.
type GitStatus struct {
	Clean     bool
	Modified  []string
	Added     []string
	Deleted   []string
	Untracked []string
}

// Status returns the current git status.
func (g *Git) Status() (*GitStatus, error) {
	out, err := g.run("status", "--porcelain")
	if err != nil {
		return nil, err
	}

	status := &GitStatus{Clean: true}
	if out == "" {
		return status, nil
	}

	status.Clean = false
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 3 {
			continue
		}
		code := line[:2]
		file := line[3:]

		switch {
		case strings.Contains(code, "M"):
			status.Modified = append(status.Modified, file)
		case strings.Contains(code, "A"):
			status.Added = append(status.Added, file)
		case strings.Contains(code, "D"):
			status.Deleted = append(status.Deleted, file)
		case strings.Contains(code, "?"):
			status.Untracked = append(status.Untracked, file)
		}
	}

	return status, nil
}

// CurrentBranch returns the current branch name.
func (g *Git) CurrentBranch() (string, error) {
	return g.run("rev-parse", "--abbrev-ref", "HEAD")
}

// DefaultBranch returns the default branch name (what HEAD points to).
// Works for both regular and bare repositories.
// Returns "main" as fallback if detection fails.
func (g *Git) DefaultBranch() string {
	branch, err := g.run("symbolic-ref", "--short", "HEAD")
	if err == nil && branch != "" {
		return branch
	}
	return "main"
}

// RemoteDefaultBranch returns the default branch from the remote (origin).
// Useful in worktrees where HEAD may not reflect the repo's actual default.
// Returns "main" as final fallback.
func (g *Git) RemoteDefaultBranch() string {
	out, err := g.run("symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil && out != "" {
		parts := strings.Split(out, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}

	_, err = g.run("rev-parse", "--verify", "origin/master")
	if err == nil {
		return "master"
	}

	_, err = g.run("rev-parse", "--verify", "origin/main")
	if err == nil {
		return "main"
	}

	return "main"
}

// HasUncommittedChanges returns true if there are uncommitted changes.
func (g *Git) HasUncommittedChanges() (bool, error) {
	status, err := g.Status()
	if err != nil {
		return false, err
	}
	return !status.Clean, nil
}

// RemoteURL returns the URL for the given remote.
func (g *Git) RemoteURL(remote string) (string, error) {
	return g.run("remote", "get-url", remote)
}

// Add stages files for commit.
func (g *Git) Add(paths ...string) error {
	args := append([]string{"add"}, paths...)
	_, err := g.run(args...)
	return err
}

// Commit creates a commit with the given message.
func (g *Git) Commit(message string) error {
	_, err := g.run("commit", "-m", message)
	return err
}

// CommitAll stages all changes and commits.
func (g *Git) CommitAll(message string) error {
	_, err := g.run("commit", "-am", message)
	return err
}

// Push pushes to the remote branch.
func (g *Git) Push(remote, branch string, force bool) error {
	args := []string{"push", remote, branch}
	if force {
		args = append(args, "--force")
	}
	_, err := g.run(args...)
	return err
}

// Checkout checks out the given ref.
func (g *Git) Checkout(ref string) error {
	_, err := g.run("checkout", ref)
	return err
}

// CheckoutNewBranch creates a new branch from startPoint and checks it out.
func (g *Git) CheckoutNewBranch(branch, startPoint string) error {
	_, err := g.run("checkout", "-b", branch, startPoint)
	return err
}

// BranchExists checks if a branch exists locally.
func (g *Git) BranchExists(name string) (bool, error) {
	_, err := g.run("show-ref", "--verify", "--quiet", "refs/heads/"+name)
	if err != nil {
		if strings.Contains(err.Error(), "exit status 1") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DeleteBranch deletes a local branch.
func (g *Git) DeleteBranch(name string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := g.run("branch", flag, name)
	return err
}

// ListBranches returns all local branches matching a pattern.
// Pattern uses git's pattern matching (e.g., "lw/*" matches all lw branches).
// Returns branch names without the refs/heads/ prefix.
func (g *Git) ListBranches(pattern string) ([]string, error) {
	args := []string{"branch", "--list", "--format=%(refname:short)"}
	if pattern != "" {
		args = append(args, pattern)
	}
	out, err := g.run(args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// ResetHard resets the current working tree and index to the given ref.
func (g *Git) ResetHard(ref string) error {
	_, err := g.run("reset", "--hard", ref)
	return err
}

// Rev returns the commit hash for the given ref.
func (g *Git) Rev(ref string) (string, error) {
	return g.run("rev-parse", ref)
}

// ConfigGet returns the value of a git config key.
// Returns empty string if the key is not set.
func (g *Git) ConfigGet(key string) (string, error) {
	out, err := g.run("config", "--get", key)
	if err != nil {
		return "", nil
	}
	return out, nil
}
