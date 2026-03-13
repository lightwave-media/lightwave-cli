package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var validSessionNameRe = regexp.MustCompile("^[a-zA-Z0-9_-]+$")

var (
	ErrNoServer        = errors.New("no tmux server running")
	ErrSessionExists   = errors.New("session already exists")
	ErrSessionNotFound = errors.New("session not found")
	ErrInvalidName     = errors.New("invalid session name")
)

type Session struct {
	Name    string
	Created time.Time
	Windows int
}

type Tmux struct{}

func New() *Tmux { return &Tmux{} }

func validateName(name string) error {
	if name == "" || !validSessionNameRe.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

func (t *Tmux) run(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		errStr := strings.TrimSpace(stderr.String())
		if strings.Contains(errStr, "no server running") || strings.Contains(errStr, "error connecting") {
			return "", ErrNoServer
		}
		if strings.Contains(errStr, "duplicate session") {
			return "", ErrSessionExists
		}
		if strings.Contains(errStr, "session not found") || strings.Contains(errStr, "can't find session") {
			return "", ErrSessionNotFound
		}
		return "", fmt.Errorf("tmux %s: %s", strings.Join(args[:1], " "), errStr)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (t *Tmux) HasSession(name string) (bool, error) {
	if err := validateName(name); err != nil {
		return false, err
	}
	_, err := t.run("has-session", "-t", name)
	if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrNoServer) {
		return false, nil
	}
	return err == nil, err
}

func (t *Tmux) NewSession(name, workDir, command string) error {
	return t.NewSessionWithEnv(name, workDir, command, nil)
}

func (t *Tmux) NewSessionWithEnv(name, workDir, command string, env map[string]string) error {
	if err := validateName(name); err != nil {
		return err
	}

	fullCmd := command
	if len(env) > 0 {
		var envParts []string
		for k, v := range env {
			envParts = append(envParts, fmt.Sprintf("export %s=%q", k, v))
		}
		fullCmd = strings.Join(envParts, " && ") + " && " + command
	}

	args := []string{"new-session", "-d", "-s", name, "-c", workDir, fullCmd}
	_, err := t.run(args...)
	return err
}

func (t *Tmux) KillSession(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	_, err := t.run("kill-session", "-t", name)
	if errors.Is(err, ErrSessionNotFound) {
		return nil
	}
	return err
}

func (t *Tmux) SendKeys(name, keys string) error {
	if err := validateName(name); err != nil {
		return err
	}
	_, err := t.run("send-keys", "-t", name, keys, "Enter")
	return err
}

func (t *Tmux) CapturePane(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	return t.run("capture-pane", "-t", name, "-p")
}

func (t *Tmux) ListSessions() ([]Session, error) {
	out, err := t.run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		if errors.Is(err, ErrNoServer) {
			return nil, nil
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}

	var sessions []Session
	for _, name := range strings.Split(out, "\n") {
		name = strings.TrimSpace(name)
		if strings.HasPrefix(name, "lw-") {
			sessions = append(sessions, Session{Name: name})
		}
	}
	return sessions, nil
}

func (t *Tmux) IsAlive(name string) bool {
	has, _ := t.HasSession(name)
	return has
}

func (t *Tmux) WaitForExit(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !t.IsAlive(name) {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("session %s still running after %v", name, timeout)
}
