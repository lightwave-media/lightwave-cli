package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lightwave-media/lightwave-cli/internal/config"
	"gopkg.in/yaml.v3"
)

// SprintSpec represents a sprint specification YAML file
type SprintSpec struct {
	Sprint struct {
		ID     string `yaml:"id"`
		Name   string `yaml:"name"`
		Number int    `yaml:"number"`
		Status string `yaml:"status"`
	} `yaml:"sprint"`
	Epic struct {
		ID   string `yaml:"id"`
		Name string `yaml:"name"`
	} `yaml:"epic"`
	Stories []struct {
		ID   string `yaml:"id"`
		Name string `yaml:"name"`
	} `yaml:"stories"`
	Dependencies []struct {
		SprintID string `yaml:"sprint_id"`
		Name     string `yaml:"name"`
	} `yaml:"dependencies"`
	Objective string `yaml:"objective"`
	Rationale string `yaml:"rationale"`
	Tasks     []struct {
		ID       string   `yaml:"id"`
		Name     string   `yaml:"name"`
		Type     string   `yaml:"type"`
		Priority string   `yaml:"priority"`
		Status   string   `yaml:"status"`
		Story    string   `yaml:"story"`
		Files    []string `yaml:"files"`
		AC       string   `yaml:"ac"`
	} `yaml:"tasks"`
	AcceptanceCriteria []string `yaml:"acceptance_criteria"`
	AntiSlop           []string `yaml:"anti_slop"`
	ResearchHints      []string `yaml:"research_hints"`
	Verification       string   `yaml:"verification"`
}

// FindSprintSpec looks for a sprint spec YAML by sprint short ID in the queue directories
func FindSprintSpec(sprintShortID string) (string, *SprintSpec, error) {
	cfg := config.Get()
	queueRoot := filepath.Join(cfg.Paths.LightwaveRoot, ".claude", "queue")

	type match struct {
		path string
		spec *SprintSpec
	}
	var matches []match

	// Search draft, pending, active directories
	for _, dir := range []string{"draft", "pending", "active"} {
		dirPath := filepath.Join(queueRoot, dir)
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			path := filepath.Join(dirPath, entry.Name())
			spec, err := parseSprintSpec(path)
			if err != nil {
				continue
			}
			if strings.HasPrefix(spec.Sprint.ID, sprintShortID) {
				matches = append(matches, match{path, spec})
			}
		}
	}

	if len(matches) == 0 {
		return "", nil, fmt.Errorf("no sprint spec found for ID %s in .claude/queue/{draft,pending,active}/", sprintShortID)
	}
	if len(matches) > 1 {
		return "", nil, fmt.Errorf("ambiguous sprint ID '%s' matches %d spec files — use more characters", sprintShortID, len(matches))
	}
	return matches[0].path, matches[0].spec, nil
}

func parseSprintSpec(path string) (*SprintSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spec SprintSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

// MoveSpec moves a spec file between queue directories (e.g., draft → active)
func MoveSpec(srcPath, destDir string) (string, error) {
	cfg := config.Get()
	destDirPath := filepath.Join(cfg.Paths.LightwaveRoot, ".claude", "queue", destDir)

	if err := os.MkdirAll(destDirPath, 0o755); err != nil {
		return "", fmt.Errorf("failed to create %s directory: %w", destDir, err)
	}

	destPath := filepath.Join(destDirPath, filepath.Base(srcPath))
	if err := os.Rename(srcPath, destPath); err != nil {
		return "", fmt.Errorf("failed to move spec: %w", err)
	}
	return destPath, nil
}

// GeneratePrompt builds a Claude Code session prompt from a sprint spec
func GeneratePrompt(spec *SprintSpec) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# Sprint %d: %s\n\n", spec.Sprint.Number, spec.Sprint.Name))
	b.WriteString(fmt.Sprintf("**Sprint ID:** %s\n", spec.Sprint.ID))
	b.WriteString(fmt.Sprintf("**Epic:** %s (%s)\n\n", spec.Epic.Name, spec.Epic.ID))

	// Objective
	b.WriteString("## Objective\n\n")
	b.WriteString(strings.TrimSpace(spec.Objective) + "\n\n")

	// Stories
	if len(spec.Stories) > 0 {
		b.WriteString("## Stories\n\n")
		for _, s := range spec.Stories {
			b.WriteString(fmt.Sprintf("- %s (`%s`)\n", s.Name, s.ID))
		}
		b.WriteString("\n")
	}

	// Tasks
	b.WriteString("## Tasks\n\n")
	b.WriteString("| # | Task | Type | Priority | AC |\n")
	b.WriteString("|---|------|------|----------|----|\n")
	for i, t := range spec.Tasks {
		if t.Status == "done" {
			continue // Skip completed tasks
		}
		b.WriteString(fmt.Sprintf("| %d | %s (`%s`) | %s | %s | %s |\n",
			i+1, t.Name, t.ID, t.Type, t.Priority, t.AC))
	}
	b.WriteString("\n")

	// Files to research
	allFiles := map[string]bool{}
	for _, t := range spec.Tasks {
		for _, f := range t.Files {
			allFiles[f] = true
		}
	}
	if len(allFiles) > 0 {
		b.WriteString("## Key Files\n\n")
		for f := range allFiles {
			b.WriteString(fmt.Sprintf("- `%s`\n", f))
		}
		b.WriteString("\n")
	}

	// Acceptance Criteria
	if len(spec.AcceptanceCriteria) > 0 {
		b.WriteString("## Acceptance Criteria\n\n")
		for _, ac := range spec.AcceptanceCriteria {
			b.WriteString(fmt.Sprintf("- [ ] %s\n", ac))
		}
		b.WriteString("\n")
	}

	// Anti-slop
	if len(spec.AntiSlop) > 0 {
		b.WriteString("## Anti-Slop Rules\n\n")
		for _, rule := range spec.AntiSlop {
			b.WriteString(fmt.Sprintf("- %s\n", rule))
		}
		b.WriteString("\n")
	}

	// Research hints
	if len(spec.ResearchHints) > 0 {
		b.WriteString("## Codebase Research Hints\n\n")
		for _, hint := range spec.ResearchHints {
			b.WriteString(fmt.Sprintf("- %s\n", hint))
		}
		b.WriteString("\n")
	}

	// Verification
	if spec.Verification != "" {
		b.WriteString("## Verification\n\n")
		b.WriteString(strings.TrimSpace(spec.Verification) + "\n\n")
	}

	// Instructions
	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Read the key files listed above before making changes\n")
	b.WriteString("2. Work through tasks in priority order (P1 first)\n")
	b.WriteString("3. After each task, run relevant tests to verify\n")
	b.WriteString("4. Follow the anti-slop rules — do not over-engineer\n")
	b.WriteString("5. Mark tasks as done via `lw task update <id> --status=done` when complete\n")

	return b.String()
}
