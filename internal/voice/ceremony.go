package voice

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Turn struct {
	Speaker   string `yaml:"speaker"`
	Text      string `yaml:"text"`
	Timestamp string `yaml:"timestamp"`
}

type CeremonySession struct {
	SessionID           string   `yaml:"session_id"`
	Kind                string   `yaml:"kind"`
	OperatorHandle      string   `yaml:"operator_handle"`
	Status              string   `yaml:"status"`
	CoachingFocus       string   `yaml:"coaching_focus,omitempty"`
	IdealStateRef       string   `yaml:"ideal_state_ref,omitempty"`
	DeltaRef            string   `yaml:"delta_ref,omitempty"`
	ParticipantPersonas []string `yaml:"participant_personas,omitempty"`
	Turns               []Turn   `yaml:"turns"`
}

func CeremonyDir(repo, sessionID string) string {
	return filepath.Join(repo, ".tasks", sessionID, "voice")
}

func CeremonyPath(repo, sessionID string) string {
	return filepath.Join(CeremonyDir(repo, sessionID), "ceremony.yaml")
}

func LoadCeremony(repo, sessionID string) (*CeremonySession, error) {
	data, err := os.ReadFile(CeremonyPath(repo, sessionID))
	if err != nil {
		return nil, fmt.Errorf("read ceremony: %w", err)
	}

	var s CeremonySession
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse ceremony: %w", err)
	}

	return &s, nil
}

func SaveCeremony(repo string, s *CeremonySession) error {
	dir := CeremonyDir(repo, s.SessionID)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}

	return os.WriteFile(CeremonyPath(repo, s.SessionID), b, filePerm)
}

func StartCeremony(repo, sessionID, kind, idealState, focus string) (*CeremonySession, error) {
	if sessionID == "" {
		sessionID = time.Now().Format("2006-01-02") + "-voice"
	}

	if kind == "" {
		kind = "guru"
	}

	s := &CeremonySession{
		SessionID:      sessionID,
		Kind:           kind,
		OperatorHandle: "operator",
		Status:         "active",
		CoachingFocus:  focus,
		IdealStateRef:  idealState,
		DeltaRef:       "spec/delta/report.yaml",
		Turns:          []Turn{},
	}
	if err := SaveCeremony(repo, s); err != nil {
		return nil, err
	}

	return s, nil
}

func AppendTurn(repo, sessionID, speaker, text string) (*CeremonySession, error) {
	s, err := LoadCeremony(repo, sessionID)
	if err != nil {
		return nil, err
	}

	s.Turns = append(s.Turns, Turn{
		Speaker:   speaker,
		Text:      text,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if err := SaveCeremony(repo, s); err != nil {
		return nil, err
	}

	return s, nil
}

func EndCeremony(repo, sessionID string) (*CeremonySession, error) {
	s, err := LoadCeremony(repo, sessionID)
	if err != nil {
		return nil, err
	}

	s.Status = "completed"
	if err := SaveCeremony(repo, s); err != nil {
		return nil, err
	}

	return s, nil
}
