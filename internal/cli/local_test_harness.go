package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/lightwave-media/lightwave-cli/internal/local/gate"
	"github.com/lightwave-media/lightwave-cli/internal/repo"
)

var errSysDevHarness = errors.New("dev/test-harness.sh not found for lightwave-sys")

func init() {
	RegisterHandler("local.test-harness", localTestHarnessHandler)
}

// localTestHarnessHandler runs lightwave-sys/dev/test-harness.sh — the
// 10-test smoke that exercises gateway REST + WS, memory persistence,
// session state, config surface, and brain.db durability. Only meaningful
// in the lightwave-sys repo; other repos skip cleanly.
func localTestHarnessHandler(ctx context.Context, _ []string, _ map[string]any) error {
	info, err := detectRepo()
	if err != nil {
		return err
	}

	if info.ID != repo.LightwaveSys {
		stageNotImplemented("test-harness", info)
		return nil
	}

	return runTestHarness(ctx, info)
}

func runTestHarness(ctx context.Context, info repo.Info) error {
	script := sysDevScript(info, "test-harness.sh")
	if script == "" {
		return errSysDevHarness
	}

	if err := runStreaming(ctx, info.Root, script); err != nil {
		return remediation(fmt.Errorf("test-harness.sh: %w", err),
			"inspect the failed test; rerun `lw local test-harness` after fixing")
	}

	return nil
}

func testHarnessStage(info repo.Info) gate.Stage {
	return gate.Stage{
		Name: "test_harness",
		Run: func(ctx context.Context) (gate.StageResult, error) {
			if info.ID != repo.LightwaveSys {
				return skipResult("test-harness only defined for lightwave-sys"), nil
			}

			if err := runTestHarness(ctx, info); err != nil {
				return gate.StageResult{Status: gate.StatusFail, Notes: "run `lw local test-harness`"}, err
			}

			return gate.StageResult{Status: gate.StatusPass, Notes: "10/10 harness checks passed"}, nil
		},
	}
}
