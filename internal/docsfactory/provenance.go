package docsfactory

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Provenance records WHICH stamp a lint verdict was reached against.
//
// #313: `lw docs spec-lint` resolves the contract off disk and said nothing
// about what it read. When the local lightwave-core checkout was six minutes
// behind origin, the run reported
//
//	spec/probe-study.md  (technical_study)  unknown kind "technical_study"
//
// which is indistinguishable from "you used a kind that does not exist". The
// kind did exist on origin/main. The whole fix was `git pull` in lightwave-core,
// but the message gave no way to suspect that, and two turns of wrong advice
// followed — a schema release train and a CLI rebuild, neither of which had
// anything to do with it.
//
// A verdict is only as current as the contract behind it, so the contract is now
// named on every run, the way a compiler names its own version.
type Provenance struct {
	// Root is the lightwave-core checkout that was actually read. It is the
	// single most useful field: resolution falls back through LW_LIGHTWAVE_CORE,
	// a sibling layout, and ~/dev, so "which one won" is not obvious.
	Root string `json:"root"`

	// Commit is the short HEAD sha, empty when Root is not a git checkout (a
	// vendored or unpacked copy, which is legitimate and simply has no sha).
	Commit string `json:"commit,omitempty"`

	// Behind counts commits between HEAD and its upstream, or -1 when that
	// cannot be determined — no upstream, detached HEAD, not a repo.
	//
	// It is measured against the LAST FETCH, not against the remote. A checkout
	// that has not fetched reports 0 while origin has moved. Reporting it as
	// "0 behind" without that qualifier would manufacture exactly the false
	// confidence this type exists to remove, so String() says "as of last fetch".
	Behind int `json:"behind"`

	// Dirty reports uncommitted changes under the schema path. A dirty stamp
	// means the verdict reflects something no ref contains (CLAUDE.md §16).
	Dirty bool `json:"dirty"`

	// SpecKindsVersion is _meta.version of spec_artifact_kinds.yaml — the
	// contract's own declared version, which moves independently of the sha.
	SpecKindsVersion string `json:"spec_kinds_version,omitempty"`
}

// String renders the one-line provenance banner.
func (p Provenance) String() string {
	var b strings.Builder

	b.WriteString("stamp: ")
	b.WriteString(filepath.Base(p.Root))

	if p.Commit != "" {
		b.WriteString("@" + p.Commit)
	}

	if p.Dirty {
		b.WriteString("-dirty")
	}

	if p.SpecKindsVersion != "" {
		b.WriteString(", spec_artifact_kinds v" + p.SpecKindsVersion)
	}

	switch {
	case p.Behind > 0:
		fmt.Fprintf(&b, ", %d behind upstream as of last fetch", p.Behind)
	case p.Behind < 0:
		b.WriteString(", upstream unknown")
	}

	return b.String()
}

// Stale reports whether the verdict may not reflect the current contract.
//
// Unknown upstream is NOT stale. A vendored copy or a detached HEAD is a normal
// way to run this, and warning on it every time is how a warning gets ignored —
// which would cost more than the case it is meant to catch.
func (p Provenance) Stale() bool { return p.Behind > 0 || p.Dirty }

// StaleWarning is the line to print when Stale, or "" when there is nothing to
// say. It names the cure, because "may be stale" without one sends the reader
// looking for a content bug, which is the original failure.
func (p Provenance) StaleWarning() string {
	switch {
	case p.Dirty && p.Behind > 0:
		return fmt.Sprintf("⚠ stamp has uncommitted changes and is %d commit(s) behind upstream — "+
			"this verdict reflects neither origin nor any ref; `git -C %s status` then pull", p.Behind, p.Root)
	case p.Dirty:
		return fmt.Sprintf("⚠ stamp has uncommitted changes — this verdict reflects a tree no ref "+
			"contains; check `git -C %s status`", p.Root)
	case p.Behind > 0:
		return fmt.Sprintf("⚠ stamp is %d commit(s) behind upstream — a kind reported unknown may "+
			"exist upstream; `git -C %s pull` before trusting this", p.Behind, p.Root)
	default:
		return ""
	}
}

// ReadProvenance describes the stamp checkout at root. It never fails: every
// field degrades to a zero value or -1, because refusing to lint over a missing
// git binary would trade a small loss of context for a total outage.
func ReadProvenance(ctx context.Context, root string) Provenance {
	p := Provenance{Root: root, Behind: -1}

	schemaDir := filepath.Join(root, "src", "schemas")

	if sha := gitLine(ctx, root, "rev-parse", "--short", "HEAD"); sha != "" {
		p.Commit = sha

		// Scoped to the schema path: churn elsewhere in lightwave-core does not
		// change what this verdict was reached against.
		p.Dirty = gitLine(ctx, root, "status", "--porcelain", "--", schemaDir) != ""

		if n := gitLine(ctx, root, "rev-list", "--count", "HEAD..@{upstream}"); n != "" {
			if parsed, err := strconv.Atoi(n); err == nil {
				p.Behind = parsed
			}
		}
	}

	p.SpecKindsVersion = metaVersion(filepath.Join(
		schemaDir, "policy", "governance", "spec_artifact_kinds.yaml"))

	return p
}

// gitLine runs a git command in dir and returns its first line, or "" on any
// failure. Errors are information here, not faults: "not a git checkout" and
// "no upstream configured" are both ordinary states.
func gitLine(ctx context.Context, dir string, args ...string) string {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// metaVersion reads _meta.version from a schema file, or "" if unreadable.
func metaVersion(path string) string {
	raw, err := os.ReadFile(path) //nolint:gosec // a path inside the resolved stamp checkout
	if err != nil {
		return ""
	}

	var doc struct {
		Meta struct {
			Version string `yaml:"version"`
		} `yaml:"_meta"`
	}

	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return ""
	}

	return doc.Meta.Version
}
