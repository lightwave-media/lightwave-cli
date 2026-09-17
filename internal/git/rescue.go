package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RescueRefPrefix is where rescued work is anchored. A ref, not a bare
// dangling commit: an unreferenced commit is reachable only by a SHA someone
// wrote down, and `git gc` is free to collect it. Under refs/ it survives gc
// and `git for-each-ref refs/rescue` lists everything ever rescued.
const RescueRefPrefix = "refs/rescue"

// refListFields is the field count of a `for-each-ref` line in our format.
const refListFields = 2

// gitAdd is the subcommand name; hoisted because goconst counts its uses
// across this package.
const gitAdd = "add"

// Rescue is one captured snapshot of a worktree's uncommitted state.
type Rescue struct {
	SHA   string // commit object holding the snapshot
	Ref   string // refs/rescue/... anchoring it against gc
	Files int    // how many paths differed from HEAD
}

// Restore is the command that brings the work back.
func (r Rescue) Restore() string {
	return fmt.Sprintf("git checkout %s -- .   # or: git cherry-pick %s", r.SHA, r.SHA)
}

// RescueUncommitted captures everything in a worktree that is not in HEAD —
// modified tracked files AND untracked ones — as a commit, and anchors it.
// Returns nil when the tree is clean; there is nothing to lose.
//
// Why not `git stash`. The stash is a STACK shared by every worktree of a
// repository, and this machine runs many sessions against one repo at a time:
// pushing onto it means another session's `stash pop` can take work that was
// never theirs. `git stash create` avoids the stack but silently omits
// untracked files, which is the half most worth rescuing — a file created and
// not yet added exists in exactly one place.
//
// So: build a throwaway index, stage everything into it, write a tree, and
// commit that tree with plumbing. The worktree's real index is untouched, the
// stash stack is untouched, and the result is an ordinary commit anyone can
// `git checkout` or `git cherry-pick`.
func (g *Git) RescueUncommitted(worktreePath, reason string) (*Rescue, error) {
	wt := NewGit(worktreePath)

	status, err := wt.run("status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("reading worktree status: %w", err)
	}

	changed := 0

	for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
		if strings.TrimSpace(line) != "" {
			changed++
		}
	}

	if changed == 0 {
		return nil, nil
	}

	// A temp index in the OS temp dir, not inside the worktree: the worktree is
	// about to be removed, and a scratch file there would be removed with it.
	indexFile, err := os.CreateTemp("", "lw-rescue-index-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp index: %w", err)
	}

	indexPath := indexFile.Name()
	_ = indexFile.Close()
	_ = os.Remove(indexPath) // git wants to create it itself

	defer func() { _ = os.Remove(indexPath) }()

	env := []string{"GIT_INDEX_FILE=" + indexPath}

	// Seed from HEAD so the snapshot is a delta against the branch tip rather
	// than a tree containing only the changed paths.
	if _, err := wt.runWithEnv([]string{"read-tree", "HEAD"}, env); err != nil {
		return nil, fmt.Errorf("seeding rescue index from HEAD: %w", err)
	}

	if _, err := wt.runWithEnv([]string{gitAdd, "-A"}, env); err != nil {
		return nil, fmt.Errorf("staging worktree into rescue index: %w", err)
	}

	tree, err := wt.runWithEnv([]string{"write-tree"}, env)
	if err != nil {
		return nil, fmt.Errorf("writing rescue tree: %w", err)
	}

	tree = strings.TrimSpace(tree)

	head, err := wt.run("rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("resolving HEAD: %w", err)
	}

	message := fmt.Sprintf("rescue: uncommitted work in %s\n\nreason: %s\n",
		filepath.Base(worktreePath), reason)

	sha, err := wt.run("commit-tree", tree, "-p", strings.TrimSpace(head), "-m", message)
	if err != nil {
		return nil, fmt.Errorf("committing rescue tree: %w", err)
	}

	sha = strings.TrimSpace(sha)

	ref := fmt.Sprintf("%s/%s-%s", RescueRefPrefix,
		filepath.Base(worktreePath), time.Now().UTC().Format("20060102T150405"))

	// Anchor in the MAIN repo, not the worktree: the worktree's admin dir goes
	// away with it, and refs written through it would be harder to find later.
	if _, err := g.run("update-ref", ref, sha); err != nil {
		return nil, fmt.Errorf("anchoring rescue ref %s: %w", ref, err)
	}

	return &Rescue{SHA: sha, Ref: ref, Files: changed}, nil
}

// ListRescues returns every rescue ref, newest first.
func (g *Git) ListRescues() ([]Rescue, error) {
	out, err := g.run("for-each-ref", "--sort=-creatordate",
		"--format=%(refname) %(objectname)", RescueRefPrefix)
	if err != nil {
		return nil, err
	}

	var rescues []Rescue

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Fields(line)
		if len(parts) != refListFields {
			continue
		}

		rescues = append(rescues, Rescue{Ref: parts[0], SHA: parts[1]})
	}

	return rescues, nil
}
