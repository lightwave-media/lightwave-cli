package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

	summary := flagStr(flags, "summary")
	if summary == "" {
		summary = "Deterministic failure detected"
	}

	repo := flagStr(flags, "repo")
	if repo == "" {
		if cwd, err := os.Getwd(); err == nil {
			repo = filepath.Base(cwd)
		} else {
			repo = "unknown"
		}
	}

	invariant := flagStr(flags, "invariant")
	if invariant == "" {
		invariant = summary
	}

	expectedStructure := flagStr(flags, "expected-structure")
	if expectedStructure == "" {
		expectedStructure = "Production code and configuration satisfy the reported invariant."
	}

	cureCommand := flagStr(flags, "cure-command")
	if cureCommand == "" {
		cureCommand = "Fix the reported root cause in production code or configuration."
	}

	forbiddenWorkarounds := flagStr(flags, "do-not")
	if forbiddenWorkarounds == "" {
		forbiddenWorkarounds = "Do not skip, disable, weaken, delete, or bypass the test or gate."
	}

	nextVerification := flagStrOr(flags, "next-verification", "mise run ci")
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(
		repo+"|"+kind+"|"+invariant+"|"+summary,
	)))[:24]

	rec := map[string]any{
		"id":                 fmt.Sprintf("fail-%d", time.Now().UnixNano()),
		"kind":               kind,
		"summary":            summary,
		"detected_at":        time.Now().UTC().Format(time.RFC3339Nano),
		"repo":               repo,
		"exit_code":          failureFlagInt(flags, "exit-code", 1),
		"signal_class":       flagStrOr(flags, "signal-class", "DEVELOPMENT"),
		"violated_invariant": invariant,
		"expected_structure": expectedStructure,
		"cure_command":       cureCommand,
		"do_not":             forbiddenWorkarounds,
		"next_verification":  nextVerification,
		"fingerprint":        fingerprint,
		"source":             flagStrOr(flags, "source", "lw failure record"),
		"fsm_state":          "triage",
	}

	if session := flagStr(flags, "session"); session != "" {
		rec["session_id"] = session
	}

	if paths := flagStrSlice(flags, "affected-path"); len(paths) > 0 {
		rec["affected_paths"] = paths
	}

	if reason := flagStr(flags, "bypass-reason"); reason != "" {
		rec["bypass_reason"] = reason
	}

	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	logPath := filepath.Join(dir, "failure-records.jsonl")

	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, reportFileMode)
	if err != nil {
		return err
	}

	if _, err = logFile.Write(append(line, '\n')); err != nil {
		_ = logFile.Close()
		return err
	}

	if err = logFile.Close(); err != nil {
		return err
	}

	snapshot, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "last-record.json"), append(snapshot, '\n'), reportFileMode)
}

func failureFlagInt(flags map[string]any, name string, fallback int) int {
	value, ok := flags[name]
	if !ok {
		return fallback
	}

	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed
		}
	}

	return fallback
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
		// Empty resolves to the repo the failure happened in — see the note in
		// issue_handlers.go. A triage issue belongs where the failure was.
		Repo:           flagStr(flags, "repo"),
		Title:          title,
		Kind:           gh.KindToolGap,
		Motivation:     summary,
		ProposedChange: flagStr(flags, "proposed-change"),
		Scope:          flagStr(flags, "scope"),
		Labels:         append(flagStrSlice(flags, "label"), "status:triage"),
		Origin:         flagStrOr(flags, "origin", "failureloop"),
		ProjectNumber:  gh.DefaultProjectNum,
		Org:            flagStrOr(flags, "org", gh.DefaultOrg),
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
