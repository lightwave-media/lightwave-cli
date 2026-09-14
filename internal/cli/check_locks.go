package cli

// check_locks.go — `lw check locks`.
//
// linked-incident: failures/check-locks-stderr-as-diff.yaml
//
// Three defects, one cause: the command did not do what it said (#444).
//
//  1. It resolved its root to paths.lightwave_root — ~/dev, the flat SIBLING
//     PARENT, which is not a git repository. `git diff` there writes
//     "warning: Not a git repository" to stderr and exits 0.
//  2. runGitDiff called CombinedOutput(), merging that warning into stdout, and
//     then discarded the error — both branches returned nil, so the if/else was
//     a no-op. The caller treated any non-empty output as a diff, so git's
//     ERROR TEXT was counted as drift. (The old comment claimed "non-zero exit
//     also means changes", which is wrong on its own terms: plain `git diff`
//     exits 0 whether or not anything changed.)
//  3. --staged is declared for this command in the stamp, and the handler
//     discarded the flags map, so it had never done anything.
//
// Net effect: it reported uv.lock and pnpm-lock.yaml as dirty on every machine,
// naming two files that exist nowhere in the fleet root. It could not return 0,
// which AGENTS.md's check contract requires of a clean run — and a violation
// naming real, familiar filenames is the one failure mode that looks like the
// tool working.
//
// The lock files live in the repos that own them (lightwave-core/uv.lock,
// lightwave-ui/pnpm-lock.yaml), never at the workspace root, so the check is
// scoped to the repository the caller is standing in — the same default
// `lw check repo-infra` uses. A repo with no lock file has nothing to drift and
// says so out loud, because a silent tick there is indistinguishable from a
// check looking in the wrong place, which is precisely what this was.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
)

// lockFiles are the lock files whose drift breaks CI.
var lockFiles = []string{"uv.lock", "pnpm-lock.yaml"}

// porcelainStatusWidth is the width of `git status --porcelain`'s status
// column: X = index, Y = working tree.
const porcelainStatusWidth = 2

// repoRootFrom resolves the git repository containing dir.
//
// A failure here is exit 2 (tool error), not exit 1 (violation): "you are not
// in a repository" is not a finding about lock files, and reporting it as one
// is how this command came to claim drift that did not exist.
func repoRootFrom(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %s (exit 2)", dir)
	}

	return strings.TrimSpace(string(out)), nil
}

// lockDrift returns the lock files present in repo, and which of them carry
// uncommitted changes.
//
// stagedOnly narrows to the index, which is what --staged has always claimed to
// do. Without it both the index and the working tree count: the stamp says "no
// uncommitted changes", and a staged-but-uncommitted lock file is exactly the
// drift that reaches CI.
//
// `git status --porcelain` answers both in one call and, unlike `git diff`,
// fails loudly outside a work tree instead of printing usage text to stderr.
// Output() rather than CombinedOutput() keeps that failure on the error channel
// where the caller can see it.
func lockDrift(ctx context.Context, repo string, stagedOnly bool) (present, dirty []string, err error) {
	for _, name := range lockFiles {
		if _, statErr := os.Stat(filepath.Join(repo, name)); statErr != nil {
			continue // this repo does not carry that lock file
		}

		present = append(present, name)

		cmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "--", name)
		cmd.Dir = repo

		out, cmdErr := cmd.Output()
		if cmdErr != nil {
			return nil, nil, fmt.Errorf("git status %s in %s: %w (exit 2)", name, repo, cmdErr)
		}

		if statusIsDirty(string(out), stagedOnly) {
			dirty = append(dirty, name)
		}
	}

	return present, dirty, nil
}

// statusIsDirty reads `git status --porcelain` output for a single path.
//
// Each line is "XY <path>": X is the index state, Y the working tree, and a
// space means unchanged. "??" is untracked, which counts as uncommitted — an
// untracked uv.lock is precisely the drift CI trips over — but it is not
// STAGED, so --staged passes over it.
func statusIsDirty(out string, stagedOnly bool) bool {
	for line := range strings.SplitSeq(out, "\n") {
		if len(line) < porcelainStatusWidth {
			continue
		}

		index, worktree := line[0], line[1]

		if stagedOnly {
			if index != ' ' && index != '?' {
				return true
			}

			continue
		}

		if index != ' ' || worktree != ' ' {
			return true
		}
	}

	return false
}

// checkLocksHandler verifies the repo's lock files carry no uncommitted changes.
func checkLocksHandler(ctx context.Context, _ []string, flags map[string]any) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w (exit 2)", err)
	}

	repo, err := repoRootFrom(ctx, cwd)
	if err != nil {
		return err
	}

	present, dirty, err := lockDrift(ctx, repo, flagBool(flags, "staged"))
	if err != nil {
		return err
	}

	if len(dirty) > 0 {
		return fmt.Errorf("uncommitted lock changes in %s: %s", repo, strings.Join(dirty, ", "))
	}

	if len(present) == 0 {
		fmt.Println(color.GreenString("✓ no lock files in " + repo + " — nothing to drift"))

		return nil
	}

	fmt.Println(color.GreenString("✓ lock files clean: " + strings.Join(present, ", ")))

	return nil
}
