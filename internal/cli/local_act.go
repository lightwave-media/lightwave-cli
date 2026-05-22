package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/local/gate"
	"github.com/lightwave-media/lightwave-cli/internal/repo"
)

// exitMissingTool is the conventional exit code for "infrastructure
// problem, not a gate failure" — matches hooks_circuit_breaker.go's
// reservation of code 2 for tool errors.
const exitMissingTool = 2

func init() {
	RegisterHandler("local.act", localActHandler)
}

// localActHandler runs the repo's GitHub Actions workflow locally via the
// `act` runner. This is the parity oracle: if `act -j ci` is green, the
// same workflow YAML will succeed on GHA (modulo runner image differences
// — see persona doc for the parity caveats).
//
// Exit codes:
//
//	0 — act ran and reported success
//	1 — act ran and reported failure
//	2 — act is not installed (tool error, not gate failure)
func localActHandler(ctx context.Context, _ []string, flags map[string]any) error {
	info, err := detectRepo()
	if err != nil {
		return err
	}

	job := flagStrOr(flags, "job", "ci")

	if !commandExists("act") {
		fmt.Fprintln(os.Stderr, color.YellowString("act not installed — `lw local act` can't run"))
		fmt.Fprintln(os.Stderr, "  → fix: brew install act")
		os.Exit(exitMissingTool)
	}

	workflow := defaultWorkflowPath(info)
	if workflow == "" {
		return fmt.Errorf("no .github/workflows/ci.yml under %s", info.Root)
	}

	args := []string{"-W", workflow, "-j", job}

	if err := runStreaming(ctx, info.Root, "act", args...); err != nil {
		return remediation(fmt.Errorf("act -j %s: %w", job, err),
			fmt.Sprintf("inspect failing step; rerun `lw local act --job %s`", job))
	}

	return nil
}

func defaultWorkflowPath(info repo.Info) string {
	candidate := filepath.Join(info.Root, ".github", "workflows", "ci.yml")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}

	return ""
}

var errNoWorkflow = errors.New("no .github/workflows/ci.yml")

func actStage(info repo.Info) gate.Stage {
	return gate.Stage{
		Name: "act",
		Run: func(ctx context.Context) (gate.StageResult, error) {
			if !commandExists("act") {
				return skipResult("act not installed (brew install act)"), nil
			}

			workflow := defaultWorkflowPath(info)
			if workflow == "" {
				return skipResult(errNoWorkflow.Error()), nil
			}

			if err := runStreaming(ctx, info.Root, "act", "-W", workflow, "-j", "ci"); err != nil {
				return gate.StageResult{Status: gate.StatusFail, Notes: "run `lw local act`"}, err
			}

			return gate.StageResult{Status: gate.StatusPass, Notes: "workflow ci.yml green via act"}, nil
		},
	}
}
