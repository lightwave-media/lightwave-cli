package github

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	DefaultIssueOrg     = "lightwave-media"
	DefaultProjectNum   = 3 // Lightwave Swarm
	EnvIssueCreateGuard = "LW_ISSUE_CREATE"
)

// IssueKind selects the GitHub issue form template shape.
type IssueKind string

const (
	KindFeatureRequest IssueKind = "feature_request"
	KindBugReport      IssueKind = "bug_report"
	KindToolGap        IssueKind = "tool_gap"
)

// IssueCreateOpts holds inputs for lw issue create.
type IssueCreateOpts struct {
	Repo           string
	Title          string
	Kind           IssueKind
	Motivation     string
	ProposedChange string
	Scope          string // bug_report: affected surface
	KindDetail     string // feature_request dropdown value
	Labels         []string
	Refs           []string // owner/repo#N or #N
	Closes         []string
	Origin         string // owner/repo#N — upstream gap source
	Milestone      string
	ProjectNumber  int
	Org            string
	DryRun         bool
}

// IssueCreateResult is returned after a successful create.
type IssueCreateResult struct {
	URL    string
	Number int
}

// DefaultLabelsForKind returns template default labels for a kind.
func DefaultLabelsForKind(kind IssueKind) []string {
	switch kind {
	case KindBugReport:
		return []string{"bug", "needs-triage"}
	case KindToolGap:
		return []string{"tool-gap", "needs-triage"}
	default:
		return []string{"enhancement", "needs-triage"}
	}
}

// BuildIssueBody renders markdown matching .github/ISSUE_TEMPLATE/*.yml sections.
func BuildIssueBody(opts IssueCreateOpts) (string, error) {
	if strings.TrimSpace(opts.Motivation) == "" {
		return "", fmt.Errorf("--motivation is required")
	}

	var b strings.Builder
	switch opts.Kind {
	case KindBugReport:
		if scope := strings.TrimSpace(opts.Scope); scope != "" {
			b.WriteString("### Affected surface\n")
			b.WriteString(scope)
			b.WriteString("\n\n")
		}
		b.WriteString("### Reproduction\n")
		b.WriteString(strings.TrimSpace(opts.Motivation))
		b.WriteString("\n\n")
		if pc := strings.TrimSpace(opts.ProposedChange); pc != "" {
			b.WriteString("### Expected / actual\n")
			b.WriteString(pc)
			b.WriteString("\n\n")
		}
	default:
		if opts.Kind == KindFeatureRequest {
			kindLine := strings.TrimSpace(opts.KindDetail)
			if kindLine == "" {
				kindLine = "Other"
			}
			b.WriteString("### Kind\n")
			b.WriteString(kindLine)
			b.WriteString("\n\n")
		}
		b.WriteString("### Motivation\n")
		b.WriteString(strings.TrimSpace(opts.Motivation))
		b.WriteString("\n\n")
		if pc := strings.TrimSpace(opts.ProposedChange); pc == "" {
			return "", fmt.Errorf("--proposed-change is required for kind %q", opts.Kind)
		}
		b.WriteString("### Proposed change\n")
		b.WriteString(strings.TrimSpace(opts.ProposedChange))
		b.WriteString("\n\n")
		if opts.Kind == KindToolGap {
			b.WriteString("### Affected command\n")
			if cmd := strings.TrimSpace(opts.Scope); cmd != "" {
				b.WriteString(cmd)
			} else {
				b.WriteString("(see proposed change)")
			}
			b.WriteString("\n\n")
		}
	}

	appendCrossLinks(&b, opts)
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

func appendCrossLinks(b *strings.Builder, opts IssueCreateOpts) {
	var footer []string
	for _, ref := range opts.Refs {
		if s := normalizeIssueRef(ref, opts.Repo); s != "" {
			footer = append(footer, "Refs "+s)
		}
	}
	for _, ref := range opts.Closes {
		if s := normalizeIssueRef(ref, opts.Repo); s != "" {
			footer = append(footer, "Closes "+s)
		}
	}
	if origin := strings.TrimSpace(opts.Origin); origin != "" {
		if s := normalizeIssueRef(origin, opts.Repo); s != "" {
			footer = append(footer, "Origin: "+s)
		}
	}
	if len(footer) == 0 {
		return
	}
	b.WriteString("\n---\n")
	for _, line := range footer {
		b.WriteString(line)
		b.WriteString("\n")
	}
}

// normalizeIssueRef accepts "owner/repo#N", "repo#N", or "#N" (same repo).
func normalizeIssueRef(ref, defaultRepo string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "#") {
		return defaultRepo + ref
	}
	if strings.Contains(ref, "#") {
		parts := strings.SplitN(ref, "#", 2)
		ownerRepo := strings.TrimSpace(parts[0])
		num := strings.TrimSpace(parts[1])
		if num == "" {
			return ""
		}
		if !strings.Contains(ownerRepo, "/") {
			ownerRepo = DefaultIssueOrg + "/" + ownerRepo
		}
		return ownerRepo + "#" + num
	}
	return ref
}

// CreateCompliantIssue creates a GitHub issue via gh with template defaults.
func CreateCompliantIssue(opts IssueCreateOpts) (IssueCreateResult, error) {
	if opts.Repo == "" {
		opts.Repo = DefaultRepo
	}
	if opts.Org == "" {
		opts.Org = DefaultIssueOrg
	}
	if opts.ProjectNumber == 0 {
		opts.ProjectNumber = DefaultProjectNum
	}
	if opts.Kind == "" {
		opts.Kind = KindFeatureRequest
	}

	body, err := BuildIssueBody(opts)
	if err != nil {
		return IssueCreateResult{}, err
	}

	labels := dedupeLabels(append(DefaultLabelsForKind(opts.Kind), opts.Labels...))
	if opts.DryRun {
		fmt.Printf("dry-run: would create issue on %s\n", opts.Repo)
		fmt.Printf("title: %s\n", opts.Title)
		fmt.Printf("labels: %s\n", strings.Join(labels, ","))
		if opts.Milestone != "" {
			fmt.Printf("milestone: %s\n", opts.Milestone)
		}
		fmt.Printf("project: %s#%d\n", opts.Org, opts.ProjectNumber)
		fmt.Println("--- body ---")
		fmt.Print(body)
		return IssueCreateResult{URL: "(dry-run)", Number: 0}, nil
	}

	args := []string{"issue", "create",
		"--repo", opts.Repo,
		"--title", opts.Title,
		"--body", body,
	}
	if len(labels) > 0 {
		args = append(args, "--label", strings.Join(labels, ","))
	}
	if opts.Milestone != "" {
		args = append(args, "--milestone", opts.Milestone)
	}

	cmd := exec.Command("gh", args...)
	cmd.Env = append(os.Environ(), EnvIssueCreateGuard+"=1")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return IssueCreateResult{}, fmt.Errorf("gh issue create failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return IssueCreateResult{}, fmt.Errorf("gh issue create failed: %w", err)
	}

	issueURL := strings.TrimSpace(string(out))
	num := issueNumberFromURL(issueURL)

	if opts.ProjectNumber > 0 {
		if err := AddToProject(opts.Org, opts.ProjectNumber, issueURL); err != nil {
			return IssueCreateResult{URL: issueURL, Number: num},
				fmt.Errorf("issue #%d created but project link failed: %w", num, err)
		}
	}

	return IssueCreateResult{URL: issueURL, Number: num}, nil
}

func dedupeLabels(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, l := range in {
		l = strings.TrimSpace(l)
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}
