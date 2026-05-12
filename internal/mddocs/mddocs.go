// Package mddocs reads markdown-canonical LightWave artefacts (tasks,
// user stories, epic briefs, sprints, DDDs) from `lightwave-media/docs/`.
//
// Artefacts are markdown files with a YAML frontmatter block delimited by
// `---` fences. Frontmatter schema is defined in
// `~/.brain/logic/documentation-workflow.md` §4.
//
// This package is the read surface for `lw task fetch-context` and the
// future `lw task new|edit|index` commands. It is intentionally stdlib +
// gopkg.in/yaml.v3 only — no DB, no network.
package mddocs

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind identifies an artefact type. Maps 1:1 to the directory it lives
// in under `lightwave-media/docs/<domain>/<dir>/`.
type Kind string

const (
	KindTask      Kind = "task"
	KindUserStory Kind = "user-story"
	KindEpicBrief Kind = "epic-brief"
	KindSprint    Kind = "sprint"
	KindDDD       Kind = "ddd"
	KindIP        Kind = "implementation-plan"
)

// DirFor returns the docs subdirectory used for this kind.
func (k Kind) DirFor() string {
	switch k {
	case KindTask:
		return "tasks"
	case KindUserStory:
		return "user-stories"
	case KindEpicBrief:
		return "epic-briefs"
	case KindSprint:
		return "sprints"
	case KindDDD:
		return "ddds"
	case KindIP:
		return "implementation-plans"
	}
	return ""
}

// KindFromID returns the artefact kind implied by the ID prefix.
// `T-0001` → KindTask, `US-001` → KindUserStory, etc.
// Returns ("", false) if the prefix is unrecognised.
func KindFromID(id string) (Kind, bool) {
	switch {
	case strings.HasPrefix(id, "T-"):
		return KindTask, true
	case strings.HasPrefix(id, "US-"):
		return KindUserStory, true
	case strings.HasPrefix(id, "EB-"):
		return KindEpicBrief, true
	case strings.HasPrefix(id, "SPR-"):
		return KindSprint, true
	case strings.HasPrefix(id, "DDD-"):
		return KindDDD, true
	case strings.HasPrefix(id, "IP-"):
		return KindIP, true
	}
	return "", false
}

// Frontmatter is the structured header of every artefact. Fields mirror
// `documentation-workflow.md` §4. Unknown YAML keys are preserved in Extra
// so callers can read kind-specific fields (priority, story_points, etc.)
// without this struct growing unboundedly.
type Frontmatter struct {
	ID             string `yaml:"id"`
	Domain         string `yaml:"domain"`
	Type           string `yaml:"type"`
	Title          string `yaml:"title"`
	Status         string `yaml:"status"`
	Owner          string `yaml:"owner"`
	CreatedBy      string `yaml:"created_by"`
	AssignedTo     string `yaml:"assigned_to"`
	CreatedAt      string `yaml:"created_at"`
	UpdatedAt      string `yaml:"updated_at"`
	ParentEpic     string `yaml:"parent_epic"`
	ParentStory    string `yaml:"parent_story"`
	AssignedSprint string `yaml:"assigned_sprint"`
	RefsSAD        string `yaml:"refs_sad"`
	RefsNFRs       string `yaml:"refs_nfrs"`
	RefsDDD        string `yaml:"refs_ddd"`
	RefsPRD        string `yaml:"refs_prd"`
	RefsNaming     string `yaml:"refs_naming"`

	Extra map[string]any `yaml:",inline"`
}

// Artefact is a parsed markdown file: frontmatter + body. Path is the
// absolute path on disk (so callers can resolve relative refs).
type Artefact struct {
	Path        string
	Frontmatter Frontmatter
	Body        string
}

// DocsRoot returns the canonical docs directory for the given lightwave
// monorepo root (typically `cfg.Paths.LightwaveRoot`).
func DocsRoot(lightwaveRoot string) string {
	return filepath.Join(lightwaveRoot, "docs")
}

// Parse reads a markdown artefact from disk: frontmatter (YAML between
// `---` fences) followed by body. Returns an error if the file does not
// open with a `---` fence — every LightWave artefact MUST carry frontmatter.
func Parse(path string) (*Artefact, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if !scanner.Scan() {
		return nil, fmt.Errorf("%s: empty file", path)
	}
	if strings.TrimSpace(scanner.Text()) != "---" {
		return nil, fmt.Errorf("%s: missing frontmatter (first line is not '---')", path)
	}

	var fm strings.Builder
	closed := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			closed = true
			break
		}
		fm.WriteString(line)
		fm.WriteByte('\n')
	}
	if !closed {
		return nil, fmt.Errorf("%s: unterminated frontmatter (no closing '---')", path)
	}

	var body strings.Builder
	for scanner.Scan() {
		body.WriteString(scanner.Text())
		body.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	var front Frontmatter
	if err := yaml.Unmarshal([]byte(fm.String()), &front); err != nil {
		return nil, fmt.Errorf("%s: parse frontmatter: %w", path, err)
	}

	return &Artefact{
		Path:        path,
		Frontmatter: front,
		Body:        strings.TrimRight(body.String(), "\n"),
	}, nil
}
