package agent

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeOptions configures CreateWorktree.
type WorktreeOptions struct {
	Repo    string // absolute path to the git repo to add the worktree to
	TaskID  string // for branch naming + worktree dir name
	Persona string // for branch naming
	AgentID string // full UUID; used as ShortID() in the branch + dir name
}

// CreateWorktree runs `git worktree add -b <branch> <path>` in Repo and
// returns the worktree path + branch name.
//
// Branch: `feature/agent-<lowercase-task-id>-<persona>-<short-agent-id>`
// Path:   `<Repo>/.worktrees/agent-<short-agent-id>`
//
// `feature/...` matches the existing branch naming convention enforced by
// `lw worktree` (`feature|fix|hotfix/<…>`). The agent-id suffix lets v_core
// spawn multiple concurrent sessions on the same task without name clashes.
func CreateWorktree(opts WorktreeOptions) (worktree, branch string, err error) {
	if opts.Repo == "" {
		return "", "", fmt.Errorf("repo path is required")
	}
	if opts.AgentID == "" {
		return "", "", fmt.Errorf("agent id is required")
	}

	short := opts.AgentID
	if len(short) > 8 {
		short = short[:8]
	}

	branch = fmt.Sprintf("feature/agent-%s-%s-%s",
		strings.ToLower(opts.TaskID),
		slug(opts.Persona),
		short,
	)
	worktree = filepath.Join(opts.Repo, ".worktrees", "agent-"+short)

	cmd := exec.Command("git", "-C", opts.Repo,
		"worktree", "add", "-b", branch, worktree)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return "", "", fmt.Errorf("git worktree add: %w\n%s", runErr, string(out))
	}
	return worktree, branch, nil
}

// RemoveWorktree runs `git worktree remove --force <path>` and deletes
// the branch. Errors are surfaced (callers may choose to ignore).
func RemoveWorktree(repo, worktree, branch string) error {
	if out, err := exec.Command("git", "-C", repo,
		"worktree", "remove", "--force", worktree).CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove %s: %w\n%s", worktree, err, string(out))
	}
	if branch != "" {
		// Best-effort: the worktree's branch may already be merged or
		// the caller may want to keep it. -D forces deletion regardless.
		_ = exec.Command("git", "-C", repo, "branch", "-D", branch).Run()
	}
	return nil
}

// slug lowercases and replaces non-alnum runs with single dashes.
func slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
