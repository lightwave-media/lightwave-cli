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
	"strings"

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

	next, bump, last, err := resolveNextTagVersion(ctx, repo, prefix, flagStr(flags, "version"))
	if err != nil {
		return err
	}

	tag := prefix + next.String()

	if exists, err := tagExists(ctx, repo, tag); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("tag %s already exists — pass --version to pick another", tag)
	}

	printTagPlan(tag, last, next, bump, module)

	if flagBool(flags, "dry-run") {
		fmt.Println(color.YellowString("dry-run: no tag created, nothing pushed"))
		return nil
	}

	if !flagBool(flags, "yes") && !promptYesNo(fmt.Sprintf("Create and push %s?", tag)) {
		fmt.Println("aborted")
		return nil
	}

	return createAndPushTag(ctx, repo, tag, next)
}

// resolveNextTagVersion returns the version to tag, the bump that produced it,
// and the last tag it was computed from ("" when this is the first release).
// An explicit --version short-circuits the commit scan entirely.
func resolveNextTagVersion(
	ctx context.Context, repo, prefix, override string,
) (next release.Version, bump release.Bump, lastTag string, err error) {
	lastTag, err = lastMatchingTag(ctx, repo, prefix)
	if err != nil {
		return release.Version{}, release.BumpNone, "", err
	}

	if override != "" {
		v, perr := release.ParseVersion(override)
		if perr != nil {
			return release.Version{}, release.BumpNone, "", fmt.Errorf("--version: %w", perr)
		}

		return v, release.BumpNone, lastTag, nil
	}

	// No prior tag: this is the first release of this artifact. Seed at 0.1.0
	// rather than computing a bump from the whole history, which would be
	// arbitrary — the operator can override with --version.
	if lastTag == "" {
		return release.Version{Minor: 1}, release.BumpNone, "", nil
	}

	lastVersion, err := release.ParseVersion(lastTag)
	if err != nil {
		return release.Version{}, release.BumpNone, "", fmt.Errorf("last tag %s: %w", lastTag, err)
	}

	commits, err := commitsSince(ctx, repo, lastTag)
	if err != nil {
		return release.Version{}, release.BumpNone, "", err
	}

	next, bump, err = release.NextVersion(lastVersion, commits)
	if err != nil {
		if errors.Is(err, release.ErrNoCommits) {
			return release.Version{}, release.BumpNone, "", fmt.Errorf("%w (last tag %s)", err, lastTag)
		}

		return release.Version{}, release.BumpNone, "", err
	}

	return next, bump, lastTag, nil
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
