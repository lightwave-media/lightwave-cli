// Package docsgate emits structured cure payloads for the docs commit gate.
package docsgate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Failure is the docs_gate_failure shape written on pre-commit failure.
type Failure struct {
	Code        string `json:"code"`
	Summary     string `json:"summary"`
	CureCommand string `json:"cure_command"`
	DetectedAt  string `json:"detected_at"`
	Session     string `json:"session,omitempty"`
}

// Emit writes a cure JSON file under ~/.lightwave/observability/docs-gate/.
func Emit(code, summary, cure string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".lightwave", "observability", "docs-gate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f := Failure{
		Code:        code,
		Summary:     summary,
		CureCommand: cure,
		DetectedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}
	b = append(b, '\n')
	name := fmt.Sprintf("%d.json", time.Now().UnixNano())
	path := filepath.Join(dir, name)
	return path, os.WriteFile(path, b, 0o644)
}
