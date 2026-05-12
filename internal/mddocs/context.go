package mddocs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Bundle is the assembled context handed to a sealed sub-session. It
// contains the seed artefact (the task) plus every linked ancestor and
// reference that was successfully resolved. Missing refs are recorded in
// Warnings rather than failing the bundle, because a partially-linked
// task is still useful to an agent.
type Bundle struct {
	Seed     *Artefact
	Story    *Artefact // parent_story
	Epic     *Artefact // parent_epic (via story when present, else seed)
	Sprint   *Artefact // assigned_sprint (via story or seed)
	Refs     []RefDoc  // refs_sad / refs_nfrs / refs_ddd / refs_prd / refs_naming
	Warnings []string
}

// RefDoc is an external reference file (SAD, NFRs, DDD, PRD, Naming) loaded
// verbatim. The artefact's body is included so the bundle is self-contained.
type RefDoc struct {
	Kind string // "SAD", "NFRs", "DDD", "PRD", "Naming"
	Path string // absolute path on disk
	Body string // file contents (frontmatter stripped if present)
}

// BuildBundle assembles a context bundle for the seed artefact, walking
// the frontmatter linkage graph upward and resolving file refs.
//
// Walk order for a Task seed:
//  1. seed (the task)
//  2. parent_story (User Story) — if set
//  3. parent_epic — preferred from the story's frontmatter, falls back to seed
//  4. assigned_sprint — preferred from the story's frontmatter, falls back to seed
//  5. refs_* — from seed first, then story, then epic; deduped by absolute path
//
// All `refs_*` paths in frontmatter are resolved relative to the file
// holding the reference (the convention in existing artefacts).
func BuildBundle(lightwaveRoot string, seed *Artefact) (*Bundle, error) {
	if seed == nil {
		return nil, errors.New("seed artefact is nil")
	}
	b := &Bundle{Seed: seed}

	storyID := seed.Frontmatter.ParentStory
	if storyID != "" {
		story, err := FindByID(lightwaveRoot, seed.Frontmatter.Domain, storyID)
		if err != nil {
			b.Warnings = append(b.Warnings, fmt.Sprintf("parent_story %s: %v", storyID, err))
		} else {
			b.Story = story
		}
	}

	epicID := ""
	if b.Story != nil && b.Story.Frontmatter.ParentEpic != "" {
		epicID = b.Story.Frontmatter.ParentEpic
	} else if seed.Frontmatter.ParentEpic != "" {
		epicID = seed.Frontmatter.ParentEpic
	}
	if epicID != "" {
		epic, err := FindByID(lightwaveRoot, seed.Frontmatter.Domain, epicID)
		if err != nil {
			b.Warnings = append(b.Warnings, fmt.Sprintf("parent_epic %s: %v", epicID, err))
		} else {
			b.Epic = epic
		}
	}

	sprintID := ""
	if b.Story != nil && b.Story.Frontmatter.AssignedSprint != "" {
		sprintID = b.Story.Frontmatter.AssignedSprint
	} else if seed.Frontmatter.AssignedSprint != "" {
		sprintID = seed.Frontmatter.AssignedSprint
	}
	if sprintID != "" {
		sprint, err := FindByID(lightwaveRoot, seed.Frontmatter.Domain, sprintID)
		if err != nil {
			b.Warnings = append(b.Warnings, fmt.Sprintf("assigned_sprint %s: %v", sprintID, err))
		} else {
			b.Sprint = sprint
		}
	}

	seen := map[string]bool{}
	for _, a := range []*Artefact{seed, b.Story, b.Epic} {
		if a == nil {
			continue
		}
		for _, r := range collectRefs(a) {
			if seen[r.Path] {
				continue
			}
			seen[r.Path] = true
			doc, err := loadRef(r.Path)
			if err != nil {
				b.Warnings = append(b.Warnings, fmt.Sprintf("%s ref %s: %v", r.Kind, r.Path, err))
				continue
			}
			doc.Kind = r.Kind
			b.Refs = append(b.Refs, doc)
		}
	}

	return b, nil
}

type refPointer struct {
	Kind string
	Path string // absolute, resolved relative to the source artefact
}

func collectRefs(a *Artefact) []refPointer {
	dir := filepath.Dir(a.Path)
	pairs := []struct {
		kind string
		ref  string
	}{
		{"SAD", a.Frontmatter.RefsSAD},
		{"NFRs", a.Frontmatter.RefsNFRs},
		{"DDD", a.Frontmatter.RefsDDD},
		{"PRD", a.Frontmatter.RefsPRD},
		{"Naming", a.Frontmatter.RefsNaming},
	}

	var out []refPointer
	for _, p := range pairs {
		if p.ref == "" {
			continue
		}
		// Strip an optional `#section` anchor — readers want the whole
		// file plus the anchor preserved as a pointer in the output.
		path := p.ref
		if idx := strings.Index(path, "#"); idx >= 0 {
			path = path[:idx]
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		out = append(out, refPointer{Kind: p.kind, Path: abs})
	}
	return out
}

func loadRef(path string) (RefDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RefDoc{}, err
	}
	body := string(data)
	if strings.HasPrefix(body, "---\n") {
		if idx := strings.Index(body[4:], "\n---\n"); idx >= 0 {
			body = body[4+idx+len("\n---\n"):]
		}
	}
	return RefDoc{Path: path, Body: body}, nil
}

// Render emits the bundle as a single markdown blob suitable for piping
// into a sealed sub-session's system prompt. The shape is deliberately
// stable: agents may template-match against the section headers.
func (b *Bundle) Render() string {
	var w strings.Builder

	w.WriteString("# Context Bundle\n\n")
	fmt.Fprintf(&w, "Assembled for **%s** — %s\n\n",
		b.Seed.Frontmatter.ID, b.Seed.Frontmatter.Title)

	if len(b.Warnings) > 0 {
		w.WriteString("> **Warnings during bundle assembly:**\n")
		for _, msg := range b.Warnings {
			w.WriteString("> - ")
			w.WriteString(msg)
			w.WriteByte('\n')
		}
		w.WriteByte('\n')
	}

	w.WriteString("## Index\n\n")
	fmt.Fprintf(&w, "- Task: `%s` — %s\n", b.Seed.Frontmatter.ID, relpath(b.Seed.Path))
	if b.Story != nil {
		fmt.Fprintf(&w, "- User Story: `%s` — %s\n", b.Story.Frontmatter.ID, relpath(b.Story.Path))
	}
	if b.Epic != nil {
		fmt.Fprintf(&w, "- Epic Brief: `%s` — %s\n", b.Epic.Frontmatter.ID, relpath(b.Epic.Path))
	}
	if b.Sprint != nil {
		fmt.Fprintf(&w, "- Sprint: `%s` — %s\n", b.Sprint.Frontmatter.ID, relpath(b.Sprint.Path))
	}
	for _, r := range b.Refs {
		fmt.Fprintf(&w, "- %s: %s\n", r.Kind, relpath(r.Path))
	}
	w.WriteByte('\n')

	writeArtefact(&w, "Task", b.Seed)
	if b.Story != nil {
		writeArtefact(&w, "User Story", b.Story)
	}
	if b.Epic != nil {
		writeArtefact(&w, "Epic Brief", b.Epic)
	}
	if b.Sprint != nil {
		writeArtefact(&w, "Sprint", b.Sprint)
	}
	for _, r := range b.Refs {
		fmt.Fprintf(&w, "---\n\n## %s reference — %s\n\n", r.Kind, relpath(r.Path))
		w.WriteString(strings.TrimRight(r.Body, "\n"))
		w.WriteString("\n\n")
	}

	return w.String()
}

func writeArtefact(w *strings.Builder, label string, a *Artefact) {
	w.WriteString("---\n\n")
	fmt.Fprintf(w, "## %s — %s (%s)\n\n", label, a.Frontmatter.ID, a.Frontmatter.Title)
	fmt.Fprintf(w, "**Source:** `%s`  \n", relpath(a.Path))
	fmt.Fprintf(w, "**Status:** %s  \n", a.Frontmatter.Status)
	if a.Frontmatter.AssignedTo != "" {
		fmt.Fprintf(w, "**Assigned to:** %s  \n", a.Frontmatter.AssignedTo)
	}
	w.WriteByte('\n')
	w.WriteString(a.Body)
	w.WriteString("\n\n")
}

// relpath shortens absolute paths by stripping a `lightwave-media/`
// prefix when present, so rendered bundles stay readable.
func relpath(p string) string {
	if idx := strings.LastIndex(p, "/lightwave-media/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}
