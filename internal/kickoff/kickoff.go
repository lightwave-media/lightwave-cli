// Package kickoff implements the kickoff interview gate for .tasks/<session>/kickoff/.
package kickoff

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	fileMode = 0o644
	dirMode  = 0o755
)

// TasksRoot returns .tasks under repo or cwd.
func TasksRoot(repoRoot string) string {
	return filepath.Join(repoRoot, ".tasks")
}

// KickoffDir returns .tasks/<session>/kickoff.
func KickoffDir(repoRoot, sessionID string) string {
	return filepath.Join(TasksRoot(repoRoot), sessionID, "kickoff")
}

// GatePath returns kickoff.json path.
func GatePath(repoRoot, sessionID string) string {
	return filepath.Join(KickoffDir(repoRoot, sessionID), "kickoff.json")
}

// Gate holds kickoff.json shape.
type Gate struct {
	RunbookSlug   string `json:"runbook_slug"`
	Round         string `json:"round"`
	Status        string `json:"status"`
	ApprovedAt    string `json:"approved_at,omitempty"`
	ManifestSHA   string `json:"manifest_sha256,omitempty"`
	ShelvedReason string `json:"shelved_reason,omitempty"`
	KickoffOK     bool   `json:"kickoff_ok"`
}

// ReadGate loads kickoff.json.
func ReadGate(repoRoot, sessionID string) (*Gate, error) {
	path := GatePath(repoRoot, sessionID)

	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var g Gate
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, err
	}

	return &g, nil
}

// WriteGate writes kickoff.json.
func WriteGate(repoRoot, sessionID string, g *Gate) error {
	dir := KickoffDir(repoRoot, sessionID)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}

	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}

	b = append(b, '\n')

	return os.WriteFile(GatePath(repoRoot, sessionID), b, fileMode)
}

// Start creates kickoff artifacts for a new session.
func Start(repoRoot, sessionID, runbookSlug, operator string) error {
	dir := KickoffDir(repoRoot, sessionID)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}

	transcript := filepath.Join(dir, "interview_transcript.yaml")
	if _, err := os.Stat(transcript); os.IsNotExist(err) {
		body := fmt.Sprintf(`runbook_slug: %s
session_id: %s
operator_handle: %s
current_round: initial
status: interview
turns: []
scaffold_manifest_ref: scaffold_manifest.yaml
`, runbookSlug, sessionID, operator)
		if err := os.WriteFile(transcript, []byte(body), fileMode); err != nil {
			return err
		}
	}

	manifest := filepath.Join(dir, "scaffold_manifest.yaml")
	if _, err := os.Stat(manifest); os.IsNotExist(err) {
		m := map[string]any{
			"_meta": map[string]any{
				"initiative":            sessionID,
				"kickoff_interview_ref": "interview_transcript.yaml",
			},
			"steps": []any{},
		}

		b, _ := yaml.Marshal(m)
		if err := os.WriteFile(manifest, b, fileMode); err != nil {
			return err
		}
	}

	return WriteGate(repoRoot, sessionID, &Gate{
		KickoffOK:   false,
		RunbookSlug: runbookSlug,
		Round:       "initial",
		Status:      "interview",
	})
}

// Finalize sets kickoff_ok after approval.
func Finalize(repoRoot, sessionID string) error {
	g, err := ReadGate(repoRoot, sessionID)
	if err != nil {
		return err
	}

	g.KickoffOK = true
	g.Round = "final"
	g.Status = "approved"
	g.ApprovedAt = time.Now().UTC().Format(time.RFC3339)

	return WriteGate(repoRoot, sessionID, g)
}

// RequireFinalized errors if kickoff_ok is false.
func RequireFinalized(repoRoot, sessionID string) error {
	g, err := ReadGate(repoRoot, sessionID)
	if err != nil {
		return fmt.Errorf("kickoff gate missing: %w", err)
	}

	if !g.KickoffOK {
		return fmt.Errorf("kickoff not finalized (kickoff_ok=false) for session %s", sessionID)
	}

	return nil
}
