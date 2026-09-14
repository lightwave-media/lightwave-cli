package cli

// git_sweep.go — `lw git sweep`, the custodian verb (#321).
//
// Report-only by default. --execute acts on the verified-safe subset and
// nothing else: a branch whose upstream is [gone] AND whose merge is proven AND
// which no worktree holds. Everything else is REPORTED — unrouted work is the
// thing this verb exists to protect, not to tidy away.
//
// Classification lives in internal/git/sweep.go, with the measurements that
// forced its shape. The short version: under squash-merge the clone cannot
// answer "was this merged", so the forge is asked, and --offline says out loud
// that it was not.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/git"
)

func init() {
	RegisterHandler("git.sweep", gitSweepHandler)
}

// forgeState is what the report says about the third proof.
const (
	forgeQueried     = "queried"
	forgeOffline     = "offline"
	forgeUnreachable = "unreachable"
)

// sweepReport is the whole answer, and the --json shape.
type sweepReport struct {
	Repo      string              `json:"repo"`
	Base      string              `json:"base"`
	Forge     string              `json:"forge"`
	Branches  []git.BranchState   `json:"branches"`
	Worktrees []git.WorktreeState `json:"worktrees"`
	Stashes   []git.StashState    `json:"stashes"`
	Executed  []string            `json:"executed,omitempty"`
	Fetched   bool                `json:"fetched"`
}

// forgeLookup wraps the `gh` query so one failure degrades the whole sweep
// loudly instead of aborting it.
//
// gh fails for whole-session reasons — not authenticated, no remote, rate
// limited — so the first failure is the last useful attempt. Rather than error
// out of a read-only report, the lookup goes quiet and the header says
// "unreachable": branches only the forge could clear then read as unproven,
// which is exactly what they are from where the sweep is standing.
type forgeLookup struct {
	ctx  context.Context
	dir  string
	down bool
}

func (f *forgeLookup) lookup(branch string) (int, bool, error) {
	if f.down {
		return 0, false, nil
	}

	cmd := exec.CommandContext(f.ctx, "gh", "pr", "list",
		"--head", branch, "--state", "merged", "--limit", "1", "--json", "number")
	cmd.Dir = f.dir

	out, err := cmd.Output()
	if err != nil {
		f.down = true

		return 0, false, nil //nolint:nilerr // a dead forge degrades the report, it does not fail it — see the type comment
	}

	var prs []struct {
		Number int `json:"number"`
	}

	if err := json.Unmarshal(out, &prs); err != nil {
		f.down = true

		return 0, false, nil //nolint:nilerr // same: unreadable is unreachable, and the header says so
	}

	if len(prs) == 0 {
		return 0, false, nil
	}

	return prs[0].Number, true, nil
}

func gitSweepHandler(ctx context.Context, _ []string, flags map[string]any) error {
	dir := flagString(flags, "repo")
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		dir = cwd
	}

	repo := git.NewGit(dir)
	if !repo.IsRepo() {
		return fmt.Errorf("lw git sweep: not a git repository: %s", dir)
	}

	offline := flagBool(flags, "offline")

	var forge *forgeLookup

	lookup := git.MergedPRLookup(nil)

	if !offline {
		forge = &forgeLookup{ctx: ctx, dir: dir}
		lookup = forge.lookup
	}

	sweeper, err := git.NewSweeper(repo, lookup)
	if err != nil {
		return err
	}

	report := sweepReport{Repo: dir, Base: sweeper.Base(), Forge: forgeOffline}

	if !offline {
		fetched, fetchErr := sweeper.FetchPrune()
		if fetchErr != nil {
			return fetchErr
		}

		report.Fetched = fetched
		report.Forge = forgeQueried
	}

	if err := collectSweep(sweeper, &report); err != nil {
		return err
	}

	if forge != nil && forge.down {
		report.Forge = forgeUnreachable
	}

	if flagBool(flags, "execute") {
		if err := executeSweep(sweeper, &report, flagBool(flags, "yes")); err != nil {
			return err
		}
	}

	return writeSweepReport(&report, asJSON(flags))
}

func collectSweep(s *git.Sweeper, report *sweepReport) error {
	branches, err := s.Branches()
	if err != nil {
		return err
	}

	worktrees, err := s.Worktrees()
	if err != nil {
		return err
	}

	stashes, err := s.Stashes(time.Now())
	if err != nil {
		return err
	}

	report.Branches, report.Worktrees, report.Stashes = branches, worktrees, stashes

	return nil
}

// executeSweep performs the safe subset, after confirmation.
//
// It re-derives the candidates from the classification rather than trusting a
// caller-supplied list, so there is exactly one definition of "safe" and it is
// the one the report printed.
func executeSweep(s *git.Sweeper, report *sweepReport, assumeYes bool) error {
	var candidates []git.BranchState

	for i := range report.Branches {
		if report.Branches[i].Deletable() {
			candidates = append(candidates, report.Branches[i])
		}
	}

	dead := 0

	for _, w := range report.Worktrees {
		if w.Prunable != "" {
			dead++
		}
	}

	if len(candidates) == 0 && dead == 0 {
		report.Executed = []string{"nothing to sweep"}

		return nil
	}

	if !assumeYes && !promptYesNo(fmt.Sprintf(
		"Delete %d branch(es) and prune %d dead worktree registration(s)?", len(candidates), dead)) {
		report.Executed = []string{"cancelled — nothing was deleted"}

		return nil
	}

	for i := range candidates {
		b := &candidates[i]

		tip, err := s.Delete(b)
		if err != nil {
			return err
		}

		// The tip makes this reversible; print it where it will be read.
		report.Executed = append(report.Executed,
			fmt.Sprintf("deleted %s (%s) — restore: git branch %s %s", b.Name, tip, b.Name, tip))
	}

	pruned, err := s.PruneWorktrees()
	if err != nil {
		return err
	}

	for _, line := range pruned {
		report.Executed = append(report.Executed, "pruned "+line)
	}

	return nil
}

// bucket names the single group a branch belongs to in the report. The order
// here is the order of the printed sections, most actionable first.
const (
	bucketSweep    = "sweep"
	bucketUnrouted = "unrouted"
	bucketHeld     = "held"
	bucketLanded   = "landed"
	bucketActive   = "active"
)

var sweepBuckets = []struct {
	name string
	note string
}{
	{bucketSweep, "upstream gone, merge proven, free — --execute deletes these"},
	{bucketUnrouted, "no remote and no proof — open a PR or delete deliberately"},
	{bucketHeld, "checked out in a worktree; nothing to do from here"},
	{bucketLanded, "merged, but not free to sweep"},
	{bucketActive, "upstream still on the remote"},
}

func bucketOf(b *git.BranchState) string {
	switch {
	case b.Deletable():
		return bucketSweep
	case b.Base || b.Current:
		return bucketActive
	case b.HeldBy != "":
		return bucketHeld
	case b.Proof != git.ProofNone:
		return bucketLanded
	case b.Upstream == "" || b.Gone:
		return bucketUnrouted
	default:
		return bucketActive
	}
}

// branchNote is the right-hand column: the receipt for a proven branch, the
// reason for one that stays.
func branchNote(b *git.BranchState) string {
	if b.Proof != git.ProofNone {
		note := string(b.Proof) + " " + b.Evidence
		if why := b.Why(); why != "" {
			note += " · " + why
		}

		return note
	}

	note := b.Why()
	if b.Unique > 0 {
		note += fmt.Sprintf(" · %d unique commit(s)", b.Unique)
	}

	return note
}

func writeSweepReport(report *sweepReport, wantJSON bool) error {
	if wantJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(report)
	}

	var out strings.Builder

	fmt.Fprintf(&out, "lw git sweep — %s\n", report.Repo)
	fmt.Fprintf(&out, "base %s · fetch %s · forge %s\n\n",
		report.Base, yesNo(report.Fetched, "pruned", "skipped"), report.Forge)

	writeSweepBranches(&out, report.Branches)
	writeSweepWorktrees(&out, report.Worktrees)
	writeSweepStashes(&out, report.Stashes)

	if len(report.Executed) > 0 {
		out.WriteString("\nexecuted\n")

		for _, line := range report.Executed {
			fmt.Fprintf(&out, "  %s\n", line)
		}
	}

	_, err := os.Stdout.WriteString(out.String())

	return err
}

func writeSweepBranches(out *strings.Builder, branches []git.BranchState) {
	grouped := map[string][]git.BranchState{}

	for i := range branches {
		name := bucketOf(&branches[i])
		grouped[name] = append(grouped[name], branches[i])
	}

	fmt.Fprintf(out, "branches — %d local\n", len(branches))

	for _, bucket := range sweepBuckets {
		rows := grouped[bucket.name]
		if len(rows) == 0 {
			continue
		}

		fmt.Fprintf(out, "\n  %s (%d) — %s\n", bucket.name, len(rows), bucket.note)

		// `active` decides nothing, so it prints as names rather than rows.
		if bucket.name == bucketActive {
			names := make([]string, 0, len(rows))
			for _, b := range rows {
				names = append(names, b.Name)
			}

			fmt.Fprintf(out, "    %s\n", strings.Join(names, ", "))

			continue
		}

		for i := range rows {
			fmt.Fprintf(out, "    %-44s %s\n", rows[i].Name, branchNote(&rows[i]))
		}
	}
}

func writeSweepWorktrees(out *strings.Builder, worktrees []git.WorktreeState) {
	dirty, locked := 0, 0

	fmt.Fprintf(out, "\nworktrees — %d registered\n", len(worktrees))

	for _, w := range worktrees {
		switch {
		case w.Prunable != "":
			fmt.Fprintf(out, "    prune  %-44s %s\n", w.Path, w.Prunable)
		case w.Locked != "":
			locked++
		case w.Dirty:
			dirty++
		}
	}

	if dirty > 0 || locked > 0 {
		fmt.Fprintf(out, "    %d dirty, %d locked — left alone; a sibling session may hold either\n", dirty, locked)
	}
}

func writeSweepStashes(out *strings.Builder, stashes []git.StashState) {
	if len(stashes) == 0 {
		return
	}

	// The stack is repo-wide, so this reports other sessions' entries too and
	// sweep never touches any of them.
	const staleDays = 2

	fmt.Fprintf(out, "\nstashes — %d on the shared stack (report only)\n", len(stashes))

	for _, s := range stashes {
		if s.AgeDays < staleDays {
			continue
		}

		tag := ""
		if s.SessionAuto {
			tag = " · session auto-save, droppable by doctrine"
		}

		fmt.Fprintf(out, "    %-10s %2dd  %s%s\n", s.Ref, s.AgeDays, s.Subject, tag)
	}
}

func yesNo(cond bool, yes, no string) string {
	if cond {
		return yes
	}

	return no
}
