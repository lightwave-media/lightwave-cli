// Package githuborg bootstraps and reconciles lightwave-media GitHub org assets
// (Lightwave Swarm project, swarm labels, milestones).
package githuborg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/github"
)

const (
	DefaultOrg           = "lightwave-media"
	DefaultProjectNumber = 3
	DefaultProjectNodeID = "PVT_kwDODlnoUM4BbDql"
	DefaultStatusFieldID = "PVTSSF_lADODlnoUM4BbDqlzhV25Fs"
	BootstrapScriptRel   = "lightwave-infrastructure-catalog/scripts/bootstrap-github-org.sh"
)

// SwarmRepos is the estate rollout set (mirrors bootstrap-github-org.sh).
var SwarmRepos = []string{
	"lightwave-core",
	"lightwave-cli",
	"lightwave-ui",
	"lightwave-platform",
	"lightwave-sys",
	"lightwave-ai",
	"lightwave-infrastructure-catalog",
	"lightwave-infrastructure-live",
	"createOS",
	"nullclaw",
	"nullhub",
	"nullbuilder",
	"nulltickets",
	"nullwatch",
	"nullboiler",
	"joelschaeffer-site",
	"homebrew-tap",
}

// Options controls bootstrap and scrum sync.
type Options struct {
	Org           string
	LightwaveRoot string
	TargetRepo    string
	FullOrg       bool
	DryRun        bool
}

// SyncReport summarizes a scrum sync run.
type SyncReport struct {
	Org           string   `json:"org"`
	ProjectNumber int      `json:"project_number"`
	BootstrapRan  bool     `json:"bootstrap_ran"`
	ReposChecked  []string `json:"repos_checked"`
	IssuesAdded   int      `json:"issues_added"`
	IssuesSkipped int      `json:"issues_skipped"`
	Errors        []string `json:"errors,omitempty"`
}

// ResolveBootstrapScript locates the org bootstrap shell script under ~/dev.
func ResolveBootstrapScript(lightwaveRoot string) (string, error) {
	if lightwaveRoot == "" {
		home, _ := os.UserHomeDir()
		lightwaveRoot = filepath.Join(home, "dev")
	}
	path := filepath.Join(lightwaveRoot, BootstrapScriptRel)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("bootstrap script not found at %s: %w", path, err)
	}
	return path, nil
}

// RunBootstrap executes bootstrap-github-org.sh (idempotent).
func RunBootstrap(ctx context.Context, opts Options) error {
	if opts.Org == "" {
		opts.Org = DefaultOrg
	}
	if opts.DryRun {
		if opts.TargetRepo != "" {
			fmt.Printf("would bootstrap org slice for %s/%s\n", opts.Org, opts.TargetRepo)
		} else {
			fmt.Printf("would bootstrap full org %s\n", opts.Org)
		}
		return nil
	}

	script, err := ResolveBootstrapScript(opts.LightwaveRoot)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Env = append(os.Environ(),
		"ORG_LOGIN="+opts.Org,
		"PROJECT_ID="+DefaultProjectNodeID,
	)
	if opts.TargetRepo != "" {
		cmd.Env = append(cmd.Env, "TARGET_REPO="+opts.TargetRepo)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Sync reconciles org bootstrap plus Lightwave Swarm board items for swarm-label issues.
func Sync(ctx context.Context, opts Options) (*SyncReport, error) {
	if opts.Org == "" {
		opts.Org = DefaultOrg
	}

	report := &SyncReport{
		Org:           opts.Org,
		ProjectNumber: DefaultProjectNumber,
	}

	bootstrapOpts := opts
	if !opts.DryRun {
		if err := RunBootstrap(ctx, bootstrapOpts); err != nil {
			return report, err
		}
		report.BootstrapRan = true
	}

	repos := SwarmRepos
	if opts.TargetRepo != "" {
		repos = []string{opts.TargetRepo}
	}

	onBoard, err := github.ListProjectItems(opts.Org, DefaultProjectNumber)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
		onBoard = map[string]bool{}
	}

	swarmLabels := []string{
		"status:ready",
		"status:in-progress",
		"status:triage",
		"status:blocked",
		"issue-type:agent-task",
	}

	for _, repo := range repos {
		report.ReposChecked = append(report.ReposChecked, repo)
		fullRepo := opts.Org + "/" + repo
		for _, label := range swarmLabels {
			if err := addLabeledIssuesToProject(ctx, fullRepo, opts.Org, label, onBoard, opts.DryRun, report); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("%s label %s: %v", repo, label, err))
			}
		}
	}

	if !opts.DryRun {
		appendObservability(report)
	}

	return report, nil
}

func addLabeledIssuesToProject(ctx context.Context, repo, org, label string, onBoard map[string]bool, dryRun bool, report *SyncReport) error {
	cmd := exec.CommandContext(ctx, "gh", "issue", "list",
		"--repo", repo,
		"--state", "open",
		"--label", label,
		"--json", "url",
		"--limit", "100",
	)
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	var issues []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(out, &issues); err != nil {
		return err
	}

	for _, iss := range issues {
		if onBoard[iss.URL] {
			report.IssuesSkipped++
			continue
		}
		if dryRun {
			fmt.Printf("would add to project: %s\n", iss.URL)
			report.IssuesAdded++
			continue
		}
		if err := github.AddToProject(org, DefaultProjectNumber, iss.URL); err != nil {
			report.Errors = append(report.Errors, err.Error())
			continue
		}
		onBoard[iss.URL] = true
		report.IssuesAdded++
	}
	return nil
}

func appendObservability(report *SyncReport) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, ".lightwave", "observability", "scrum-sync.jsonl")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	line := fmt.Sprintf(`{"ts":%q,"org":%q,"added":%d,"skipped":%d,"errors":%d}`+"\n",
		time.Now().UTC().Format(time.RFC3339),
		report.Org,
		report.IssuesAdded,
		report.IssuesSkipped,
		len(report.Errors),
	)
	_, _ = f.WriteString(line)
}
