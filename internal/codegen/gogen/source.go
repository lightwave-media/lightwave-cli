package gogen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Source resolves schema YAML from lightwave-core.
//
// WHY THIS IS AN INTERFACE. The generator used to call os.ReadDir and
// os.ReadFile directly against <root>/lightwave-core/src/schemas/<family> —
// the WORKING TREE of a checkout this host shares. Measured 2026-09-13 that
// checkout sat on a feature branch at 5c77cb2 while origin/main was a8e634c,
// with nine concurrent worktrees attached. So "generated from the stamp"
// named no particular stamp: two runs an hour apart could differ with no
// change to main, and nothing recorded which version was read.
//
// ADR-0049 makes the database a DERIVED index whose safety property is that
// it rebuilds from truth. That property is worth nothing if "truth" is
// whatever branch a sibling session happens to have checked out. Resolving by
// git ref makes the input nameable, and the resolved sha is printed so
// downstream records can carry it.
type Source interface {
	// List returns schema paths under dir, relative to the repo root.
	List(ctx context.Context, dir string) ([]string, error)
	// Read returns the bytes of one path from List.
	Read(ctx context.Context, p string) ([]byte, error)
	// Describe names what was read, for the generation summary.
	Describe() string
}

// WorktreeRef is the ref value that opts back into reading the working tree.
// Spelled as a word rather than an empty string so it appears in shell history
// and in the summary line: reading a mutable tree should be a thing someone
// chose, not a default they never saw.
const WorktreeRef = "worktree"

// shortSHALen is how much of a sha the summary line carries — enough to be
// unambiguous in this repo, short enough to read.
const shortSHALen = 12

// FSSource reads the working tree. Correct when iterating on a schema that is
// not committed yet; wrong as a default, per the type doc above.
type FSSource struct{ Root string }

func (s FSSource) List(_ context.Context, dir string) ([]string, error) {
	full := filepath.Join(s.Root, dir)

	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}

	var out []string

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") || e.Name() == "__index.yaml" {
			continue
		}

		out = append(out, path.Join(dir, e.Name()))
	}

	sort.Strings(out)

	return out, nil
}

func (s FSSource) Read(_ context.Context, p string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.Root, p))
}

func (s FSSource) Describe() string { return "working tree (UNPINNED)" }

// GitSource reads a committed ref. The default.
type GitSource struct {
	Root string
	Ref  string
	sha  string
}

// NewGitSource resolves ref to a sha up front, so a bad ref fails before any
// generation happens rather than producing an empty, plausible result.
func NewGitSource(ctx context.Context, root, ref string) (*GitSource, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", ref+"^{commit}").Output() //nolint:gosec // operator-supplied ref against a local checkout
	if err != nil {
		return nil, fmt.Errorf("cannot resolve ref %q in %s (try --ref %s to read the working tree): %w",
			ref, root, WorktreeRef, err)
	}

	return &GitSource{Root: root, Ref: ref, sha: strings.TrimSpace(string(out))}, nil
}

func (s *GitSource) List(ctx context.Context, dir string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", s.Root, "ls-tree", "-r", "--name-only", s.sha, "--", dir).Output() //nolint:gosec // sha resolved by NewGitSource
	if err != nil {
		return nil, fmt.Errorf("listing %s at %s: %w", dir, s.Ref, err)
	}

	var paths []string

	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" || !strings.HasSuffix(line, ".yaml") || path.Base(line) == "__index.yaml" {
			continue
		}

		paths = append(paths, line)
	}

	sort.Strings(paths)

	return paths, nil
}

func (s *GitSource) Read(ctx context.Context, p string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", s.Root, "show", s.sha+":"+p).Output() //nolint:gosec // sha resolved by NewGitSource
	if err != nil {
		return nil, fmt.Errorf("reading %s at %s: %w", p, s.Ref, err)
	}

	return out, nil
}

// SHA is the resolved commit. Callers print it so a generated artifact can say
// which stamp produced it.
func (s *GitSource) SHA() string { return s.sha }

func (s *GitSource) Describe() string {
	short := s.sha
	if len(short) > shortSHALen {
		short = short[:shortSHALen]
	}

	return fmt.Sprintf("%s@%s", s.Ref, short)
}

// NewSource returns the working-tree source for WorktreeRef, else a git source.
func NewSource(ctx context.Context, root, ref string) (Source, error) {
	if ref == WorktreeRef {
		return FSSource{Root: root}, nil
	}

	return NewGitSource(ctx, root, ref)
}
