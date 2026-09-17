package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"

	"github.com/lightwave-media/lightwave-cli/internal/git"
	"github.com/lightwave-media/lightwave-cli/internal/worktreelock"
)

// removeWorktreeSafely is the ONE place a worktree is removed, and it answers
// two questions before it does:
//
//	is someone working here?   — a fact, from the liveness lock (pid + TTL)
//	would this lose anything?  — a fact, from `git status --porcelain`
//
// Both used to be inferred. `lw worktree prune` guessed abandonment from idle
// days; raw `git worktree remove --force` asked nothing at all. Measured on
// this host in September: 25 raw forced removals and 4 `rm -rf` under
// .worktrees, against 3 uses of the sanctioned verbs — and a session lost
// ~45 minutes of uncommitted work to one of them, mid-edit.
//
// A live holder is refused outright: `--force` overrides DIRTINESS, never
// somebody else's session. Nothing about "I am in a hurry" makes it safe to
// delete the tree another agent is typing into.
//
// Dirty-and-forced is rescued first. That is the property that matters most,
// because it does not depend on liveness detection being right: prevention has
// to be correct forever, recovery only has to work once.
func removeWorktreeSafely(g *git.Git, path, caller string, force bool) error {
	if holder := worktreelock.Holder(path); holder != nil {
		return fmt.Errorf(
			"%s is held by a live session (%s, pid %d, active %s ago)\n"+
				"  --force overrides a dirty tree, never another session's work.\n"+
				"  If that session is gone, remove %s",
			path, shortSession(holder.SessionID), holder.PID,
			holder.Age().Round(time.Second), worktreelock.LockPath(path))
	}

	if force {
		rescue, err := g.RescueUncommitted(path, caller)
		if err != nil {
			// Refuse rather than proceed: a forced removal whose safety net
			// failed is exactly the operation that loses work silently.
			return fmt.Errorf("could not rescue uncommitted work in %s, refusing to force-remove: %w", path, err)
		}

		if rescue != nil {
			color.Yellow("rescued %d uncommitted path(s) from %s", rescue.Files, path)
			fmt.Printf("  ref: %s\n  sha: %s\n  restore: %s\n",
				rescue.Ref, rescue.SHA[:12], rescue.Restore())
		}
	}

	if err := g.WorktreeRemove(path, force); err != nil {
		return fmt.Errorf("git worktree remove: %w\n  (use --force to remove a dirty worktree; its work is rescued first)", err)
	}

	if err := g.WorktreePrune(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: git worktree prune: %v\n", err)
	}

	return nil
}

// sessionID identifies whoever is claiming a worktree.
//
// CLAUDE_SESSION_ID is what the hooks stamp, so a lock written by `lw` and one
// written by a hook name the same session and can be matched. The agent-id and
// pid fallbacks keep the lock meaningful for a human at a terminal, where an
// empty session_id would read as "held by nobody" — the value that makes a
// sweep delete the tree.
func sessionID() string {
	for _, key := range []string{"CLAUDE_SESSION_ID", "LW_SESSION_ID", "PAPERCLIP_AGENT_ID"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}

	return fmt.Sprintf("pid-%d", os.Getpid())
}

// shortSession trims a session uuid to the prefix the hooks print, so a
// denial here and a denial from checkout-lock name the same session the same
// way and can be matched by eye.
func shortSession(id string) string {
	const shown = 8
	if len(id) <= shown {
		return id
	}

	return id[:shown]
}
