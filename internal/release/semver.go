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

	parsed, ok := parseConventional(subject)
	if !ok {
		return BumpPatch
	}

	if parsed.Breaking {
		return BumpMajor
	}

	if strings.EqualFold(parsed.Type, "feat") {
		return BumpMinor
	}

	return BumpPatch
}

// conventionalParts is the decomposed subject line.
type conventionalParts struct {
	Type     string
	Scope    string
	Breaking bool
}

// parseConventional splits a Conventional Commits subject. Extracted so the
// bump decision and the classification report read the same parse instead of
// each running the regex with its own interpretation.
func parseConventional(subject string) (conventionalParts, bool) {
	m := conventionalRe.FindStringSubmatch(subject)
	if m == nil {
		return conventionalParts{}, false
	}

	return conventionalParts{
		Type:     strings.ToLower(m[1]),
		Scope:    strings.Trim(m[2], "()"),
		Breaking: m[3] == "!",
	}, true
}

// contributorFacingTypes describe the contributor workflow rather than the
// released artifact's contract. A `!` on one of these is still a real break —
// of how people contribute — but it is not a break of any `lw` command, so it
// should not silently publish a major that tells every user their usage broke.
//
// Deliberately conservative. `build` is absent because it can change the shipped
// artifact, and `refactor`/`perf`/`revert` are absent because they can change
// observable behaviour. When in doubt a type stays consumer-facing, because the
// cost of a needless major is an upgrade note while the cost of a missed one is
// a silent break.
var contributorFacingTypes = map[string]bool{
	"ci":    true,
	"chore": true,
	"docs":  true,
	"test":  true,
	"style": true,
}

// BreakingMarker is one commit in the range that demands a major bump.
type BreakingMarker struct {
	Subject string
	Type    string
	Scope   string

	// ContributorFacing is true when the commit's type describes the
	// contributor workflow rather than the artifact's contract.
	ContributorFacing bool
}

// Summary is the classification that produced a bump — what a human needs to
// see BEFORE publishing, not after.
type Summary struct {
	ByType   map[string]int
	Breaking []BreakingMarker
	Total    int
}

// AllBreakingAreContributorFacing reports whether a computed major rests
// entirely on contributor-facing commits.
//
// This is the v3.13.0 case (#382): a `ci(release)!` commit whose break was "no
// more nightly Release PRs" would compute v4.0.0 and announce to every consumer
// that their usage broke. It is a warning, never an automatic downgrade — a
// `ci!` commit CAN carry a real consumer break, and silently reclassifying it
// would trade a loud wrong answer for a quiet one.
func (s Summary) AllBreakingAreContributorFacing() bool {
	if len(s.Breaking) == 0 {
		return false
	}

	for _, b := range s.Breaking {
		if !b.ContributorFacing {
			return false
		}
	}

	return true
}

// Summarize classifies a commit range: counts by type, and every breaking
// marker with the scope that carried it.
func Summarize(commits []Commit) Summary {
	out := Summary{ByType: make(map[string]int, len(commits)), Total: len(commits)}

	for _, c := range commits {
		parsed, ok := parseConventional(c.Subject)

		typ := parsed.Type
		if !ok {
			typ = "(non-conventional)"
		}

		out.ByType[typ]++

		if !ok {
			continue
		}

		if !parsed.Breaking && !breakingTrailerRe.MatchString(c.Body) {
			continue
		}

		out.Breaking = append(out.Breaking, BreakingMarker{
			Subject:           c.Subject,
			Type:              parsed.Type,
			Scope:             parsed.Scope,
			ContributorFacing: contributorFacingTypes[parsed.Type],
		})
	}

	return out
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
