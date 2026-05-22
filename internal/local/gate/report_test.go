package gate_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/local/gate"
)

// Most gate tests use t.Setenv (XDG_STATE_HOME → temp dir), which Go
// prohibits combining with t.Parallel. The paralleltest linter is
// silenced per-function rather than restructuring the tests, because
// the setenv approach is the simplest way to isolate state-dir writes
// without exporting an internal hook.

//nolint:paralleltest // t.Setenv prevents parallel
func TestWriteThenLoadRoundtrip(t *testing.T) {
	withXDGState(t)

	r := gate.Report{
		Repo: "lightwave-cli",
		SHA:  "deadbeef0000000000000000000000000000abcd",
		Stages: map[string]gate.StageResult{
			"fmt":  {Status: gate.StatusPass, DurationMS: 12},
			"lint": {Status: gate.StatusPass, DurationMS: 340},
		},
		Overall: gate.StatusPass,
		TS:      time.Now().UTC(),
	}

	path, err := gate.Write(&r)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if !strings.HasSuffix(path, ".json") {
		t.Fatalf("path %q does not end in .json", path)
	}

	got, err := gate.Load(r.Repo, r.SHA)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Overall != r.Overall {
		t.Fatalf("Overall = %q, want %q", got.Overall, r.Overall)
	}

	if got.Stages["lint"].DurationMS != 340 {
		t.Fatalf("lint duration = %d, want 340", got.Stages["lint"].DurationMS)
	}
}

//nolint:paralleltest // t.Setenv prevents parallel
func TestRequireGreenForHEAD_NoReport(t *testing.T) {
	withXDGState(t)

	ok, reason := gate.RequireGreenForHEAD("lightwave-cli", "abc123")
	if ok {
		t.Fatalf("expected not-ok when no report exists")
	}

	if !strings.Contains(reason, "no gate report") {
		t.Fatalf("reason = %q, want 'no gate report'", reason)
	}

	if !strings.Contains(reason, "lw local gate") {
		t.Fatalf("reason = %q, want remediation command", reason)
	}
}

//nolint:paralleltest // t.Setenv prevents parallel
func TestRequireGreenForHEAD_StaleSHA(t *testing.T) {
	withXDGState(t)

	_, err := gate.Write(&gate.Report{
		Repo:    "lightwave-cli",
		SHA:     "oldsha111111111111111111111111111111aaaa",
		Stages:  map[string]gate.StageResult{"fmt": {Status: gate.StatusPass}},
		Overall: gate.StatusPass,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	ok, _ := gate.RequireGreenForHEAD("lightwave-cli", "newsha22222222222222222222222222222bbbb")
	if ok {
		t.Fatalf("expected not-ok for stale report")
	}
}

//nolint:paralleltest // t.Setenv prevents parallel
func TestRequireGreenForHEAD_OverallFail(t *testing.T) {
	withXDGState(t)

	sha := "deadbeef00000000000000000000000000001111"

	_, err := gate.Write(&gate.Report{
		Repo: "lightwave-cli",
		SHA:  sha,
		Stages: map[string]gate.StageResult{
			"fmt":  {Status: gate.StatusPass},
			"lint": {Status: gate.StatusFail, Notes: "golangci-lint reported violations"},
		},
		Overall: gate.StatusFail,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	ok, reason := gate.RequireGreenForHEAD("lightwave-cli", sha)
	if ok {
		t.Fatalf("expected not-ok when overall=fail")
	}

	if !strings.Contains(reason, "lint") {
		t.Fatalf("reason = %q, want failing stage name", reason)
	}
}

//nolint:paralleltest // t.Setenv prevents parallel
func TestRequireGreenForHEAD_HappyPath(t *testing.T) {
	withXDGState(t)

	sha := "happy00000000000000000000000000000002222"

	_, err := gate.Write(&gate.Report{
		Repo:    "lightwave-cli",
		SHA:     sha,
		Stages:  map[string]gate.StageResult{"fmt": {Status: gate.StatusPass}},
		Overall: gate.StatusPass,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	ok, reason := gate.RequireGreenForHEAD("lightwave-cli", sha)
	if !ok {
		t.Fatalf("expected ok, got not-ok: %s", reason)
	}
}

func TestStatePath_RequiresRepoAndSHA(t *testing.T) {
	t.Parallel()

	if _, err := gate.StatePath("", "abc"); err == nil {
		t.Fatalf("expected error for empty repo")
	}

	if _, err := gate.StatePath("repo", ""); err == nil {
		t.Fatalf("expected error for empty sha")
	}
}

func TestRun_AllPass(t *testing.T) {
	t.Parallel()

	stages := []gate.Stage{
		{Name: "a", Run: func(_ context.Context) (gate.StageResult, error) {
			return gate.StageResult{Status: gate.StatusPass}, nil
		}},
		{Name: "b", Run: func(_ context.Context) (gate.StageResult, error) {
			return gate.StageResult{Status: gate.StatusPass}, nil
		}},
	}

	r, err := gate.Run(context.Background(), &gate.Report{Repo: "x", SHA: "y"}, stages)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if r.Overall != gate.StatusPass {
		t.Fatalf("Overall = %q, want pass", r.Overall)
	}

	if r.Stages["a"].Status != gate.StatusPass || r.Stages["b"].Status != gate.StatusPass {
		t.Fatalf("stages = %+v", r.Stages)
	}
}

func TestRun_StopsOnFirstFail(t *testing.T) {
	t.Parallel()

	bRan := false

	stages := []gate.Stage{
		{Name: "a", Run: func(_ context.Context) (gate.StageResult, error) {
			return gate.StageResult{}, errors.New("boom")
		}},
		{Name: "b", Run: func(_ context.Context) (gate.StageResult, error) {
			bRan = true
			return gate.StageResult{Status: gate.StatusPass}, nil
		}},
	}

	r, err := gate.Run(context.Background(), &gate.Report{Repo: "x", SHA: "y"}, stages)
	if err == nil {
		t.Fatalf("expected Run to return the first error")
	}

	if bRan {
		t.Fatalf("stage b ran despite stage a failing")
	}

	if r.Overall != gate.StatusFail {
		t.Fatalf("Overall = %q, want fail", r.Overall)
	}

	if r.Stages["a"].Status != gate.StatusFail {
		t.Fatalf("a status = %q, want fail", r.Stages["a"].Status)
	}

	if r.Stages["b"].Status != gate.StatusSkip {
		t.Fatalf("b status = %q, want skip", r.Stages["b"].Status)
	}
}

func TestRun_RequiresRepoAndSHA(t *testing.T) {
	t.Parallel()

	if _, err := gate.Run(context.Background(), &gate.Report{SHA: "x"}, nil); err == nil {
		t.Fatalf("expected error when repo missing")
	}

	if _, err := gate.Run(context.Background(), &gate.Report{Repo: "x"}, nil); err == nil {
		t.Fatalf("expected error when sha missing")
	}
}

func withXDGState(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
}
