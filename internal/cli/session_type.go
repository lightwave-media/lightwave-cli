package cli

import (
	"fmt"
	"regexp"
	"strings"
)

// SessionType determines the Claude Code session profile for a task.
// Inferred from GitHub Issue labels with priority: backend > frontend > infra.
type SessionType string

const (
	SessionBackend  SessionType = "backend"
	SessionFrontend SessionType = "frontend"
	SessionInfra    SessionType = "infra"
)

// sessionTypePriority defines resolution order when multiple labels exist.
// Lower index = higher priority.
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

// inferSessionTypeFromIssue extracts labels from a ghIssue and infers session type.
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

// String returns the session type as a string.
func (s SessionType) String() string {
	return string(s)
}

// slugRe matches non-alphanumeric characters for slug generation.
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
		// Don't end on a partial word
		if last := strings.LastIndex(slug, "-"); last > 20 {
			slug = slug[:last]
		}
	}

	return fmt.Sprintf("%s/issue-%d-%s", prefix, issueNumber, slug)
}
