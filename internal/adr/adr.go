// Package adr reserves Architecture Decision Record identifiers atomically and
// renders the skeleton the stamp requires.
//
// It exists because ADR ids were hand-allocated by reading the directory, and
// parallel sessions collided. That is not hypothetical: on lightwave-core's
// spec/adr/ at origin/main, ELEVEN numbers are double-assigned — 0001, 0002,
// 0003, 0004, 0005, 0028, 0032, 0033, 0036, 0037, 0038. One collision is
// recorded in the corpus itself: ~/.lightwave/specs/adr/ADR-0032 carries a
// `renumbered_from: "ADR-0006"` note explaining that two files claimed 0006, so
// the index stored one under a mangled composite key and it was never
// addressable by its id.
//
// Read-then-write is the bug. Two sessions both read max=52, both write 0053.
// So the scan and the create happen inside one flock'd critical section, and
// the create itself is O_EXCL — belt and braces, because flock is advisory and
// a writer that never took the lock must still not clobber a file.
package adr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Tree is one ADR corpus.
//
// The two corpora disagree about more than location, so the allocator carries
// both shapes rather than normalising one into the other. Writing an `id:` key
// into lightwave-core would make the new file inconsistent with its 50+
// siblings, which all use `adr_id:`; writing `adr_id:` into the host tree would
// be equally wrong there. A scaffolder's job is to produce a file that looks
// like its neighbours.
type Tree struct {
	// Name is the --tree value: "core" or "host".
	Name string
	// Dir is the absolute directory holding the corpus.
	Dir string
	// IDPrefix prefixes the identifier: CORE-0053, ADR-0033.
	IDPrefix string
	// IDKey is the frontmatter key carrying the identifier.
	IDKey string
	// FilePrefix prefixes the filename. Core files are `0053-slug.md`; host
	// files are `ADR-0033-slug.md`.
	FilePrefix string
}

// CoreTree returns the lightwave-core corpus, rooted at the workspace.
func CoreTree(lightwaveRoot string) Tree {
	return Tree{
		Name:       "core",
		Dir:        filepath.Join(lightwaveRoot, "lightwave-core", "spec", "adr"),
		IDPrefix:   "CORE",
		IDKey:      "adr_id",
		FilePrefix: "",
	}
}

// HostTree returns the operator-home corpus.
func HostTree(home string) Tree {
	return Tree{
		Name:       "host",
		Dir:        filepath.Join(home, ".lightwave", "specs", "adr"),
		IDPrefix:   "ADR",
		IDKey:      "id",
		FilePrefix: "ADR-",
	}
}

// TreeFor resolves a --tree value. An unknown value is an error rather than a
// silent default: picking a corpus for the caller would file the decision in
// the wrong repository, which is expensive to notice and annoying to undo.
func TreeFor(name, lightwaveRoot, home string) (Tree, error) {
	switch name {
	case "core":
		return CoreTree(lightwaveRoot), nil
	case "host":
		return HostTree(home), nil
	case "":
		return Tree{}, errors.New("--tree is required: core (lightwave-core/spec/adr) or host (~/.lightwave/specs/adr)")
	default:
		return Tree{}, fmt.Errorf("unknown --tree %q: expected core or host", name)
	}
}

// numberPattern matches both filename shapes found in the corpora: a bare
// `0047-worktree-single-root.md` and a prefixed `ADR-0031-agent-runner.md`.
//
// Both genuinely occur in lightwave-core's spec/adr/ today, so a scanner that
// knows only the bare form reads max=0052 while ADR-0031 sits beside it. That
// happens to be harmless at 52 and would not be at 31.
var numberPattern = regexp.MustCompile(`^(?:ADR-)?(\d{4})-`)

// ParseNumber extracts the sequence number from an ADR filename.
func ParseNumber(filename string) (int, bool) {
	m := numberPattern.FindStringSubmatch(filename)
	if m == nil {
		return 0, false
	}

	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}

	return n, true
}

// Max returns the highest id currently present, or 0 for an empty corpus.
//
// A file whose name does not parse is skipped rather than failing the scan.
// The corpora contain hand-authored strays (README.md, an index), and refusing
// to allocate because a neighbour is misnamed would make the tool unusable in
// exactly the messy trees it exists to protect.
func (t *Tree) Max() (int, error) {
	entries, err := os.ReadDir(t.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("no ADR tree at %s", t.Dir)
		}

		return 0, fmt.Errorf("read %s: %w", t.Dir, err)
	}

	maxSeen := 0

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		n, ok := ParseNumber(e.Name())
		if ok && n > maxSeen {
			maxSeen = n
		}
	}

	return maxSeen, nil
}

var (
	nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)
	trimDashes      = regexp.MustCompile(`^-+|-+$`)
)

// Slugify renders a title as a filename-safe slug.
func Slugify(title string) string {
	s := strings.ToLower(title)
	s = nonAlphanumeric.ReplaceAllString(s, "-")
	s = trimDashes.ReplaceAllString(s, "")

	return s
}

// ID renders the identifier for a sequence number: CORE-0053, ADR-0033.
func (t *Tree) ID(n int) string {
	return fmt.Sprintf("%s-%04d", t.IDPrefix, n)
}

// Filename renders the file name for a sequence number and slug.
func (t *Tree) Filename(n int, slug string) string {
	return fmt.Sprintf("%s%04d-%s.md", t.FilePrefix, n, slug)
}

// Skeleton is the content of a freshly reserved ADR.
type Skeleton struct {
	ID         string
	IDKey      string
	Title      string
	Status     string
	DecidedAt  string
	Area       string
	Supersedes string
}

// Render writes the skeleton.
//
// The frontmatter and section set are dictated by the stamp, not by taste:
// policy/governance/spec_artifact_kinds.yaml declares frontmatter_required
// [kind, status, decided_at] and required_sections [Context, Decision,
// Consequences] for kind `adr`, and names `lw docs spec-lint` as the validator.
// A skeleton that does not satisfy its own linter is worse than no scaffolder,
// because it produces files that fail a gate the author did not run.
//
// `status` defaults to `proposed` — the declared default of the adr_statuses
// enum, which is a closed set.
//
// The identity keys (id, title, area) come from the data schema
// data/reference_documents/adr.yaml. Note the two stamped files disagree about
// the date key: spec_artifact_kinds requires `decided_at`, while adr.yaml lists
// `date`. `decided_at` is emitted because it is the one the validator enforces;
// the divergence is reported upstream rather than papered over by emitting both.
func (t *Tree) Render(sk *Skeleton) string {
	var b strings.Builder

	b.WriteString("---\n")
	fmt.Fprintf(&b, "%s: \"%s\"\n", sk.IDKey, sk.ID)
	b.WriteString("kind: adr\n")
	fmt.Fprintf(&b, "title: %q\n", sk.Title)
	fmt.Fprintf(&b, "status: %q\n", sk.Status)
	fmt.Fprintf(&b, "decided_at: %s\n", sk.DecidedAt)
	fmt.Fprintf(&b, "area: %q\n", sk.Area)

	if sk.Supersedes != "" {
		fmt.Fprintf(&b, "supersedes: %q\n", sk.Supersedes)
	} else {
		b.WriteString("supersedes: null\n")
	}

	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s\n\n", sk.Title)

	b.WriteString("## Context\n\n")
	b.WriteString("<!-- One paragraph: the forces in play. What is being decided and why now.\n")
	b.WriteString("     Name the constraints and the trade-off space. -->\n\n")

	b.WriteString("## Decision\n\n")
	b.WriteString("<!-- The decision itself, stated clearly. One paragraph max. -->\n\n")

	b.WriteString("## Consequences\n\n")
	b.WriteString("<!-- Prefix each with '+' for positive or '-' for negative.\n")
	b.WriteString("     Honest tradeoff disclosure is required. -->\n\n")
	b.WriteString("- +\n")
	b.WriteString("- -\n\n")

	b.WriteString("## Alternatives considered\n\n")
	b.WriteString("<!-- At least one. The stamp treats 'no alternative' as itself a flag. -->\n\n")
	b.WriteString("- **Option:** \n")
	b.WriteString("  **Why rejected:** \n")

	return b.String()
}

// Result describes a reservation.
type Result struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Tree   string `json:"tree"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Number int    `json:"number"`
	DryRun bool   `json:"dry_run"`
}

// lockName is the flock target. It sits inside the corpus so the lock and the
// thing it protects cannot be separated by a caller passing a different root.
const lockName = ".adr-alloc.lock"

const (
	lockPerm = 0o644
	filePerm = 0o644
)

// Reserve allocates the next id and writes the skeleton.
//
// The scan and the create are one critical section. Doing them separately is
// the collision this package exists to prevent, and no amount of retrying
// outside the lock fixes it — two readers agree on max, then both write.
//
// dryRun takes the lock and reports what would be written without writing, so
// a preview cannot itself consume an id.
func (t *Tree) Reserve(title, area, supersedes string, now time.Time, dryRun bool) (Result, error) {
	if strings.TrimSpace(title) == "" {
		return Result{}, errors.New("title is required")
	}

	if _, err := os.Stat(t.Dir); err != nil {
		return Result{}, fmt.Errorf("no ADR tree at %s", t.Dir)
	}

	unlock, err := t.lock()
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	maxSeen, err := t.Max()
	if err != nil {
		return Result{}, err
	}

	next := maxSeen + 1
	slug := Slugify(title)

	if slug == "" {
		return Result{}, fmt.Errorf("title %q slugifies to nothing; use letters or digits", title)
	}

	path := filepath.Join(t.Dir, t.Filename(next, slug))

	res := Result{
		ID:     t.ID(next),
		Number: next,
		Path:   path,
		Tree:   t.Name,
		Title:  title,
		Status: "proposed",
		DryRun: dryRun,
	}

	if dryRun {
		return res, nil
	}

	body := t.Render(&Skeleton{
		ID:         res.ID,
		IDKey:      t.IDKey,
		Title:      title,
		Status:     res.Status,
		DecidedAt:  now.Format("2006-01-02"),
		Area:       area,
		Supersedes: supersedes,
	})

	// O_EXCL even though the lock is held. flock is advisory: a writer that
	// never took the lock is unaffected by it, and silently overwriting
	// somebody's in-progress ADR is the one outcome worse than refusing.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		if os.IsExist(err) {
			return Result{}, fmt.Errorf("refusing to overwrite %s", path)
		}

		return Result{}, fmt.Errorf("create %s: %w", path, err)
	}

	if _, err := f.WriteString(body); err != nil {
		f.Close()

		return Result{}, fmt.Errorf("write %s: %w", path, err)
	}

	if err := f.Close(); err != nil {
		return Result{}, fmt.Errorf("close %s: %w", path, err)
	}

	return res, nil
}

// lock takes an exclusive advisory lock on the corpus and returns its release.
func (t *Tree) lock() (func(), error) {
	lockPath := filepath.Join(t.Dir, lockName)

	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, lockPerm)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", lockPath, err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()

		return nil, fmt.Errorf("lock %s: %w", lockPath, err)
	}

	return func() {
		//nolint:errcheck // best-effort unlock; the close below releases it anyway
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
