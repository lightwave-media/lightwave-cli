package docsfactory

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the parsed result of `--- yaml ---\n<body>` heading on a
// markdown file. Empty Raw + non-nil Map = no frontmatter present.
type Frontmatter struct {
	Map  map[string]any
	Body string
	Raw  string // the original frontmatter bytes (between the two `---`)
}

var fmDelimiter = []byte("---\n")

// ParseFrontmatter splits a markdown-with-frontmatter file into the YAML map
// and the body. If the file has no `---` delimiter at the very start, returns
// (empty-map, full-content, "", nil) — convention used by lint/check verbs
// to distinguish "missing" from "malformed".
func ParseFrontmatter(content []byte) (*Frontmatter, error) {
	if !bytes.HasPrefix(content, fmDelimiter) {
		return &Frontmatter{Map: map[string]any{}, Body: string(content)}, nil
	}
	rest := content[len(fmDelimiter):]
	end := bytes.Index(rest, fmDelimiter)
	if end < 0 {
		return nil, fmt.Errorf("unterminated frontmatter: missing closing ---")
	}
	raw := rest[:end]
	body := string(rest[end+len(fmDelimiter):])

	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse frontmatter yaml: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return &Frontmatter{Map: m, Body: body, Raw: string(raw)}, nil
}

// ReadFrontmatter reads + parses a file in one step.
func ReadFrontmatter(path string) (*Frontmatter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseFrontmatter(data)
}

// Render emits the frontmatter back as `--- yaml ---\n<body>`. Keys are
// sorted alphabetically — determinism rule from doc_artifact_kinds.yaml.
// Empty Map renders no frontmatter at all (just body), matching ParseFrontmatter.
func (f *Frontmatter) Render() ([]byte, error) {
	if len(f.Map) == 0 {
		return []byte(f.Body), nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f.Map); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := bytes.Buffer{}
	out.Write(fmDelimiter)
	out.Write(buf.Bytes())
	out.Write(fmDelimiter)
	out.WriteString(f.Body)
	return out.Bytes(), nil
}

// HeaderCommentMap parses `# key: value` lines from the head of a YAML/JSON/
// mermaid file — the "header comments" frontmatter substitute for kinds that
// don't have native frontmatter. Stops at the first non-comment line.
//
// Recognized prefixes: `# ` (yaml/python), `// ` (TS/Go — unused here),
// `%% ` (mermaid), `  "_<key>"` (JSON underscore convention). The JSON case
// is handled by ParseHeaderCommentJSON.
func HeaderCommentMap(content []byte) map[string]string {
	m := map[string]string{}
	for line := range strings.SplitSeq(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		var rest string
		var ok bool
		switch {
		case strings.HasPrefix(trimmed, "# "):
			rest, ok = strings.CutPrefix(trimmed, "# ")
		case strings.HasPrefix(trimmed, "%% "):
			rest, ok = strings.CutPrefix(trimmed, "%% ")
		default:
			// First non-comment line ends the header block.
			if trimmed == "" {
				continue
			}
			return m
		}
		if !ok {
			continue
		}
		if idx := strings.Index(rest, ":"); idx > 0 {
			k := strings.TrimSpace(rest[:idx])
			v := strings.TrimSpace(rest[idx+1:])
			m[k] = v
		}
	}
	return m
}

// ParseHeaderCommentJSON parses the leading `"_kind"`, `"_source_commit"`,
// etc. fields from a JSON file. We treat any top-level key whose name starts
// with `_` as a header-comment field. The lightwave determinism convention
// puts these first; we accept them anywhere at the top level.
func ParseHeaderCommentJSON(content []byte) (map[string]string, error) {
	// Use yaml decoder since YAML is a JSON superset and we want flexible
	// types. Reject if the parse fails entirely.
	var raw map[string]any
	if err := yaml.Unmarshal(content, &raw); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range raw {
		if !strings.HasPrefix(k, "_") {
			continue
		}
		out[strings.TrimPrefix(k, "_")] = fmt.Sprintf("%v", v)
	}
	return out, nil
}

// SectionHeadings extracts level-2 markdown headings ("## Title") from a body.
// Used to check `required_sections` against the actual document. Returns
// headings in document order, with leading "## " stripped.
func SectionHeadings(body string) []string {
	var out []string
	for line := range strings.SplitSeq(body, "\n") {
		if rest, ok := strings.CutPrefix(line, "## "); ok {
			out = append(out, strings.TrimSpace(rest))
		}
	}
	return out
}

// MissingSections returns the elements of required that are not present in
// got. Comparison is case-sensitive (intentional — the schema is the spec).
func MissingSections(required, got []string) []string {
	gotSet := map[string]bool{}
	for _, g := range got {
		gotSet[g] = true
	}
	var missing []string
	for _, r := range required {
		if !gotSet[r] {
			missing = append(missing, r)
		}
	}
	return missing
}

// MissingFrontmatterKeys returns required keys not present in the parsed map.
// Empty values count as missing per the lint convention (presence-only is
// always a smell).
func MissingFrontmatterKeys(required []string, fm map[string]any) []string {
	var missing []string
	for _, k := range required {
		v, ok := fm[k]
		if !ok {
			missing = append(missing, k)
			continue
		}
		if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
			missing = append(missing, k)
		}
	}
	return missing
}

// kindFromFrontmatter returns the `kind:` value from a parsed frontmatter
// map, normalizing common type wobble.
func kindFromFrontmatter(fm map[string]any) string {
	v, ok := fm["kind"]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}
