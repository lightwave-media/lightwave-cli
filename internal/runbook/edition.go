package runbook

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// StepKind is the MDX block type. Command and Template are high-blast-radius.
const (
	KindCheck    = "check"
	KindCommand  = "command"
	KindTemplate = "template"
)

// Step is one Check/Command/Template from the published edition.
type Step struct {
	ID          string
	Kind        string
	Description string
	Command     string
	HighBlast   bool
}

// Edition is a published runbook.mdx plus its content hash.
type Edition struct {
	Slug  string
	Dir   string
	Path  string
	Hash  string
	Steps []Step
}

var (
	blockRe = regexp.MustCompile(`(?s)<(Check|Command|Template)\s([^>]*?)\s*/>`)
	attrRe  = regexp.MustCompile(`(?s)(id|description|command)\s*=\s*"([^"]*)"`)
)

// LoadEdition reads runbook.mdx for an index entry. Missing file is an
// edition mismatch (phantom index row), not a license to invent steps.
func LoadEdition(coreRepo string, entry Entry) (*Edition, error) {
	path := filepath.Join(RunbooksDir(coreRepo), entry.Dir, runbookMDX)

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrEditionMismatch, path, err)
	}

	sum := sha256.Sum256(raw)

	return &Edition{
		Slug:  entry.Slug,
		Dir:   entry.Dir,
		Path:  path,
		Hash:  hex.EncodeToString(sum[:]),
		Steps: ParseSteps(string(raw)),
	}, nil
}

// ParseSteps extracts Check/Command/Template blocks from MDX.
func ParseSteps(mdx string) []Step {
	matches := blockRe.FindAllStringSubmatch(mdx, -1)
	if len(matches) == 0 {
		return nil
	}

	steps := make([]Step, 0, len(matches))
	for _, m := range matches {
		kind := strings.ToLower(m[1])
		attrs := parseAttrs(m[2])

		id := attrs["id"]
		if id == "" {
			continue
		}

		steps = append(steps, Step{
			ID:          id,
			Kind:        kind,
			Description: attrs["description"],
			Command:     attrs["command"],
			HighBlast:   kind == KindCommand || kind == KindTemplate,
		})
	}

	return steps
}

func parseAttrs(body string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(body, -1) {
		out[m[1]] = m[2]
	}

	return out
}
