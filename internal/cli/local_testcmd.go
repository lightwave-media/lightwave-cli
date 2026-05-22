package cli

import (
	"context"
	"fmt"

	"github.com/lightwave-media/lightwave-cli/internal/local/gate"
	"github.com/lightwave-media/lightwave-cli/internal/repo"
)

func init() {
	RegisterHandler("local.test", localTestHandler)
}

// localTestHandler runs the repo-native test suite.
//
//	lightwave-cli  → go test ./...
//	lightwave-sys  → dev/ci.sh test  (single-threaded inside the CI image)
//
// Honors a --pkg flag for scoping (Go only).
func localTestHandler(ctx context.Context, _ []string, flags map[string]any) error {
	info, err := detectRepo()
	if err != nil {
		return err
	}

	pkg := flagStr(flags, "pkg")

	return runTest(ctx, info, pkg)
}

func runTest(ctx context.Context, info repo.Info, pkg string) error {
	switch info.ID {
	case repo.LightwaveCLI:
		target := "./..."
		if pkg != "" {
			target = pkg
		}

		if err := runStreaming(ctx, info.Root, "go", "test", target); err != nil {
			return remediation(fmt.Errorf("go test: %w", err), "fix the failing test and rerun `lw local test`")
		}

		return nil
	case repo.LightwaveSys:
		script := sysDevScript(info, "ci.sh")
		if script == "" {
			return errSysDevCISh
		}

		if err := runStreaming(ctx, info.Root, script, "test"); err != nil {
			return remediation(fmt.Errorf("dev/ci.sh test: %w", err), "fix the failing test and rerun `lw local test`")
		}

		return nil
	case repo.LightwavePlatform, repo.LightwaveMedia, repo.LightwaveCore, repo.LightwaveUI, repo.Other:
		stageNotImplemented("test", info)
		return nil
	}

	return nil
}

func testStage(info repo.Info) gate.Stage {
	return gate.Stage{
		Name: "test",
		Run: func(ctx context.Context) (gate.StageResult, error) {
			switch info.ID {
			case repo.LightwaveCLI, repo.LightwaveSys:
				if err := runTest(ctx, info, ""); err != nil {
					return gate.StageResult{Status: gate.StatusFail, Notes: "run `lw local test`"}, err
				}

				return gate.StageResult{Status: gate.StatusPass}, nil
			case repo.LightwavePlatform, repo.LightwaveMedia, repo.LightwaveCore, repo.LightwaveUI, repo.Other:
				return skipResult(fmt.Sprintf("test not defined for repo %s", info.ID)), nil
			}

			return gate.StageResult{Status: gate.StatusPass}, nil
		},
	}
}
