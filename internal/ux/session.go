package ux

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	StatusRecording = "recording"
	StatusStopped   = "stopped"
	StatusAnalyzed  = "analyzed"
)

// Session represents a UX recording session.
type Session struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	StartedAt    string `json:"started_at"`
	StoppedAt    string `json:"stopped_at,omitempty"`
	DurationSecs int    `json:"duration_secs,omitempty"`
	Status       string `json:"status"`
	Screen       int    `json:"screen"`
	AudioDevice  int    `json:"audio_device"`
}

// ImprovementItem is a single UX improvement extracted by Claude.
type ImprovementItem struct {
	ID                int    `json:"id"`
	Severity          string `json:"severity"`
	Category          string `json:"category"`
	Description       string `json:"description"`
	UserQuote         string `json:"user_quote,omitempty"`
	AffectedComponent string `json:"affected_component,omitempty"`
	Timestamp         string `json:"timestamp,omitempty"`
	Source            string `json:"source"`
}

// BaseDir returns the root directory for UX sessions.
func BaseDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".lightwave", "ux")
}

// SessionsDir returns the sessions directory.
func SessionsDir() string {
	return filepath.Join(BaseDir(), "sessions")
}

// ModelsDir returns the whisper models directory.
func ModelsDir() string {
	return filepath.Join(BaseDir(), "models")
}

// NewSessionID generates a session ID from the current time.
func NewSessionID() string {
	return time.Now().Format("ux-20060102-150405")
}

// SessionDir returns the directory for a specific session.
func SessionDir(id string) string {
	return filepath.Join(SessionsDir(), id)
}

// RecordingPath returns the path to the MKV recording.
func RecordingPath(id string) string {
	return filepath.Join(SessionDir(id), "recording.mp4")
}

// PIDPath returns the path to the ffmpeg PID file.
func PIDPath(id string) string {
	return filepath.Join(SessionDir(id), "pid")
}

// MetadataPath returns the path to the session metadata JSON.
func MetadataPath(id string) string {
	return filepath.Join(SessionDir(id), "metadata.json")
}

// ItemsPath returns the path to the improvement items JSONL file.
func ItemsPath(id string) string {
	return filepath.Join(SessionDir(id), "items.jsonl")
}

// AnalysisPath returns the path to the full analysis output.
func AnalysisPath(id string) string {
	return filepath.Join(SessionDir(id), "analysis.md")
}

// TranscriptJSONPath returns the path to the whisper JSON output.
func TranscriptJSONPath(id string) string {
	return filepath.Join(SessionDir(id), "transcript.json")
}

// TranscriptSRTPath returns the path to the SRT subtitle file.
func TranscriptSRTPath(id string) string {
	return filepath.Join(SessionDir(id), "transcript.srt")
}

// AudioPath returns the path to the extracted audio WAV.
func AudioPath(id string) string {
	return filepath.Join(SessionDir(id), "audio.wav")
}

// FramesDir returns the directory for extracted keyframes.
func FramesDir(id string) string {
	return filepath.Join(SessionDir(id), "frames")
}

// CreateSession initializes a new session directory and writes metadata.
func CreateSession(name string, screen, audioDevice int) (*Session, error) {
	id := NewSessionID()
	dir := SessionDir(id)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}

	s := &Session{
		ID:          id,
		Name:        name,
		StartedAt:   time.Now().Format(time.RFC3339),
		Status:      StatusRecording,
		Screen:      screen,
		AudioDevice: audioDevice,
	}

	if err := s.Save(); err != nil {
		return nil, err
	}

	return s, nil
}

// Save writes the session metadata to disk.
func (s *Session) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	return os.WriteFile(MetadataPath(s.ID), data, 0644)
}

// LoadSession reads session metadata from disk.
func LoadSession(id string) (*Session, error) {
	data, err := os.ReadFile(MetadataPath(id))
	if err != nil {
		return nil, fmt.Errorf("read session %s: %w", id, err)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse session %s: %w", id, err)
	}
	return &s, nil
}

// ListSessions returns all sessions sorted by ID (newest first).
func ListSessions() ([]*Session, error) {
	dir := SessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	var sessions []*Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := LoadSession(e.Name())
		if err != nil {
			continue // skip corrupt sessions
		}
		sessions = append(sessions, s)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ID > sessions[j].ID // newest first
	})

	return sessions, nil
}

// FindActiveSession returns the currently recording session, if any.
func FindActiveSession() (*Session, error) {
	sessions, err := ListSessions()
	if err != nil {
		return nil, err
	}
	for _, s := range sessions {
		if s.Status == StatusRecording {
			return s, nil
		}
	}
	return nil, nil
}

// LatestSession returns the most recent session.
func LatestSession() (*Session, error) {
	sessions, err := ListSessions()
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no sessions found")
	}
	return sessions[0], nil
}

// LoadItems reads improvement items from a session's items.jsonl.
func LoadItems(id string) ([]ImprovementItem, error) {
	data, err := os.ReadFile(ItemsPath(id))
	if err != nil {
		return nil, fmt.Errorf("read items: %w", err)
	}

	var items []ImprovementItem
	// Try as JSON array first (Claude output)
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("parse items: %w", err)
	}
	return items, nil
}
