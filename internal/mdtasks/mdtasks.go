// Package mdtasks is the WRITE surface for markdown-canonical task
// artefacts in `lightwave-media/docs/<domain>/tasks/`. Read surface
// lives in internal/mddocs (Parse, FindByID, BuildBundle); this
// package is just the create/edit/close/next-ID side.
//
// Per documentation-workflow.md §7, markdown is Phase A canonical.
// SQLite/Postgres indexes are caches that index this package's output.
package mdtasks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/mddocs"
	"gopkg.in/yaml.v3"
)

// NewOptions configures a `lw task new` call. Title is required;
// everything else is best-effort defaults.
type NewOptions struct {
	LightwaveRoot  string // monorepo root (cfg.Paths.LightwaveRoot)
	Domain         string // e.g. "software"
	Title          string // required
	Body           string // markdown body after the frontmatter fence
	Owner          string // free-form user id (Phase A)
	CreatedBy      string // free-form user id (Phase A)
	AssignedTo     string // optional; defaults to Owner
	ParentStory    string // optional US-NNN
	ParentEpic     string // optional EB-NNN
	AssignedSprint string // optional SPR-NNN
	Priority       string // optional p1_urgent / p2_high / …
	Status         string // optional; defaults to "draft"
}

// New writes a fresh task markdown file under
// docs/<domain>/tasks/T-NNNN-<slug>.md and returns the absolute path
// + assigned ID. The ID is the next available T-NNNN across the docs
// tree (zero-padded to 4 digits — sortable).
func New(opts NewOptions) (path, id string, err error) {
	if opts.LightwaveRoot == "" {
		return "", "", errors.New("LightwaveRoot is required")
	}
	if opts.Domain == "" {
		return "", "", errors.New("--domain is required")
	}
	if strings.TrimSpace(opts.Title) == "" {
		return "", "", errors.New("--title is required")
	}

	docs := mddocs.DocsRoot(opts.LightwaveRoot)
	tasksDir := filepath.Join(docs, opts.Domain, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir tasks dir: %w", err)
	}

	id, err = NextTaskID(opts.LightwaveRoot)
	if err != nil {
		return "", "", err
	}

	now := time.Now().UTC().Format("2006-01-02")
	owner := opts.Owner
	if owner == "" {
		owner = "joel"
	}
	createdBy := opts.CreatedBy
	if createdBy == "" {
		createdBy = owner
	}
	assignedTo := opts.AssignedTo
	if assignedTo == "" {
		assignedTo = owner
	}
	status := opts.Status
	if status == "" {
		status = "draft"
	}

	// Build frontmatter as an ordered yaml.Node MappingNode so field
	// order is stable (yaml.v3's struct marshalling honours field order,
	// but the explicit node lets us conditionally include optional keys
	// without an N-field struct + zero-value gymnastics).
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	addPair := func(k, v string) {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: k},
			&yaml.Node{Kind: yaml.ScalarNode, Value: v},
		)
	}
	addPair("id", id)
	addPair("domain", opts.Domain)
	addPair("type", "task")
	addPair("title", opts.Title)
	addPair("status", status)
	addPair("owner", owner)
	addPair("created_by", createdBy)
	addPair("assigned_to", assignedTo)
	addPair("created_at", now)
	addPair("updated_at", now)
	if opts.ParentStory != "" {
		addPair("parent_story", opts.ParentStory)
	}
	if opts.ParentEpic != "" {
		addPair("parent_epic", opts.ParentEpic)
	}
	if opts.AssignedSprint != "" {
		addPair("assigned_sprint", opts.AssignedSprint)
	}
	if opts.Priority != "" {
		addPair("priority", opts.Priority)
	}

	frontBytes, err := yaml.Marshal(mapping)
	if err != nil {
		return "", "", fmt.Errorf("marshal frontmatter: %w", err)
	}

	slug := slugify(opts.Title)
	filename := fmt.Sprintf("%s-%s.md", id, slug)
	path = filepath.Join(tasksDir, filename)

	body := strings.TrimSpace(opts.Body)
	var doc strings.Builder
	doc.WriteString("---\n")
	doc.Write(frontBytes)
	doc.WriteString("---\n\n")
	fmt.Fprintf(&doc, "# %s — %s\n\n", id, opts.Title)
	if body != "" {
		doc.WriteString(body)
		doc.WriteByte('\n')
	}

	if err := os.WriteFile(path, []byte(doc.String()), 0o644); err != nil {
		return "", "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, id, nil
}

// EditOptions configures `lw task edit`. Only non-nil fields are
// applied — the caller decides which keys are being changed. Re-writes
// the whole file with the updated frontmatter; preserves body verbatim.
type EditOptions struct {
	LightwaveRoot string
	Domain        string // optional; speeds up lookup

	Title          *string
	Status         *string
	AssignedTo     *string
	ParentStory    *string
	ParentEpic     *string
	AssignedSprint *string
	Priority       *string
}

// Edit applies the non-nil fields in opts to the task identified by id.
// Updates `updated_at` automatically. Returns the path to the rewritten
// file.
func Edit(id string, opts EditOptions) (string, error) {
	if opts.LightwaveRoot == "" {
		return "", errors.New("LightwaveRoot is required")
	}
	artefact, err := mddocs.FindByID(opts.LightwaveRoot, opts.Domain, id)
	if err != nil {
		return "", err
	}

	// Re-parse frontmatter via yaml.Node so unknown keys survive the
	// round-trip (priority, story_points, implementation_target, etc.).
	raw, err := os.ReadFile(artefact.Path)
	if err != nil {
		return "", err
	}
	fmText, body, err := splitFrontmatter(raw)
	if err != nil {
		return "", err
	}

	var fm yaml.Node
	if err := yaml.Unmarshal(fmText, &fm); err != nil {
		return "", fmt.Errorf("parse frontmatter: %w", err)
	}

	// fm is a DocumentNode wrapping a MappingNode.
	if len(fm.Content) == 0 || fm.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("frontmatter is not a mapping")
	}
	mapping := fm.Content[0]

	if opts.Title != nil {
		setOrAdd(mapping, "title", *opts.Title)
	}
	if opts.Status != nil {
		setOrAdd(mapping, "status", *opts.Status)
	}
	if opts.AssignedTo != nil {
		setOrAdd(mapping, "assigned_to", *opts.AssignedTo)
	}
	if opts.ParentStory != nil {
		setOrAdd(mapping, "parent_story", *opts.ParentStory)
	}
	if opts.ParentEpic != nil {
		setOrAdd(mapping, "parent_epic", *opts.ParentEpic)
	}
	if opts.AssignedSprint != nil {
		setOrAdd(mapping, "assigned_sprint", *opts.AssignedSprint)
	}
	if opts.Priority != nil {
		setOrAdd(mapping, "priority", *opts.Priority)
	}
	setOrAdd(mapping, "updated_at", time.Now().UTC().Format("2006-01-02"))

	newFrontBytes, err := yaml.Marshal(&fm)
	if err != nil {
		return "", fmt.Errorf("marshal frontmatter: %w", err)
	}

	var out strings.Builder
	out.WriteString("---\n")
	out.Write(newFrontBytes)
	out.WriteString("---\n")
	out.WriteString(body)

	if err := os.WriteFile(artefact.Path, []byte(out.String()), 0o644); err != nil {
		return "", err
	}
	return artefact.Path, nil
}

// Close is the shortcut for Edit with Status="done".
func Close(id string, lightwaveRoot, domain string) (string, error) {
	done := "done"
	return Edit(id, EditOptions{
		LightwaveRoot: lightwaveRoot,
		Domain:        domain,
		Status:        &done,
	})
}

// NextTaskID walks every domain's tasks/ directory under docs/, finds
// the highest existing T-NNNN, and returns the next one zero-padded to
// 4 digits. T-0001 is the first ID when no tasks exist.
func NextTaskID(lightwaveRoot string) (string, error) {
	docs := mddocs.DocsRoot(lightwaveRoot)
	highest := 0

	entries, err := os.ReadDir(docs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "T-0001", nil
		}
		return "", err
	}

	for _, dom := range entries {
		if !dom.IsDir() {
			continue
		}
		tasksDir := filepath.Join(docs, dom.Name(), "tasks")
		files, err := os.ReadDir(tasksDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			n := extractTaskNumber(f.Name())
			if n > highest {
				highest = n
			}
		}
	}

	return fmt.Sprintf("T-%04d", highest+1), nil
}

var taskIDRE = regexp.MustCompile(`^T-(\d+)`)

func extractTaskNumber(filename string) int {
	m := taskIDRE.FindStringSubmatch(filename)
	if m == nil {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(m[1], "%d", &n)
	return n
}

// slugify produces a kebab-case slug truncated to 40 chars for
// filenames. Same shape as the helper in task_handlers.go but local to
// avoid a cli ↔ mdtasks dependency cycle.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 40 {
		out = strings.TrimRight(out[:40], "-")
	}
	if out == "" {
		out = "task"
	}
	return out
}

// splitFrontmatter splits a markdown file into its `---`-fenced YAML
// frontmatter and the body. Returns an error when the frontmatter is
// missing or unterminated.
func splitFrontmatter(data []byte) (frontmatter []byte, body string, err error) {
	if !strings.HasPrefix(string(data), "---\n") && !strings.HasPrefix(string(data), "---\r\n") {
		return nil, "", errors.New("missing leading frontmatter fence '---'")
	}
	rest := string(data)
	rest = strings.TrimPrefix(rest, "---\n")
	rest = strings.TrimPrefix(rest, "---\r\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil, "", errors.New("unterminated frontmatter (no closing '---')")
	}
	frontmatter = []byte(rest[:idx])
	body = rest[idx+len("\n---"):]
	// Strip the single trailing newline after the closing fence if
	// present so writes round-trip cleanly.
	body = strings.TrimPrefix(body, "\n")
	return frontmatter, body, nil
}

// setOrAdd updates an existing key in a YAML mapping node, or appends it
// when missing. Used by Edit so unknown keys (priority, story_points,
// etc.) survive untouched.
func setOrAdd(mapping *yaml.Node, key, value string) {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Tag = ""
			return
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	v := &yaml.Node{Kind: yaml.ScalarNode, Value: value}
	mapping.Content = append(mapping.Content, k, v)
}
