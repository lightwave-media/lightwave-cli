package ux

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const composeProject = "lightwave-platform"

// sessionPIDs tracks all background processes for a recording session.
type sessionPIDs struct {
	FFmpeg       int `json:"ffmpeg"`
	BackendLogs  int `json:"backend_logs,omitempty"`
	FrontendLogs int `json:"frontend_logs,omitempty"`
}

func pidsPath(sessionID string) string {
	return SessionDir(sessionID) + "/pids.json"
}

func savePIDs(sessionID string, pids *sessionPIDs) error {
	data, _ := json.Marshal(pids)
	return os.WriteFile(pidsPath(sessionID), data, 0644)
}

func loadPIDs(sessionID string) (*sessionPIDs, error) {
	data, err := os.ReadFile(pidsPath(sessionID))
	if err != nil {
		return nil, err
	}
	var pids sessionPIDs
	return &pids, json.Unmarshal(data, &pids)
}

// StartRecording launches ffmpeg + log tailers for a UX session.
func StartRecording(session *Session) error {
	outputPath := RecordingPath(session.ID)
	sessionDir := SessionDir(session.ID)

	// AVFoundation input: "screen_index:audio_index"
	input := fmt.Sprintf("%d:%d", session.Screen, session.AudioDevice)

	// ── ffmpeg ──────────────────────────────────────────────────────────
	ffmpegCmd := exec.Command("ffmpeg",
		"-f", "avfoundation",
		"-framerate", "5",
		"-capture_cursor", "1",
		"-capture_mouse_clicks", "1",
		"-i", input,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-crf", "28",
		"-c:a", "aac",
		"-b:a", "128k",
		"-y",
		outputPath,
	)
	ffmpegCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	ffmpegLog, err := os.Create(sessionDir + "/ffmpeg.log")
	if err != nil {
		return fmt.Errorf("create ffmpeg log: %w", err)
	}
	ffmpegCmd.Stdout = ffmpegLog
	ffmpegCmd.Stderr = ffmpegLog

	if err := ffmpegCmd.Start(); err != nil {
		ffmpegLog.Close()
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	ffmpegLog.Close()

	pids := &sessionPIDs{FFmpeg: ffmpegCmd.Process.Pid}
	ffmpegCmd.Process.Release()

	// ── backend log tailer ──────────────────────────────────────────────
	backendPid := startLogTailer(sessionDir, "backend")
	if backendPid > 0 {
		pids.BackendLogs = backendPid
	}

	// ── frontend log tailer ─────────────────────────────────────────────
	frontendPid := startLogTailer(sessionDir, "frontend")
	if frontendPid > 0 {
		pids.FrontendLogs = frontendPid
	}

	// Save all PIDs
	if err := savePIDs(session.ID, pids); err != nil {
		// Kill ffmpeg if we can't save PIDs
		if p, err := os.FindProcess(pids.FFmpeg); err == nil {
			p.Kill()
		}
		return fmt.Errorf("save pids: %w", err)
	}

	// Also write legacy pid file for backwards compat
	os.WriteFile(PIDPath(session.ID), []byte(strconv.Itoa(pids.FFmpeg)), 0644)

	return nil
}

// startLogTailer spawns `docker compose logs -f --since=now <service>` in the background.
// Returns the PID, or 0 if the service isn't running.
func startLogTailer(sessionDir, service string) int {
	cmd := exec.Command("docker", "compose",
		"-p", composeProject,
		"logs", "-f", "--since", "0s", "--timestamps",
		service,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	logFile, err := os.Create(fmt.Sprintf("%s/%s.log", sessionDir, service))
	if err != nil {
		return 0
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return 0
	}
	logFile.Close()

	pid := cmd.Process.Pid
	cmd.Process.Release()
	return pid
}

// StopRecording stops all background processes for a session.
func StopRecording(session *Session) error {
	pids, err := loadPIDs(session.ID)
	if err != nil {
		// Fall back to legacy pid file
		return stopLegacy(session)
	}

	// Stop log tailers first (SIGTERM is fine, they're just tailing)
	for _, pid := range []int{pids.BackendLogs, pids.FrontendLogs} {
		if pid > 0 {
			if p, err := os.FindProcess(pid); err == nil {
				p.Signal(syscall.SIGTERM)
			}
		}
	}

	// Stop ffmpeg with SIGINT for graceful container finalization
	if pids.FFmpeg > 0 {
		process, err := os.FindProcess(pids.FFmpeg)
		if err != nil {
			return fmt.Errorf("find ffmpeg process %d: %w", pids.FFmpeg, err)
		}
		if err := process.Signal(syscall.SIGINT); err != nil {
			return fmt.Errorf("signal ffmpeg (pid %d): %w", pids.FFmpeg, err)
		}

		// Wait for ffmpeg to finalize the MP4
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if err := process.Signal(syscall.Signal(0)); err != nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	// Update session metadata
	now := time.Now()
	session.StoppedAt = now.Format(time.RFC3339)
	session.Status = StatusStopped

	startTime, err := time.Parse(time.RFC3339, session.StartedAt)
	if err == nil {
		session.DurationSecs = int(now.Sub(startTime).Seconds())
	}

	// Clean up PID files
	os.Remove(pidsPath(session.ID))
	os.Remove(PIDPath(session.ID))

	if err := session.Save(); err != nil {
		return err
	}

	// Best-effort: generate time-anchored docker log JSONL
	_ = generateSyncedLog(session)
	return nil
}

// stopLegacy handles sessions that only have the old single-pid file.
func stopLegacy(session *Session) error {
	pidData, err := os.ReadFile(PIDPath(session.ID))
	if err != nil {
		return fmt.Errorf("read pid file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		return fmt.Errorf("parse pid: %w", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}

	if err := process.Signal(syscall.SIGINT); err != nil {
		return fmt.Errorf("signal ffmpeg (pid %d): %w", pid, err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	now := time.Now()
	session.StoppedAt = now.Format(time.RFC3339)
	session.Status = StatusStopped

	startTime, err := time.Parse(time.RFC3339, session.StartedAt)
	if err == nil {
		session.DurationSecs = int(now.Sub(startTime).Seconds())
	}

	os.Remove(PIDPath(session.ID))
	if err := session.Save(); err != nil {
		return err
	}

	// Best-effort: generate time-anchored docker log JSONL
	_ = generateSyncedLog(session)
	return nil
}

// IsFFmpegRunning checks if the ffmpeg process for a session is still alive.
func IsFFmpegRunning(session *Session) bool {
	pids, err := loadPIDs(session.ID)
	if err != nil {
		// Fall back to legacy pid file
		pidData, err := os.ReadFile(PIDPath(session.ID))
		if err != nil {
			return false
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
		if err != nil {
			return false
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			return false
		}
		return process.Signal(syscall.Signal(0)) == nil
	}

	process, err := os.FindProcess(pids.FFmpeg)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

// syncedLogEntry is one line in docker.synced.jsonl.
type syncedLogEntry struct {
	OffsetSeconds float64 `json:"offset_seconds"`
	Service       string  `json:"service"`
	Level         string  `json:"level"`
	Message       string  `json:"message"`
}

// generateSyncedLog parses backend.log and frontend.log captured during a session
// and writes docker.synced.jsonl with offset_seconds relative to session_start_time.
func generateSyncedLog(session *Session) error {
	if session.SessionStartTime == "" {
		return nil
	}

	startTime, err := time.Parse(time.RFC3339, session.SessionStartTime)
	if err != nil {
		return fmt.Errorf("parse session_start_time: %w", err)
	}

	var entries []syncedLogEntry

	for _, service := range []string{"backend", "frontend"} {
		logPath := filepath.Join(SessionDir(session.ID), service+".log")
		data, err := os.ReadFile(logPath)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			entry := parseDockerLogLine(line, service, startTime)
			if entry != nil {
				entries = append(entries, *entry)
			}
		}
	}

	if len(entries) == 0 {
		return nil
	}

	f, err := os.Create(DockerSyncedPath(session.ID))
	if err != nil {
		return fmt.Errorf("create synced log: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, entry := range entries {
		if err := enc.Encode(entry); err != nil {
			return fmt.Errorf("write entry: %w", err)
		}
	}

	return nil
}

// parseDockerLogLine extracts a syncedLogEntry from a docker compose log line.
// Docker compose logs --timestamps format: "{service}  | {RFC3339Nano_timestamp} {message}"
func parseDockerLogLine(line, service string, startTime time.Time) *syncedLogEntry {
	// Strip the "{service} | " prefix added by docker compose
	if idx := strings.Index(line, " | "); idx != -1 {
		line = strings.TrimSpace(line[idx+3:])
	}

	// Timestamp is the first space-delimited token
	space := strings.IndexByte(line, ' ')
	if space < 1 {
		return nil
	}

	rawTS, message := line[:space], strings.TrimSpace(line[space+1:])
	if message == "" {
		return nil
	}

	ts, err := time.Parse(time.RFC3339Nano, rawTS)
	if err != nil {
		ts, err = time.Parse(time.RFC3339, rawTS)
		if err != nil {
			return nil
		}
	}

	return &syncedLogEntry{
		OffsetSeconds: ts.Sub(startTime).Seconds(),
		Service:       service,
		Level:         detectLogLevel(message),
		Message:       message,
	}
}

// detectLogLevel scans a log message for common level keywords.
func detectLogLevel(line string) string {
	upper := strings.ToUpper(line)
	for _, level := range []string{"CRITICAL", "ERROR", "WARNING", "WARN", "DEBUG", "INFO"} {
		if strings.Contains(upper, level) {
			return strings.ToLower(level)
		}
	}
	return "info"
}

// FormatDuration converts seconds to a human-readable duration string.
func FormatDuration(secs int) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	m := secs / 60
	s := secs % 60
	if m < 60 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
}
