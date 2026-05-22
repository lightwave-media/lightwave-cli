package cli

import (
	"context"
	"fmt"

	"github.com/lightwave-media/lightwave-cli/internal/local/gate"
	"github.com/lightwave-media/lightwave-cli/internal/repo"
)

func init() {
	RegisterHandler("local.lint", localLintHandler)
}

// localLintHandler runs the repo-native linter.
//
//	lightwave-cli  → golangci-lint run --new-from-merge-base=origin/main
//	lightwave-sys  → dev/ci.sh lint  (Docker-isolated, matches GHA image)
func localLintHandler(ctx context.Context, _ []string, _ map[string]any) error {
	info, err := detectRepo()
	if err != nil {
		return err
	}

	return runLint(ctx, info)
}

func runLint(ctx context.Context, info repo.Info) error {
	switch info.ID {
	case repo.LightwaveCLI:
		if err := runStreaming(ctx, info.Root, "golangci-lint", "run", "--new-from-merge-base=origin/main"); err != nil {
			return remediation(fmt.Errorf("golangci-lint: %w", err), "fix the reported issues and rerun `lw local lint`")
		}

		return nil
	case repo.LightwaveSys:
		script := sysDevScript(info, "ci.sh")
		if script == "" {
			return errSysDevCISh
		}

		if err := runStreaming(ctx, info.Root, script, "lint"); err != nil {
			return remediation(fmt.Errorf("dev/ci.sh lint: %w", err), "fix the reported issues and rerun `lw local lint`")
		}

		return nil
	case repo.LightwavePlatform, repo.LightwaveMedia, repo.LightwaveCore, repo.LightwaveUI, repo.Other:
		stageNotImplemented("lint", info)
		return nil
	}

	return nil
}

func lintStage(info repo.Info) gate.Stage {
	return gate.Stage{
		Name: "lint",
		Run: func(ctx context.Context) (gate.StageResult, error) {
			switch info.ID {
			case repo.LightwaveCLI, repo.LightwaveSys:
				if err := runLint(ctx, info); err != nil {
					return gate.StageResult{Status: gate.StatusFail, Notes: "run `lw local lint`"}, err
				}

				return gate.StageResult{Status: gate.StatusPass}, nil
			case repo.LightwavePlatform, repo.LightwaveMedia, repo.LightwaveCore, repo.LightwaveUI, repo.Other:
				return skipResult(fmt.Sprintf("lint not defined for repo %s", info.ID)), nil
			}

			return gate.StageResult{Status: gate.StatusPass}, nil
		},
	}
}
