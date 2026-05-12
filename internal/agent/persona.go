package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PersonaNotFoundError is returned when the requested persona has no
// resolvable system-prompt source. Callers can offer remediation hints
// (e.g. "stub it with `lw agent persona stub <name>`" — TODO US-002).
type PersonaNotFoundError struct {
	Name       string
	SearchedIn []string
}

func (e *PersonaNotFoundError) Error() string {
	return fmt.Sprintf("persona %q not found (searched %v)", e.Name, e.SearchedIn)
}

// LoadPersonaPrompt resolves the persona name to a system-prompt body
// suitable for passing to `claude -p` / `pi`.
//
// Resolution order:
//  1. Override file: $LW_PERSONA_DIR/<name>.yaml (when env var set)
//  2. Brain authority: ~/.brain/cortex/agents/createOS-domains/software/<name>.yaml
//
// The whole YAML body is returned verbatim — Claude/pi accept structured
// YAML as a system prompt and the engineering persona files at the brain
// path are authored exactly for this role (cortex_session shape). Going
// further (rendering only specific fields) is YAGNI until v_core's
// enforcement layer needs it.
//
// Per the canonical spec (`v_core.yaml`), v_core dispatches the 8
// engineering personas: platform-engineer, frontend-engineer,
// infrastructure-engineer, qa-engineer, compliance, triager,
// research-analyst, brain. `compliance` and `triager` are not yet in the
// brain — they return PersonaNotFoundError until stubs land (US-002 in
// the lightwave-sys session).
func LoadPersonaPrompt(name string) (string, string, error) {
	if name == "" {
		return "", "", errors.New("persona name is required")
	}

	var searched []string

	if override := os.Getenv("LW_PERSONA_DIR"); override != "" {
		path := filepath.Join(override, name+".yaml")
		searched = append(searched, path)
		if body, err := os.ReadFile(path); err == nil {
			return string(body), path, nil
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	brainPath := filepath.Join(home, ".brain", "cortex", "agents",
		"createOS-domains", "software", name+".yaml")
	searched = append(searched, brainPath)
	if body, err := os.ReadFile(brainPath); err == nil {
		return string(body), brainPath, nil
	}

	return "", "", &PersonaNotFoundError{Name: name, SearchedIn: searched}
}
