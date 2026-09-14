package git

// sweep.go — branch and worktree classification for `lw git sweep` (#321).
//
// The verb turns on one question per branch: did this work reach the base? Get
// it wrong in the permissive direction and unrouted work is destroyed, so
// nothing is deletable without positive proof — and "no proof found" is
// reported as unproven, never treated as nothing to lose.
//
// #321 proposed `git cherry origin/main` as the test. Measured on this repo, it
// cannot work: everything merges by SQUASH here, so a merged branch's commits
// are not in the base and `git cherry` calls them unique. Side by side —
//
//	docs/readme-conformance (squash-merged as #428):  ancestor NO, cherry +2
//	an isolated never-merged control:                 ancestor NO, cherry +1
//
// — the same shape, opposite meanings. So cherry output is reported as context
// for a human and is an input to no deletion.
//
// Three proofs are sound, tried cheapest first:
//
//	ancestor      the tip is reachable from the base (fast-forward, merge commit)
//	tree-on-base  a commit in the base's history carries the tip's exact tree —
//	              the squash landed that content verbatim. Local, no network,
//	              and it cleared 3 of the 5 squash-merged branches measured here
//	merged-pr     the forge reports a merged PR whose head was this branch. The
//	              only proof for a branch that was behind the base when it
//	              merged, because the squash tree then matches neither side
//
// Offline drops the third and says so rather than guessing.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// nulSep is the byte git writes for %00 / %x00 — the field separator for the
// multi-field formats below, chosen because no ref name or subject can contain
// it. It is only ever SPLIT on; see scanBranches for why it is never passed in.
const nulSep = "\x00"

// gitWorktree is both the subcommand and the porcelain record key.
const gitWorktree = "worktree"

// MergeProof names the evidence that a branch's work reached the base.
type MergeProof string

const (
	// ProofNone means no evidence was found, which is not evidence of absence.
	ProofNone MergeProof = ""
	// ProofAncestor — the branch tip is reachable from the base.
	ProofAncestor MergeProof = "ancestor"
	// ProofSameTree — a commit in the base's history has the branch's tree.
	ProofSameTree MergeProof = "tree-on-base"
	// ProofMergedPR — the forge reports a merged PR with this branch as head.
	ProofMergedPR MergeProof = "merged-pr"
)

// MergedPRLookup asks the forge whether branch was the head of a merged PR.
// A nil lookup on a Sweeper means offline: the third proof is skipped, and a
// branch only it could have cleared stays unproven and is reported.
type MergedPRLookup func(branch string) (number int, merged bool, err error)

// BranchState is one local branch's classification.
type BranchState struct {
	Name string `json:"name"`
	// Upstream is the configured tracking ref, "" when the branch has none.
	Upstream string `json:"upstream,omitempty"`
	// Proof and Evidence are the answer and the receipt: a base commit for
	// tree-on-base, the base ref for ancestor, "#N" for a merged PR.
	Proof    MergeProof `json:"proof,omitempty"`
	Evidence string     `json:"evidence,omitempty"`
	// HeldBy is the worktree that has this branch checked out, "" when none.
	HeldBy string `json:"held_by,omitempty"`
	// Unique counts commits `git cherry` calls unpatched. Reported for
	// unproven branches so a human can see how much work is at stake; it
	// decides nothing, for the reason in this file's header comment.
	Unique int `json:"unique_commits,omitempty"`
	// Gone means the upstream was configured and the remote no longer has it.
	Gone    bool `json:"gone"`
	Current bool `json:"current"`
	Base    bool `json:"base"`
}

// Deletable reports whether --execute may remove this branch.
//
// Proof alone is not the bar. The standing git-cleanup rule restricts deletion
// to branches whose upstream is [gone] — the remote already dropped it, which
// under org-wide auto-delete-on-merge means a merge fired or a human deleted it
// deliberately. Proof is the other half: [gone] with no proof is precisely the
// shape of work whose PR was closed unmerged.
func (b *BranchState) Deletable() bool {
	return b.Gone && b.Proof != ProofNone && b.HeldBy == "" && !b.Current && !b.Base
}

// Why explains a branch that is not Deletable, in the order a reader deciding
// what to do next cares about. Empty when the branch is deletable.
func (b *BranchState) Why() string {
	switch {
	case b.Base:
		return "the base branch"
	case b.Current:
		return "checked out here"
	case b.HeldBy != "":
		return "checked out in " + b.HeldBy
	case b.Upstream == "":
		return "local only — no upstream that could have been merged"
	case !b.Gone:
		return "upstream " + b.Upstream + " still exists"
	case b.Proof == ProofNone:
		return "upstream gone, merge unproven"
	default:
		return ""
	}
}

// WorktreeState is one registered worktree's classification.
type WorktreeState struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	// Prunable carries git's own reason when the registration is dead (the
	// path no longer exists). Empty for a live worktree.
	Prunable string `json:"prunable,omitempty"`
	// Locked carries the lock reason; a locked worktree is never touched.
	Locked string `json:"locked,omitempty"`
	Main   bool   `json:"main"`
	Dirty  bool   `json:"dirty"`
}

// Sweeper classifies one repository. Build it with NewSweeper.
type Sweeper struct {
	git    *Git
	lookup MergedPRLookup
	trees  map[string]string
	base   string
}

// NewSweeper resolves the base ref and returns a classifier for the repo.
//
// The base is the remote default branch when it resolves, and the local branch
// of the same name otherwise — a repo with no origin still classifies against
// its own trunk rather than erroring on a ref nobody has.
func NewSweeper(g *Git, lookup MergedPRLookup) (*Sweeper, error) {
	name := g.RemoteDefaultBranch()

	for _, ref := range []string{"origin/" + name, name} {
		if _, err := g.run("rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
			return &Sweeper{git: g, lookup: lookup, base: ref}, nil
		}
	}

	return nil, fmt.Errorf("git sweep: no base branch — neither origin/%s nor %s resolves", name, name)
}

// Base returns the ref every branch is classified against.
func (s *Sweeper) Base() string { return s.base }

// baseTrees maps every tree OID in the base's history to the commit carrying
// it. Built once per sweep: the per-branch alternative is a full history walk
// for each branch, and this is a single `git log` for all of them.
func (s *Sweeper) baseTrees() (map[string]string, error) {
	if s.trees != nil {
		return s.trees, nil
	}

	out, err := s.git.run("log", "--format=%T %h", s.base)
	if err != nil {
		return nil, fmt.Errorf("git sweep: reading %s history: %w", s.base, err)
	}

	trees := map[string]string{}

	for line := range strings.SplitSeq(out, "\n") {
		oid, commit, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}

		if _, seen := trees[oid]; !seen {
			trees[oid] = commit
		}
	}

	s.trees = trees

	return trees, nil
}

// proveMerged runs the three proofs in cost order and stops at the first hit.
func (s *Sweeper) proveMerged(branch string) (MergeProof, string, error) {
	onBase, err := s.git.IsAncestor(branch, s.base)
	if err != nil {
		return ProofNone, "", err
	}

	if onBase {
		return ProofAncestor, s.base, nil
	}

	trees, err := s.baseTrees()
	if err != nil {
		return ProofNone, "", err
	}

	tree, err := s.git.run("rev-parse", branch+"^{tree}")
	if err != nil {
		return ProofNone, "", err
	}

	if commit, ok := trees[tree]; ok {
		return ProofSameTree, commit, nil
	}

	if s.lookup == nil {
		return ProofNone, "", nil
	}

	number, merged, err := s.lookup(branch)
	if err != nil {
		return ProofNone, "", err
	}

	if merged {
		return ProofMergedPR, fmt.Sprintf("#%d", number), nil
	}

	return ProofNone, "", nil
}

// uniqueCommits counts what `git cherry` marks unpatched (`+`) on the branch.
// Context for a human reading an unproven branch, never a deletion input.
func (s *Sweeper) uniqueCommits(branch string) int {
	out, err := s.git.run("cherry", s.base, branch)
	if err != nil {
		return 0
	}

	n := 0

	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "+ ") {
			n++
		}
	}

	return n
}

// branchRef is one row of the for-each-ref scan, before proofs are attempted.
type branchRef struct {
	name     string
	upstream string
	gone     bool
}

// scanBranches reads every local branch with its tracking state in one call.
//
// `%(upstream:track)` is the only place git reports [gone] — a branch whose
// tracking ref was configured and has since been deleted on the remote. It is
// NOT the same as having no upstream, and the two must not be collapsed: a
// branch with no upstream never had a remote to be merged from.
func (s *Sweeper) scanBranches() ([]branchRef, error) {
	// %00 is git's OWN escape for a NUL byte in the emitted line. A literal
	// "\x00" here would be a NUL inside argv, which exec refuses outright
	// ("fork/exec: invalid argument") — the scan then fails on every call.
	out, err := s.git.run("for-each-ref",
		"--format=%(refname:short)%00%(upstream:short)%00%(upstream:track)",
		"refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("git sweep: listing branches: %w", err)
	}

	var refs []branchRef

	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(line, nulSep)

		const wantFields = 3
		if len(fields) != wantFields || fields[0] == "" {
			continue
		}

		refs = append(refs, branchRef{
			name:     fields[0],
			upstream: fields[1],
			gone:     strings.Contains(fields[2], "[gone]"),
		})
	}

	return refs, nil
}

// Branches classifies every local branch, in git's own (alphabetical) order.
func (s *Sweeper) Branches() ([]BranchState, error) {
	refs, err := s.scanBranches()
	if err != nil {
		return nil, err
	}

	held, err := s.heldBranches()
	if err != nil {
		return nil, err
	}

	current, _ := s.git.CurrentBranch()
	baseName := strings.TrimPrefix(s.base, "origin/")

	states := make([]BranchState, 0, len(refs))

	for _, ref := range refs {
		state := BranchState{
			Name:     ref.name,
			Upstream: ref.upstream,
			Gone:     ref.gone,
			HeldBy:   held[ref.name],
			Current:  ref.name == current,
			Base:     ref.name == baseName,
		}

		// The base is the recovery anchor and is never a deletion candidate,
		// so it is also not worth the proof calls.
		if !state.Base {
			proof, evidence, proofErr := s.proveMerged(ref.name)
			if proofErr != nil {
				return nil, proofErr
			}

			state.Proof, state.Evidence = proof, evidence

			if proof == ProofNone {
				state.Unique = s.uniqueCommits(ref.name)
			}
		}

		states = append(states, state)
	}

	return states, nil
}

// heldBranches maps branch name to the worktree path that has it checked out.
// A branch checked out anywhere cannot be deleted, and the fleet routinely runs
// 15+ concurrent worktrees, so this is the common reason a merged branch stays.
func (s *Sweeper) heldBranches() (map[string]string, error) {
	trees, err := s.Worktrees()
	if err != nil {
		return nil, err
	}

	held := map[string]string{}

	for _, w := range trees {
		if w.Branch != "" {
			held[w.Branch] = w.Path
		}
	}

	return held, nil
}

// listWorktrees reads the registrations without inspecting any working tree.
func (s *Sweeper) listWorktrees() ([]WorktreeState, error) {
	out, err := s.git.run(gitWorktree, "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git sweep: listing worktrees: %w", err)
	}

	return parseWorktreePorcelain(out), nil
}

// Worktrees classifies every registered worktree. The first entry git reports
// is the main working tree.
func (s *Sweeper) Worktrees() ([]WorktreeState, error) {
	states, err := s.listWorktrees()
	if err != nil {
		return nil, err
	}

	for i := range states {
		if states[i].Prunable != "" {
			continue // the path is gone; nothing to inspect
		}

		status, statusErr := NewGit(states[i].Path).run("status", "--porcelain")
		if statusErr != nil {
			continue
		}

		states[i].Dirty = status != ""
	}

	return states, nil
}

// parseWorktreePorcelain reads `git worktree list --porcelain`. Records are
// blank-line separated; `prunable` and `locked` each carry a reason string,
// verified against a real dead registration and a real locked tree.
func parseWorktreePorcelain(out string) []WorktreeState {
	var (
		states  []WorktreeState
		current WorktreeState
	)

	flush := func() {
		if current.Path != "" {
			current.Main = len(states) == 0
			states = append(states, current)
		}

		current = WorktreeState{}
	}

	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			flush()
			continue
		}

		key, value, _ := strings.Cut(line, " ")

		switch key {
		case gitWorktree:
			current.Path = value
		case "branch":
			current.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "prunable":
			current.Prunable = reasonOr(value, "registration is dead")
		case "locked":
			current.Locked = reasonOr(value, "locked")
		}
	}

	flush()

	return states
}

// reasonOr keeps git's own wording when it gives one; both keys can appear bare.
func reasonOr(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

// FetchPrune refreshes remote-tracking refs and drops those the remote no
// longer has. Reports false when there is no origin to fetch from.
//
// Every [gone] verdict is only as current as the last fetch, and it is wrong in
// BOTH directions when stale: a branch deleted on the remote an hour ago does
// not read [gone] yet, and a remote-tracking ref for a branch someone recreated
// still reads [gone] until a prune. So the sweep fetches before it classifies,
// and --offline is the caller saying it knows the answers are as old as the
// clone.
func (s *Sweeper) FetchPrune() (bool, error) {
	// No origin is a fact to report, not a failure: a local-only repo still
	// classifies, it just classifies against stale knowledge of nothing.
	if _, err := s.git.run("remote", "get-url", "origin"); err != nil {
		return false, nil //nolint:nilerr // absence of a remote is the answer, not an error
	}

	if _, err := s.git.run("fetch", "origin", "--prune"); err != nil {
		return false, fmt.Errorf("git sweep: fetch --prune: %w", err)
	}

	return true, nil
}

// StashState is one entry of the stash stack.
//
// The stack is REPO-WIDE — every worktree of the same clone shares
// .git/refs/stash — so this lists other sessions' work as readily as your own.
// That is why sweep only ever reports stashes: `git stash drop` here would drop
// a sibling session's entry, and the stack renumbers under concurrent use.
type StashState struct {
	Ref     string `json:"ref"`
	Subject string `json:"subject"`
	AgeDays int    `json:"age_days"`
	// SessionAuto marks a `claude-session-start:*` auto-save. Those are
	// disposable by doctrine, and they are the bulk of a stale stack.
	SessionAuto bool `json:"session_auto"`
}

// Stashes lists the stack oldest-last, as git reports it.
func (s *Sweeper) Stashes(now time.Time) ([]StashState, error) {
	// %x00 is the log-format spelling of the same escape; see scanBranches.
	out, err := s.git.run("stash", "list", "--format=%gd%x00%gs%x00%ct")
	if err != nil || out == "" {
		return nil, err
	}

	var stashes []StashState

	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(line, nulSep)

		const wantFields = 3
		if len(fields) != wantFields {
			continue
		}

		seconds, convErr := strconv.ParseInt(fields[2], 10, 64)
		if convErr != nil {
			continue
		}

		stashes = append(stashes, StashState{
			Ref:         fields[0],
			Subject:     fields[1],
			AgeDays:     int(now.Sub(time.Unix(seconds, 0)).Hours() / hoursPerDay),
			SessionAuto: strings.Contains(fields[1], "claude-session-start:"),
		})
	}

	return stashes, nil
}

const hoursPerDay = 24

// --- the acting half; everything above only reads.

// ErrNotDeletable guards Delete against a caller that skipped the classifier.
var ErrNotDeletable = errors.New("git sweep: branch is not classified deletable")

// Delete removes a branch and returns the tip it removed.
//
// `git branch -d` is tried first and is the whole story for a branch the base
// actually contains. It REFUSES a squash-merged branch, though — git looks for
// the branch's commits in the base and a squash does not put them there — so
// every branch this org merges would survive a -d-only sweep, and the verb
// would report cleanup it never performed.
//
// So -D is the fallback, and it is reached only where git's own check is
// structurally unable to see a merge that did happen: proof tree-on-base or
// merged-pr, both of which are positive evidence the content is on the base.
// An unproven branch never gets here — Deletable() is re-checked on the way in,
// not assumed from the caller.
//
// The returned tip makes this reversible: `git branch <name> <tip>` restores
// it, and the reflog holds the object for the usual 90 days.
func (s *Sweeper) Delete(b *BranchState) (string, error) {
	if !b.Deletable() {
		return "", fmt.Errorf("%w: %s (%s)", ErrNotDeletable, b.Name, b.Why())
	}

	tip, err := s.git.run("rev-parse", "--short", b.Name)
	if err != nil {
		return "", err
	}

	if _, err := s.git.run("branch", "-d", b.Name); err == nil {
		return tip, nil
	}

	if b.Proof != ProofSameTree && b.Proof != ProofMergedPR {
		return "", fmt.Errorf("git sweep: %s: git refused -d and proof %q does not justify -D", b.Name, b.Proof)
	}

	if _, err := s.git.run("branch", "-D", b.Name); err != nil {
		return "", err
	}

	return tip, nil
}

// PruneWorktrees clears registrations whose path is gone. It removes no
// directory and touches no live worktree — `git worktree prune` only drops
// administrative entries git has already marked prunable.
//
// Removing a live worktree is deliberately NOT part of --execute. #321 lists it
// under the safe set, but the fleet runs 15+ concurrent worktrees and a clean
// tree on a merged branch is indistinguishable from a sibling session's idle
// checkout. Those are reported instead.
//
// --expire=now overrides gc.worktreePruneExpire, which defaults to three
// months. Without it the report and the action disagree: `worktree list`
// annotates an entry prunable the moment its path disappears, so sweep would
// name a dead registration and then decline to clear it — the exact shape of a
// verb that promises what it cannot deliver. The grace period exists for a
// worktree on an unmounted volume; the cost of overriding it is an
// administrative pointer that `git worktree add` recreates, with the working
// tree's files untouched either way.
// The returned list is derived by RE-LISTING, not from `git worktree prune -v`,
// whose verbose line does not come back on stdout — so reporting it would have
// meant reporting an empty result for work that succeeded, or worse, a
// non-empty one for work that did not.
func (s *Sweeper) PruneWorktrees() ([]string, error) {
	before, err := s.listWorktrees()
	if err != nil {
		return nil, err
	}

	if _, err := s.git.run(gitWorktree, "prune", "--expire=now"); err != nil {
		return nil, fmt.Errorf("git sweep: pruning worktrees: %w", err)
	}

	after, err := s.listWorktrees()
	if err != nil {
		return nil, err
	}

	remaining := make(map[string]bool, len(after))
	for _, w := range after {
		remaining[w.Path] = true
	}

	var pruned []string

	for _, w := range before {
		if w.Prunable != "" && !remaining[w.Path] {
			pruned = append(pruned, w.Path)
		}
	}

	return pruned, nil
}
