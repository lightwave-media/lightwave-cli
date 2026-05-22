// Package gate is the local pre-egress gate for `lw local *`.
//
// The gate composes per-repo stages (doctor → fmt → lint → test →
// test-harness → act) into a single PASS/FAIL signal, persists a JSON
// report keyed by repo + HEAD sha, and offers an egress guard
// (RequireGreenForHEAD) that mutating commands MUST call before doing
// anything externally visible — `lw local pr`, future `lw deploy`, etc.
//
// State lives under ${XDG_STATE_HOME:-$HOME/.local/state}/lightwave/
// dev-gate/<repo>/<sha>.json, matching the push-circuit-breaker
// convention in internal/cli/hooks_circuit_breaker.go.
package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Status is the pass/fail/skip vocabulary used in every stage and at the
// report top level.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"

	// stateDirMode / stateFileMode are the 0o700 / 0o600 bits used when
	// writing the gate report. Matches internal/memory/memory.go — gate
	// reports may carry session-identifying notes, so the same private-
	// to-user perms apply.
	stateDirMode  os.FileMode = 0o700
	stateFileMode os.FileMode = 0o600

	// shortSHALen is the prefix length used by reason messages. Matches
	// what `git log --oneline` truncates to.
	shortSHALen = 8
)

// StageResult is what one Stage records into the Report. Notes carries
// the remediation hint operators see when a gate refuses to let them
// push — keep it short and specific (e.g. "run `lw local fmt`").
type StageResult struct {
	Extra      map[string]any `json:"extra,omitempty"`
	Status     Status         `json:"status"`
	Notes      string         `json:"notes,omitempty"`
	DurationMS int64          `json:"duration_ms"`
}

// Report is the on-disk JSON written by `lw local gate` and read by
// `lw local report` / `lw local pr` / any future egress guard.
type Report struct {
	TS      time.Time              `json:"ts"`
	Stages  map[string]StageResult `json:"stages"`
	SHA     string                 `json:"sha"`
	Repo    string                 `json:"repo"`
	Overall Status                 `json:"overall"`
}

// StatePath returns the canonical on-disk path for (repo, sha). Honors
// XDG_STATE_HOME the same way the push circuit breaker does — keeping
// gate state alongside other lightwave CLI state under one root.
func StatePath(repo, sha string) (string, error) {
	if repo == "" {
		return "", errors.New("repo is required")
	}

	if sha == "" {
		return "", errors.New("sha is required")
	}

	base, err := stateBase()
	if err != nil {
		return "", err
	}

	return filepath.Join(base, repo, sha+".json"), nil
}

func stateBase() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "lightwave", "dev-gate"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".local", "state", "lightwave", "dev-gate"), nil
}

// Write persists report to disk via tmp+rename so concurrent readers
// never see a partial value. Returns the path written. Takes a pointer
// to keep the value off the call stack — Report is ~80 bytes.
func Write(r *Report) (string, error) {
	if r == nil {
		return "", errors.New("report is nil")
	}

	if r.SHA == "" {
		return "", errors.New("report.SHA is required")
	}

	if r.Repo == "" {
		return "", errors.New("report.Repo is required")
	}

	path, err := StatePath(r.Repo, r.SHA)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), stateDirMode); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, stateFileMode); err != nil {
		return "", err
	}

	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}

	return path, nil
}

// Load reads the report stored for (repo, sha). Returns os.ErrNotExist
// (wrapped) when no report has been written for that sha yet — callers
// distinguish "never ran the gate" from "gate ran and failed".
func Load(repo, sha string) (Report, error) {
	path, err := StatePath(repo, sha)
	if err != nil {
		return Report{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, err
	}

	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return Report{}, fmt.Errorf("parse %s: %w", path, err)
	}

	return r, nil
}

// RequireGreenForHEAD is the egress guard. Mutating commands call it
// before doing anything externally visible. Returns (true, "") when the
// gate report for (repo, headSHA) exists AND has overall == pass AND
// records the same sha that was passed in. Otherwise returns
// (false, <single-line reason with the remediation command>).
//
// "Stale" — i.e. a green report exists but for a different sha — is
// treated as not-green: HEAD has moved since the gate was run, so the
// signal can't be trusted.
func RequireGreenForHEAD(repo, headSHA string) (bool, string) {
	r, err := Load(repo, headSHA)
	if errors.Is(err, os.ErrNotExist) {
		return false, fmt.Sprintf("no gate report for %s@%s — run `lw local gate`", repo, shortSHA(headSHA))
	}

	if err != nil {
		return false, fmt.Sprintf("gate report unreadable: %v — rerun `lw local gate`", err)
	}

	if r.SHA != headSHA {
		return false, fmt.Sprintf("gate report sha %s does not match HEAD %s — rerun `lw local gate`", shortSHA(r.SHA), shortSHA(headSHA))
	}

	if r.Overall != StatusPass {
		failing := firstFailingStage(&r)
		if failing == "" {
			return false, "gate report overall=fail — rerun `lw local gate` and inspect"
		}

		return false, fmt.Sprintf("gate stage %q failed — fix and rerun `lw local gate`", failing)
	}

	return true, ""
}

func firstFailingStage(r *Report) string {
	for name, st := range r.Stages {
		if st.Status == StatusFail {
			return name
		}
	}

	return ""
}

func shortSHA(s string) string {
	if len(s) > shortSHALen {
		return s[:shortSHALen]
	}

	return s
}
