package ux

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// WhisperTranscript is the top-level JSON output from whisper-cli.
type WhisperTranscript struct {
	Transcription []WhisperSegment `json:"transcription"`
}

// WhisperSegment is a single segment from whisper output.
type WhisperSegment struct {
	Timestamps WhisperTimestamps `json:"timestamps"`
	Offsets    WhisperOffsets    `json:"offsets"`
	Text       string            `json:"text"`
}

// WhisperTimestamps holds human-readable timestamps.
type WhisperTimestamps struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// WhisperOffsets holds millisecond offsets.
type WhisperOffsets struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// Analyze runs the full analysis pipeline for a session.
func Analyze(sessionID string) error {
	session, err := LoadSession(sessionID)
	if err != nil {
		return err
	}

	if session.Status == StatusRecording {
		return fmt.Errorf("session %s is still recording — stop it first", sessionID)
	}

	recording := RecordingPath(sessionID)
	if _, err := os.Stat(recording); os.IsNotExist(err) {
		return fmt.Errorf("recording not found: %s", recording)
	}

	// Step 1: Extract audio
	fmt.Println("  Extracting audio...")
	if err := extractAudio(sessionID); err != nil {
		return fmt.Errorf("extract audio: %w", err)
	}

	// Step 2: Transcribe
	fmt.Println("  Transcribing with whisper...")
	if err := transcribe(sessionID); err != nil {
		return fmt.Errorf("transcribe: %w", err)
	}

	// Step 3: Extract keyframes
	fmt.Println("  Extracting keyframes...")
	if err := extractFrames(sessionID); err != nil {
		return fmt.Errorf("extract frames: %w", err)
	}

	// Step 4: Load transcript text
	fmt.Println("  Preparing analysis...")
	transcript, err := loadTranscriptText(sessionID)
	if err != nil {
		return fmt.Errorf("load transcript: %w", err)
	}

	// Step 5: Collect frame paths
	framePaths, err := collectFramePaths(sessionID)
	if err != nil {
		return fmt.Errorf("collect frames: %w", err)
	}

	// Step 6: Send to Claude
	fmt.Printf("  Analyzing with Claude (%d frames)...\n", len(framePaths))
	result, err := AnalyzeWithClaude(context.Background(), sessionID, transcript, framePaths)
	if err != nil && result == nil {
		return fmt.Errorf("claude analysis: %w", err)
	}

	// Step 7: Save results
	if result.RawText != "" {
		os.WriteFile(AnalysisPath(sessionID), []byte(result.RawText), 0644)
	}

	if len(result.Items) > 0 {
		itemsJSON, _ := json.MarshalIndent(result.Items, "", "  ")
		os.WriteFile(ItemsPath(sessionID), itemsJSON, 0644)
	}

	// Update session status
	session.Status = StatusAnalyzed
	session.Save()

	if err != nil {
		// Partial success — raw text was saved but items didn't parse
		fmt.Printf("  Warning: %v\n", err)
	}

	return nil
}

// extractAudio uses ffmpeg to extract audio as 16kHz mono WAV.
func extractAudio(sessionID string) error {
	input := RecordingPath(sessionID)
	output := AudioPath(sessionID)

	cmd := exec.Command("ffmpeg",
		"-i", input,
		"-vn",
		"-acodec", "pcm_s16le",
		"-ar", "16000",
		"-ac", "1",
		"-y",
		output,
	)
	return runQuiet(cmd, sessionID, "ffmpeg-audio.log")
}

// transcribe runs whisper-cli on the extracted audio.
func transcribe(sessionID string) error {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return fmt.Errorf("no config — run 'lw ux init' first")
	}

	modelPath := cfg.WhisperModel
	if modelPath == "" {
		modelPath = DefaultWhisperModelPath()
	}

	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return fmt.Errorf("whisper model not found at %s — run 'lw ux init'", modelPath)
	}

	input := AudioPath(sessionID)
	outputBase := filepath.Join(SessionDir(sessionID), "transcript")

	cmd := exec.Command("whisper-cli",
		"-m", modelPath,
		"-f", input,
		"--output-json",
		"--output-srt",
		"-of", outputBase,
	)
	return runQuiet(cmd, sessionID, "whisper.log")
}

// extractFrames uses ffmpeg to extract keyframes at regular intervals.
func extractFrames(sessionID string) error {
	input := RecordingPath(sessionID)
	framesDir := FramesDir(sessionID)

	if err := os.MkdirAll(framesDir, 0755); err != nil {
		return err
	}

	// Get session duration to decide frame interval
	session, err := LoadSession(sessionID)
	if err != nil {
		return err
	}

	// For short recordings (<60s), extract 1 frame per 5 seconds
	// For longer recordings, 1 frame per 30 seconds
	interval := 30
	if session.DurationSecs < 60 {
		interval = 5
	}

	outputPattern := filepath.Join(framesDir, "frame_%04d.jpg")

	cmd := exec.Command("ffmpeg",
		"-i", input,
		"-vf", fmt.Sprintf("fps=1/%d,scale=1280:-1,format=yuvj420p", interval),
		"-q:v", "3",
		"-y",
		outputPattern,
	)
	return runQuiet(cmd, sessionID, "ffmpeg-frames.log")
}

// loadTranscriptText reads the whisper JSON and formats it as timestamped text.
func loadTranscriptText(sessionID string) (string, error) {
	data, err := os.ReadFile(TranscriptJSONPath(sessionID))
	if err != nil {
		return "", err
	}

	var transcript WhisperTranscript
	if err := json.Unmarshal(data, &transcript); err != nil {
		return "", fmt.Errorf("parse whisper json: %w", err)
	}

	var sb strings.Builder
	for _, seg := range transcript.Transcription {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("[%s - %s] %s\n", seg.Timestamps.From, seg.Timestamps.To, text))
	}

	return sb.String(), nil
}

// runQuiet runs a command with stdout/stderr redirected to a log file in the session dir.
func runQuiet(cmd *exec.Cmd, sessionID, logName string) error {
	logPath := filepath.Join(SessionDir(sessionID), logName)
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	return cmd.Run()
}

// collectFramePaths returns sorted paths to all extracted keyframe images.
func collectFramePaths(sessionID string) ([]string, error) {
	framesDir := FramesDir(sessionID)
	entries, err := os.ReadDir(framesDir)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
			paths = append(paths, filepath.Join(framesDir, e.Name()))
		}
	}

	sort.Strings(paths)
	return paths, nil
}
