package runbook

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	// Path is a Check/Command script, or a Template's blueprint directory,
	// relative to the runbook's own dir. Target is where a Template renders.
	Path   string
	Target string
	// Expected is a word a Check's output must contain. The catalog writes
	// `test -f x && echo found || echo missing`, which exits 0 either way.
	Expected string
	// Timeout bounds the step (timeoutMs="{1800000}"); zero means unbounded.
	Timeout time.Duration
	// HighBlast steps wait for operator sign-off: every Command and Template,
	// and a Check whose inline command needs a shell or hands a string to an
	// interpreter (lightwave-cli#348).
	HighBlast bool
}

// Edition is a published runbook.mdx plus its content hash.
type Edition struct {
	Slug   string
	Dir    string
	Path   string
	Hash   string
	Steps  []Step
	Inputs []InputDecl
}

var (
	// A quoted value may carry \" escapes (command="psql -c \"SELECT ...\""),
	// so neither a block nor a value may end at an escaped quote. The old
	// `"([^"]*)"` cut those commands off mid-string.
	blockRe = regexp.MustCompile(`(?s)<(Check|Command|Template)\s((?:[^>"]|"(?:[^"\\]|\\.)*")*?)\s*/>`)
	attrRe  = regexp.MustCompile(`(?s)(\w+)\s*=\s*(?:"((?:[^"\\]|\\.)*)"|\{([^}]*)\})`)

	unescapeAttr = strings.NewReplacer(`\"`, `"`, `\\`, `\`)
)

// LoadEdition reads runbook.mdx for an index entry. Missing file is an
// edition mismatch (phantom index row), not a license to invent steps.
func LoadEdition(coreRepo string, entry Entry) (*Edition, error) {
	path := filepath.Join(RunbooksDir(coreRepo), entry.Dir, runbookMDX)

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrEditionMismatch, path, err)
	}

	inputs, err := parseInputs(string(raw), filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("runbook %s: %w", entry.Slug, err)
	}

	sum := sha256.Sum256(raw)

	return &Edition{
		Slug:   entry.Slug,
		Dir:    entry.Dir,
		Path:   path,
		Hash:   hex.EncodeToString(sum[:]),
		Steps:  ParseSteps(string(raw)),
		Inputs: inputs,
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
			Path:        attrs["path"],
			Target:      attrs["target"],
			Expected:    attrs["expected"],
			Timeout:     parseTimeout(attrs["timeoutMs"]),
			HighBlast:   kind != KindCheck || needsShell(attrs["command"]) || runsCode(attrs["command"]),
		})
	}

	return steps
}

// parseAttrs reads name="value" (with \" escapes) and name={expression}.
func parseAttrs(body string) map[string]string {
	out := map[string]string{}

	for _, m := range attrRe.FindAllStringSubmatchIndex(body, -1) {
		name := body[m[2]:m[3]]
		if m[4] >= 0 { // name="value"
			out[name] = unescapeAttr.Replace(body[m[4]:m[5]])

			continue
		}

		out[name] = strings.TrimSpace(body[m[6]:m[7]]) // name={expression}
	}

	return out
}

func parseTimeout(ms string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(ms))
	if err != nil || n <= 0 {
		return 0
	}

	return time.Duration(n) * time.Millisecond
}
