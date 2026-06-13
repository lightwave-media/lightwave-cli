package docsfactory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// HandoffDoc is the lint-relevant view of an agent_handoff instance (the
// data shape stamped at lightwave-core
// src/schemas/data/reference_documents/agent_handoff.yaml v1.2.0). Fields the
// step-contract validator does not inspect are intentionally omitted.
type HandoffDoc struct {
	ID       string           `yaml:"id"`
	Status   string           `yaml:"status"`
	Requests []HandoffRequest `yaml:"requests"`
	Steps    []HandoffStep    `yaml:"steps"`
}

// HandoffRequest is the negotiation-layer ask; lint only needs request_id so a
// step's request_ref can be resolved against it.
type HandoffRequest struct {
	RequestID string `yaml:"request_id"`
}

// HandoffStep is one entry of the v1.2.0 execution layer. Success/Failure/
// RequestRef are *string so the validator can distinguish OMITTED (nil) from
// PRESENT-but-blank ("") — the per-kind success/failure contract turns on that.
type HandoffStep struct {
	StepID     string   `yaml:"step_id"`
	Kind       string   `yaml:"kind"`
	Title      string   `yaml:"title"`
	Body       string   `yaml:"body"`
	DependsOn  []string `yaml:"depends_on"`
	Consumes   []string `yaml:"consumes"`
	Produces   []string `yaml:"produces"`
	Success    *string  `yaml:"success"`
	Failure    *string  `yaml:"failure"`
	RequestRef *string  `yaml:"request_ref"`
}

// errUnsupportedHandoffFormat is returned for inputs the slice-1 loader can't
// parse yet (the .handoff.md print). Failing loudly is deliberate — silent
// skip is the slop mode these verbs exist to prevent.
var errUnsupportedHandoffFormat = fmt.Errorf(
	".handoff.md parsing is not yet supported (slice 2); pass the agent_handoff YAML data shape")

// LoadHandoff reads a handoff instance from disk, dispatching on extension.
// .yaml/.yml → the structured data shape. .handoff.md (the rendered print) is
// a deferred follow-up and returns errUnsupportedHandoffFormat.
func LoadHandoff(path string) (*HandoffDoc, error) {
	if strings.HasSuffix(path, ".handoff.md") {
		return nil, errUnsupportedHandoffFormat
	}
	switch filepath.Ext(path) {
	case ".yaml", ".yml":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return ParseHandoffYAML(data)
	default:
		return nil, fmt.Errorf("unsupported handoff file extension %q (want .yaml/.yml)", filepath.Ext(path))
	}
}

// ParseHandoffYAML unmarshals a handoff from YAML. It is example-aware: if the
// document has a top-level `example:` key (i.e. the agent_handoff.yaml schema
// file itself), the example block is parsed as the handoff — so the canonical
// golden instance can be linted directly with
// `lw lint handoff .../agent_handoff.yaml`. Otherwise the whole document is the
// handoff.
func ParseHandoffYAML(data []byte) (*HandoffDoc, error) {
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var doc HandoffDoc
	if node, ok := raw["example"]; ok {
		if err := node.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode example: %w", err)
		}
		return &doc, nil
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}
