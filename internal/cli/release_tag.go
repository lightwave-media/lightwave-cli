package cli

// release_tag.go — `lw release tag`, the front door of the tag-driven release
// plane (lightwave-core policy/governance/release_pipeline.yaml).
//
// The plane is triggered by a tag and nothing else: the tag IS the version, so
// there are no bump commits and no committed CHANGELOG.md. This command is the
// ergonomic way to produce that tag — it computes the next SemVer from the
// conventional commits since the last matching tag, then creates and pushes an
// annotated tag. `git tag vX.Y.Z && git push origin vX.Y.Z` remains the
// byte-identical manual path.
//
// Version arithmetic lives in internal/release (pure, unit-tested). This file
// is the git I/O and the operator interaction around it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/release"
)

func init() {
	RegisterHandler("release.tag", releaseTagHandler)
}

// commitRecordSep separates commits in the `git log` output. Subject and body
// need a separator that cannot occur in either, so use ASCII unit/record
// separators rather than hoping a text sentinel is unique.
const (
	commitFieldSep  = "\x1f"
	commitRecordSep = "\x1e"
)

func releaseTagHandler(ctx context.Context, _ []string, flags map[string]any) error {
	repo, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	module := flagStr(flags, "module")
	prefix := release.TagPrefix(module)

	plan, err := resolveNextTagVersion(ctx, repo, prefix, flagStr(flags, "version"))
	if err != nil {
		return err
	}

	tag := prefix + plan.Next.String()

	if exists, err := tagExists(ctx, repo, tag); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("tag %s already exists — pass --version to pick another", tag)
	}

	printTagPlan(tag, plan.LastTag, plan.Next, plan.Bump, module)
	printClassification(plan.Summary, tagAge(ctx, repo, plan.LastTag))

	// Checked before the dry-run exit on purpose: a dry run whose only job is to
	// tell you what a real run would do must report the thing that would stop it.
	guardErr := verifyHeadIsOriginMain(ctx, repo)

	if flagBool(flags, "dry-run") {
		if guardErr != nil {
			fmt.Printf("%s %v\n", color.RedString("would refuse:"), guardErr)
		}

		fmt.Println(color.YellowString("dry-run: no tag created, nothing pushed"))

		return nil
	}

	if guardErr != nil {
		return guardErr
	}

	if !flagBool(flags, "yes") && !promptYesNo(fmt.Sprintf("Create and push %s?", tag)) {
		fmt.Println("aborted")
		return nil
	}

	return createAndPushTag(ctx, repo, tag, plan.Next)
}

// verifyHeadIsOriginMain refuses to tag anything that is not exactly
// origin/main's tip.
//
// The plane enforces tag-on-main server-side (release-core.yml), so without this
// the local command happily creates and pushes a tag that the pipeline then
// rejects — leaving a published tag behind with no release, which someone has to
// delete by hand. Failing here costs a second; failing there costs a cleanup.
//
// It also catches the quieter mistake: tagging a stale local main that is behind
// origin, which would release older code under a newer version.
func verifyHeadIsOriginMain(ctx context.Context, repo string) error {
	head, err := gitOutput(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("git rev-parse HEAD: %w", err)
	}

	// Ask the remote rather than trusting the local ref, which may be stale.
	lsRemote, err := gitOutput(ctx, repo, "ls-remote", "origin", "refs/heads/main")
	if err != nil {
		return fmt.Errorf("git ls-remote origin refs/heads/main: %w", err)
	}

	originMain, _, _ := strings.Cut(strings.TrimSpace(lsRemote), "\t")
	if originMain == "" {
		return errors.New("could not resolve origin/main — is the remote reachable?")
	}

	if head != originMain {
		return fmt.Errorf(
			"HEAD (%s) is not origin/main (%s) — the release plane only accepts tags on main; "+
				"push or fast-forward first",
			shortSHA(head), shortSHA(originMain),
		)
	}

	return nil
}

func shortSHA(sha string) string {
	const n = 7
	if len(sha) <= n {
		return sha
	}

	return sha[:n]
}

// resolveNextTagVersion returns the version to tag, the bump that produced it,
// and the last tag it was computed from ("" when this is the first release).
// An explicit --version short-circuits the commit scan entirely.
// tagPlan is everything the operator interaction needs about the release being
// proposed. A struct rather than a fifth return value: the classification
// (#382) pushed this past the point where positional results stayed readable.
type tagPlan struct {
	LastTag string
	Summary release.Summary
	Next    release.Version
	Bump    release.Bump
}

func resolveNextTagVersion(
	ctx context.Context, repo, prefix, override string,
) (tagPlan, error) {
	lastTag, err := lastMatchingTag(ctx, repo, prefix)
	if err != nil {
		return tagPlan{}, err
	}

	if override != "" {
		v, perr := release.ParseVersion(override)
		if perr != nil {
			return tagPlan{}, fmt.Errorf("--version: %w", perr)
		}

		return tagPlan{Next: v, Bump: release.BumpNone, LastTag: lastTag}, nil
	}

	// No prior tag: this is the first release of this artifact. Seed at 0.1.0
	// rather than computing a bump from the whole history, which would be
	// arbitrary — the operator can override with --version.
	if lastTag == "" {
		return tagPlan{Next: release.Version{Minor: 1}, Bump: release.BumpNone}, nil
	}

	lastVersion, err := release.ParseVersion(lastTag)
	if err != nil {
		return tagPlan{}, fmt.Errorf("last tag %s: %w", lastTag, err)
	}

	commits, err := commitsSince(ctx, repo, lastTag)
	if err != nil {
		return tagPlan{}, err
	}

	next, bump, err := release.NextVersion(lastVersion, commits)
	if err != nil {
		if errors.Is(err, release.ErrNoCommits) {
			return tagPlan{}, fmt.Errorf("%w (last tag %s)", err, lastTag)
		}

		return tagPlan{}, err
	}

	return tagPlan{
		Next:    next,
		Bump:    bump,
		LastTag: lastTag,
		Summary: release.Summarize(commits),
	}, nil
}

// lastMatchingTag returns the highest tag matching the prefix, or "" when the
// artifact has never been released. Sorted by version, not by date, so an
// out-of-order tag push cannot rewrite history's idea of "latest".
func lastMatchingTag(ctx context.Context, repo, prefix string) (string, error) {
	out, err := gitOutput(ctx, repo, "tag", "--list", prefix+"*", "--sort=-v:refname")
	if err != nil {
		return "", fmt.Errorf("git tag --list: %w", err)
	}

	for line := range strings.SplitSeq(out, "\n") {
		if tag := strings.TrimSpace(line); tag != "" {
			return tag, nil
		}
	}

	return "", nil
}

func commitsSince(ctx context.Context, repo, lastTag string) ([]release.Commit, error) {
	out, err := gitOutput(ctx, repo, "log",
		lastTag+"..HEAD",
		"--no-merges",
		"--format=%s"+commitFieldSep+"%b"+commitRecordSep,
	)
	if err != nil {
		return nil, fmt.Errorf("git log %s..HEAD: %w", lastTag, err)
	}

	records := strings.Split(out, commitRecordSep)
	commits := make([]release.Commit, 0, len(records))

	for _, rec := range records {
		record := strings.TrimSpace(rec)
		if record == "" {
			continue
		}

		subject, body, _ := strings.Cut(record, commitFieldSep)
		commits = append(commits, release.Commit{
			Subject: strings.TrimSpace(subject),
			Body:    body,
		})
	}

	return commits, nil
}

func tagExists(ctx context.Context, repo, tag string) (bool, error) {
	out, err := gitOutput(ctx, repo, "tag", "--list", tag)
	if err != nil {
		return false, fmt.Errorf("git tag --list %s: %w", tag, err)
	}

	return strings.TrimSpace(out) != "", nil
}

func printTagPlan(tag, lastTag string, next release.Version, bump release.Bump, module string) {
	from := lastTag
	if from == "" {
		from = "(no prior tag)"
	}

	fmt.Printf("%s %s\n", color.CyanString("release tag:"), tag)
	fmt.Printf("  from:    %s\n", from)
	fmt.Printf("  version: %s\n", next.String())

	switch {
	case lastTag == "":
		fmt.Printf("  bump:    %s\n", color.YellowString("first release (seeded; --version overrides)"))
	case bump == release.BumpNone:
		fmt.Printf("  bump:    %s\n", color.YellowString("explicit (--version)"))
	default:
		fmt.Printf("  bump:    %s (from conventional commits)\n", bump)
	}

	if module != "" {
		fmt.Printf("  module:  %s\n", module)
	}
}

// printClassification shows the reasoning behind the computed version before it
// is published, not after (#382).
//
// v3.13.0 shipped 30 commits that had sat on main for four weeks, and cutting it
// meant reading 30 subjects by hand to classify them. The range also held a
// `ci(release)!` commit whose BREAKING CHANGE was "no more nightly Release PRs"
// — contributor-facing, no `lw` command changed — which conventional-commit
// arithmetic computes as a major. Publishing that says "your usage broke" to
// every consumer when nothing of theirs did.
//
// So: print the histogram, list the breaking markers with their scope, and warn
// when a major rests only on contributor-facing commits. The judgment stays with
// the human; this only makes the delta visible in time to apply it.
func printClassification(s release.Summary, staleness string) {
	if s.Total == 0 {
		return
	}

	fmt.Printf("\n%s %d commit(s)\n", color.CyanString("since last tag:"), s.Total)

	if staleness != "" {
		fmt.Printf("  age:     %s\n", staleness)
	}

	for _, t := range sortedTypes(s.ByType) {
		fmt.Printf("  %-20s %d\n", t, s.ByType[t])
	}

	if len(s.Breaking) == 0 {
		return
	}

	fmt.Printf("\n%s (%d)\n", color.YellowString("breaking markers"), len(s.Breaking))

	for _, b := range s.Breaking {
		facing := color.RedString("consumer-facing")
		if b.ContributorFacing {
			facing = color.YellowString("contributor-facing")
		}

		fmt.Printf("  ! %-8s %s\n      %s\n", b.Type, facing, b.Subject)
	}

	if s.AllBreakingAreContributorFacing() {
		fmt.Printf("\n%s\n",
			color.YellowString("every breaking marker above is contributor-facing."))
		fmt.Println("  A major says \"your usage broke\" to everyone who installed this.")
		fmt.Println("  If no command, flag or output changed, pass --version to cut a minor instead.")
	}
}

// sortedTypes orders the histogram deterministically so two runs of the same
// range print the same report.
func sortedTypes(byType map[string]int) []string {
	out := make([]string, 0, len(byType))
	for t := range byType {
		out = append(out, t)
	}

	sort.Strings(out)

	return out
}

// tagAge reports how far main has drifted from the last tag, in days.
//
// The staleness half of #382: release.yml fires on a pushed tag, no workflow
// creates one, and `lw` neither self-updates nor says it is behind — so a merged
// PR is not a shipped feature and nothing anywhere says so. Four weeks of main
// went unreleased that way. Empty string when the age cannot be read; a missing
// signal must not fail a release.
func tagAge(ctx context.Context, repo, lastTag string) string {
	if lastTag == "" {
		return ""
	}

	out, err := gitOutput(ctx, repo, "log", "-1", "--format=%cI", lastTag)
	if err != nil || out == "" {
		return ""
	}

	tagged, err := time.Parse(time.RFC3339, strings.TrimSpace(out))
	if err != nil {
		return ""
	}

	days := int(time.Since(tagged).Hours() / hoursPerDay)
	if days < 1 {
		return "tagged today"
	}

	age := fmt.Sprintf("%s was %d day(s) ago", lastTag, days)
	if days >= staleTagDays {
		return color.YellowString("%s — main has been unreleased that long", age)
	}

	return age
}

// staleTagDays is when an unreleased main is worth flagging. Set from the
// incident: v3.12.0 → v3.13.0 was four weeks, and nothing reported it.
// hoursPerDay is declared in worktree.go, in this same package.
const staleTagDays = 14

func createAndPushTag(ctx context.Context, repo, tag string, next release.Version) error {
	msg := "Release " + next.String()

	if err := gitRun(ctx, repo, "tag", "-a", tag, "-m", msg); err != nil {
		return fmt.Errorf("git tag -a %s: %w", tag, err)
	}

	fmt.Printf("%s created annotated tag %s\n", color.GreenString("✓"), tag)

	if err := gitRun(ctx, repo, "push", "origin", tag); err != nil {
		return fmt.Errorf("git push origin %s (tag exists locally; delete it or push manually): %w", tag, err)
	}

	fmt.Printf("%s pushed %s — the release pipeline runs on the tag\n", color.GreenString("✓"), tag)

	return nil
}

func gitRun(ctx context.Context, dir string, args ...string) error {
	c := exec.CommandContext(ctx, "git", args...)
	c.Dir = dir
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	return c.Run()
}
