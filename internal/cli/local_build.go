package cli

import (
	"context"
	"fmt"

	"github.com/lightwave-media/lightwave-cli/internal/repo"
)

func init() {
	RegisterHandler("local.build", localBuildHandler)
}

// localBuildHandler runs a repo-native compile/build.
//
//	lightwave-cli  → go build ./...
//	lightwave-sys  → dev/ci.sh build  (release build in Docker)
//
// Build is included in `lw local gate` only when the persona spec calls
// for it (release tags). For day-to-day inner loop, lint+test+act are the
// gates. The composite picks up build via the per-repo stage builders.
func localBuildHandler(ctx context.Context, _ []string, _ map[string]any) error {
	info, err := detectRepo()
	if err != nil {
		return err
	}

	return runBuild(ctx, info)
}

func runBuild(ctx context.Context, info repo.Info) error {
	switch info.ID {
	case repo.LightwaveCLI:
		if err := runStreaming(ctx, info.Root, "go", "build", "./..."); err != nil {
			return remediation(fmt.Errorf("go build: %w", err), "fix compile errors and rerun `lw local build`")
		}

		return nil
	case repo.LightwaveSys:
		script := sysDevScript(info, "ci.sh")
		if script == "" {
			return errSysDevCISh
		}

		if err := runStreaming(ctx, info.Root, script, "build"); err != nil {
			return remediation(fmt.Errorf("dev/ci.sh build: %w", err), "fix compile errors and rerun `lw local build`")
		}

		return nil
	case repo.LightwavePlatform, repo.LightwaveMedia, repo.LightwaveCore, repo.LightwaveUI, repo.Other:
		stageNotImplemented("build", info)
		return nil
	}

	return nil
}
