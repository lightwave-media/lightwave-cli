package ux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StartRecording launches ffmpeg to record screen + mic audio.
func StartRecording(session *Session) error {
	outputPath := RecordingPath(session.ID)

	// AVFoundation input: "screen_index:audio_index"
	input := fmt.Sprintf("%d:%d", session.Screen, session.AudioDevice)

	cmd := exec.Command("ffmpeg",
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

	// Detach from terminal — run in background
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Send ffmpeg output to a log file for debugging
	logPath := fmt.Sprintf("%s/ffmpeg.log", SessionDir(session.ID))
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	logFile.Close()

	// Save PID
	pid := cmd.Process.Pid
	if err := os.WriteFile(PIDPath(session.ID), []byte(strconv.Itoa(pid)), 0644); err != nil {
		// Kill the process if we can't save the PID
		cmd.Process.Kill()
		return fmt.Errorf("save pid: %w", err)
	}

	// Detach — don't wait for the process
	cmd.Process.Release()

	return nil
}

// StopRecording sends SIGINT to the ffmpeg process for graceful shutdown.
func StopRecording(session *Session) error {
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

	// Send SIGINT for graceful MKV finalization
	if err := process.Signal(syscall.SIGINT); err != nil {
		return fmt.Errorf("signal ffmpeg (pid %d): %w", pid, err)
	}

	// Wait for process to exit (poll with timeout)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			// Process is gone
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Update session metadata
	now := time.Now()
	session.StoppedAt = now.Format(time.RFC3339)
	session.Status = StatusStopped

	// Calculate duration
	startTime, err := time.Parse(time.RFC3339, session.StartedAt)
	if err == nil {
		session.DurationSecs = int(now.Sub(startTime).Seconds())
	}

	// Clean up PID file
	os.Remove(PIDPath(session.ID))

	return session.Save()
}

// IsFFmpegRunning checks if the ffmpeg process for a session is still alive.
func IsFFmpegRunning(session *Session) bool {
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
