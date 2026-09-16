package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// personaPrefix is the naming convention of the live persona prints
// (v_platform-developer.yaml, v_qa-engineer.yaml, …). Callers may pass
// the bare name; resolution retries with the prefix.
const personaPrefix = "v_"

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

// LightwaveHome returns the root of the ~/.lightwave print tree.
//
// When $BRAIN is set (settings.json exports BRAIN=~/.lightwave/brain to
// every session) its parent directory wins, so a relocated brain carries
// every sibling print with it. Otherwise ~/.lightwave.
func LightwaveHome() (string, error) {
	if brain := os.Getenv("BRAIN"); brain != "" {
		return filepath.Dir(brain), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".lightwave"), nil
}

// LoadPersonaPrompt resolves the persona name to a system-prompt body
// suitable for passing to `claude -p` / `pi`.
//
// Resolution order:
//  1. Override file: $LW_PERSONA_DIR/<name>.yaml (when env var set)
//  2. Persona print: <LightwaveHome>/config/agents/<name>.yaml, then
//     v_<name>.yaml when the name lacks the v_ prefix (EB-007 moved the
//     agent prints from ~/.brain/cortex/... to ~/.lightwave/config/agents/).
//
// The whole YAML body is returned verbatim — Claude/pi accept structured
// YAML as a system prompt and the persona files are authored exactly for
// this role. Going further (rendering only specific fields) is YAGNI
// until v_core's enforcement layer needs it.
//
// Per the canonical spec (`v_core.yaml`), v_core dispatches the 8
// engineering personas: platform-engineer, frontend-engineer,
// infrastructure-engineer, qa-engineer, compliance, triager,
// research-analyst, brain. `compliance` and `triager` have no print yet —
// they return PersonaNotFoundError until stubs land (US-002 in the
// lightwave-sys session).
func LoadPersonaPrompt(name string) (string, string, error) {
	if name == "" {
		return "", "", errors.New("persona name is required")
	}

	candidates, err := personaCandidatePaths(name)
	if err != nil {
		return "", "", err
	}

	for _, path := range candidates {
		if body, err := os.ReadFile(path); err == nil {
			return string(body), path, nil
		}
	}

	return "", "", &PersonaNotFoundError{Name: name, SearchedIn: candidates}
}

// personaCandidatePaths lists every file LoadPersonaPrompt tries, in
// resolution order. The same list is what PersonaNotFoundError reports.
func personaCandidatePaths(name string) ([]string, error) {
	var paths []string

	if override := os.Getenv("LW_PERSONA_DIR"); override != "" {
		paths = append(paths, filepath.Join(override, name+".yaml"))
	}

	root, err := LightwaveHome()
	if err != nil {
		return nil, err
	}

	agentsDir := filepath.Join(root, "config", "agents")
	paths = append(paths, filepath.Join(agentsDir, name+".yaml"))

	if !strings.HasPrefix(name, personaPrefix) {
		paths = append(paths, filepath.Join(agentsDir, personaPrefix+name+".yaml"))
	}

	return paths, nil
}
