package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	gh "github.com/lightwave-media/lightwave-cli/internal/github"
)

func init() {
	RegisterHandler("failure.record", failureRecordHandler)
	RegisterHandler("failure.file", failureFileHandler)
	RegisterHandler("failure.status", failureStatusHandler)
}

const maxFailureIssueTitleLen = 120

func failureRecordHandler(_ context.Context, _ []string, flags map[string]any) error {
	home, _ := os.UserHomeDir()

	dir := filepath.Join(home, ".lightwave", "observability", "failures")
	if err := os.MkdirAll(dir, codegenDirPerm); err != nil {
		return err
	}

	kind := flagStr(flags, "kind")
	if kind == "" {
		kind = "tool-gap"
	}

	rec := map[string]any{
		"id":          fmt.Sprintf("fail-%d", time.Now().Unix()),
		"kind":        kind,
		"summary":     flagStr(flags, "summary"),
		"detected_at": time.Now().UTC().Format(time.RFC3339),
		"fsm_state":   "triage",
	}

	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "last-record.json"), append(b, '\n'), reportFileMode)
}

func failureFileHandler(ctx context.Context, _ []string, flags map[string]any) error {
	summary := flagStr(flags, "summary")
	if summary == "" {
		summary = "Tool gap / failure detected — triage required"
	}

	title := "[triage] " + summary
	if len(title) > maxFailureIssueTitleLen {
		title = title[:maxFailureIssueTitleLen-3] + "..."
	}

	opts := gh.IssueCreateOpts{
		Repo:           flagStrOr(flags, "repo", gh.DefaultRepo),
		Title:          title,
		Kind:           gh.KindToolGap,
		Motivation:     summary,
		ProposedChange: flagStr(flags, "proposed-change"),
		Scope:          flagStr(flags, "scope"),
		Labels:         append(flagStrSlice(flags, "label"), "status:triage"),
		Origin:         flagStrOr(flags, "origin", "failureloop"),
		ProjectNumber:  gh.DefaultProjectNum,
		Org:            flagStrOr(flags, "org", gh.DefaultIssueOrg),
		DryRun:         flagBool(flags, "dry-run"),
	}

	if err := failureRecordHandler(ctx, nil, flags); err != nil {
		return err
	}

	result, err := gh.CreateCompliantIssue(opts)
	if err != nil {
		return fmt.Errorf("failure file: %w", err)
	}

	if opts.DryRun {
		fmt.Println("failure file: dry-run ok")
		return nil
	}

	fmt.Printf("failure file: created issue #%d\n%s\n", result.Number, result.URL)

	return nil
}

func failureStatusHandler(_ context.Context, _ []string, _ map[string]any) error {
	fmt.Println("failure status: triage")
	return nil
}
