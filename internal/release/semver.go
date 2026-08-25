package release

// semver.go — next-version computation for `lw release tag`.
//
// The release plane (lightwave-core policy/governance/release_pipeline.yaml) is
// tag-driven: a tag IS the version, and pushing one is the whole release
// trigger. There are no version-bump commits and no committed CHANGELOG.md, so
// the only question this file answers is: given the last tag and the commits
// since it, what should the next tag be?
//
// Everything here is pure — no git, no network — so the rules are testable in
// isolation. Git I/O lives in the handler.

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Bump is the size of a version increment. Ordered so the largest bump among a
// set of commits wins by simple comparison.
type Bump int

const (
	BumpNone Bump = iota
	BumpPatch
	BumpMinor
	BumpMajor
)

func (b Bump) String() string {
	switch b {
	case BumpMajor:
		return "major"
	case BumpMinor:
		return "minor"
	case BumpPatch:
		return "patch"
	case BumpNone:
		return "none"
	default:
		return "unknown"
	}
}

// Version is a parsed SemVer core. Pre-release and build metadata are not
// modelled: the plane's tag grammar is plain `v<major>.<minor>.<patch>`.
type Version struct {
	Major int
	Minor int
	Patch int
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Next applies a bump, zeroing the lower components as SemVer requires.
func (v Version) Next(b Bump) Version {
	switch b {
	case BumpMajor:
		return Version{Major: v.Major + 1}
	case BumpMinor:
		return Version{Major: v.Major, Minor: v.Minor + 1}
	case BumpPatch:
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
	case BumpNone:
		return v
	default:
		return v
	}
}

var semverRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// ParseVersion accepts a bare or tag-shaped version: "1.2.3", "v1.2.3", or a
// monorepo tag "nulltickets/v1.2.3". Returns an error rather than guessing.
func ParseVersion(s string) (Version, error) {
	core := strings.TrimSpace(s)
	if i := strings.LastIndex(core, "/"); i >= 0 {
		core = core[i+1:]
	}

	core = strings.TrimPrefix(core, "v")

	m := semverRe.FindStringSubmatch(core)
	if m == nil {
		return Version{}, fmt.Errorf("not a SemVer version: %q (want v<major>.<minor>.<patch>)", s)
	}

	// Each group is \d+, so Atoi cannot fail here.
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])

	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

// conventionalRe matches a Conventional Commits subject: type, optional scope,
// optional `!` breaking marker, then `: `. Captures the marker so a breaking
// change is detected from the subject alone.
var conventionalRe = regexp.MustCompile(`^([a-zA-Z]+)(\([^)]*\))?(!)?:\s`)

// breakingTrailerRe matches the footer form of a breaking change. The spec
// allows both `BREAKING CHANGE:` and `BREAKING-CHANGE:`.
var breakingTrailerRe = regexp.MustCompile(`(?m)^BREAKING[ -]CHANGE:`)

// ClassifyCommit maps one commit to the bump it demands.
//
// Rules, per Conventional Commits:
//   - `!` after the type/scope, or a BREAKING CHANGE footer → major
//   - `feat` → minor
//   - anything else, including a non-conventional subject → patch
//
// The last rule is a deliberate choice, not a fallback: `lw release tag` is
// only ever run to cut a release, so every commit in range is shipping. Mapping
// unknown types to "no bump" would let a release compute the version that is
// already tagged. Non-conventional subjects are counted as patch for the same
// reason — a repo with sloppy history still gets a monotonic version.
//
// Note: 0.x is NOT special-cased. Standard SemVer bumping applies at every
// major, so a breaking change on 0.x goes to 1.0.0. Repos that want the
// "0.x breaking → minor" convention should pass --version explicitly.
func ClassifyCommit(subject, body string) Bump {
	if breakingTrailerRe.MatchString(body) {
		return BumpMajor
	}

	m := conventionalRe.FindStringSubmatch(subject)
	if m == nil {
		return BumpPatch
	}

	if m[3] == "!" {
		return BumpMajor
	}

	if strings.EqualFold(m[1], "feat") {
		return BumpMinor
	}

	return BumpPatch
}

// Commit is the minimum a bump decision needs.
type Commit struct {
	Subject string
	Body    string
}

// ErrNoCommits signals an empty range — there is nothing to release, which is
// an error rather than a no-op so a scripted release fails loudly instead of
// re-tagging the current version.
var ErrNoCommits = errors.New("no commits since the last tag — nothing to release")

// NextVersion computes the version following last, given the commits since it.
// Returns the winning bump alongside so callers can explain the decision.
func NextVersion(last Version, commits []Commit) (Version, Bump, error) {
	if len(commits) == 0 {
		return Version{}, BumpNone, ErrNoCommits
	}

	bump := BumpNone
	for _, c := range commits {
		if b := ClassifyCommit(c.Subject, c.Body); b > bump {
			bump = b
		}
	}

	return last.Next(bump), bump, nil
}

// TagPrefix returns the tag prefix for a module. Empty module → "v" (the
// single-artifact grammar); otherwise "<module>/v" (the monorepo grammar).
// Both are fixed by release_pipeline.yaml's tag_grammar.
func TagPrefix(module string) string {
	if module == "" {
		return "v"
	}

	return module + "/v"
}
