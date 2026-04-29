package cli

import (
	"fmt"
	"regexp"
	"strings"
)

// notifyJoel logs a notification message to stdout. Originally a hook for the
// Elixir orchestrator's PubSub channel; the orchestrator was retired but
// surviving call sites in github.go / spec.go still emit user-facing
// notifications through this entrypoint.
func notifyJoel(message string) {
	fmt.Printf("  Notification: %s\n", message)
}

// escapeAppleScript escapes a string for safe use inside AppleScript
// double-quoted strings. Replaces backslashes and double quotes with their
// escaped forms.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// SessionType determines the Claude Code session profile for a task.
// Inferred from GitHub Issue labels with priority: backend > frontend > infra.
type SessionType string

const (
	SessionBackend  SessionType = "backend"
	SessionFrontend SessionType = "frontend"
	SessionInfra    SessionType = "infra"
)

var sessionTypePriority = []struct {
	label       string
	sessionType SessionType
}{
	{"backend", SessionBackend},
	{"python", SessionBackend},
	{"docker", SessionInfra},
	{"frontend", SessionFrontend},
	{"infra", SessionInfra},
	{"github-actions", SessionInfra},
}

// InferSessionType determines session type from issue labels.
// Returns SessionBackend as default if no matching label found.
func InferSessionType(labels []string) SessionType {
	labelSet := make(map[string]bool)
	for _, l := range labels {
		labelSet[l] = true
	}
	for _, sp := range sessionTypePriority {
		if labelSet[sp.label] {
			return sp.sessionType
		}
	}
	return SessionBackend
}

// inferSessionTypeFromIssue extracts labels from a ghIssue and infers session
// type. ghIssue is defined in github.go.
func inferSessionTypeFromIssue(issue ghIssue) SessionType {
	var labelNames []string
	for _, l := range issue.Labels {
		labelNames = append(labelNames, l.Name)
	}
	return InferSessionType(labelNames)
}

// WorkingDir returns the default working directory for a session type.
func (s SessionType) WorkingDir() string {
	switch s {
	case SessionFrontend:
		return "packages/lightwave-frontend"
	case SessionInfra:
		return "packages/lightwave-infra"
	default:
		return "packages/lightwave-core"
	}
}

func (s SessionType) String() string {
	return string(s)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// IssueBranchName generates a branch name from issue metadata.
// Format: {type}/issue-{N}-{slug}
func IssueBranchName(issueNumber int, title string, taskType string) string {
	prefix := "feat"
	switch taskType {
	case "bug", "fix", "hotfix":
		prefix = "fix"
	case "chore":
		prefix = "chore"
	}

	slug := strings.ToLower(title)
	slug = slugRe.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = slug[:40]
		if last := strings.LastIndex(slug, "-"); last > 20 {
			slug = slug[:last]
		}
	}

	return fmt.Sprintf("%s/issue-%d-%s", prefix, issueNumber, slug)
}
