package cli

import (
	"context"
	"fmt"

	"github.com/lightwave-media/lightwave-cli/internal/local/gate"
	"github.com/lightwave-media/lightwave-cli/internal/repo"
)

func init() {
	RegisterHandler("local.fmt", localFmtHandler)
}

// localFmtHandler runs the repo-native formatter.
//
//	lightwave-cli  → gofmt -l -w .   (or -l only with --check)
//	lightwave-sys  → cargo fmt --all (--check passes --check)
//
// Other repos fall back to "skip" — formatters are language-specific and
// the persona only enforces them for the two MVP repos.
func localFmtHandler(ctx context.Context, _ []string, flags map[string]any) error {
	info, err := detectRepo()
	if err != nil {
		return err
	}

	check := flagBool(flags, "check")

	switch info.ID {
	case repo.LightwaveCLI:
		return runFmtCLI(ctx, info, check)
	case repo.LightwaveSys:
		return runFmtSys(ctx, info, check)
	case repo.LightwavePlatform, repo.LightwaveMedia, repo.LightwaveCore, repo.LightwaveUI, repo.Other:
		stageNotImplemented("fmt", info)
		return nil
	}

	return nil
}

func runFmtCLI(ctx context.Context, info repo.Info, check bool) error {
	args := []string{"-l"}
	if !check {
		args = append(args, "-w")
	}

	args = append(args, ".")

	if err := runStreaming(ctx, info.Root, "gofmt", args...); err != nil {
		hint := "lw local fmt"
		if check {
			hint = "lw local fmt  # rerun without --check to apply"
		}

		return remediation(fmt.Errorf("gofmt: %w", err), hint)
	}

	return nil
}

func runFmtSys(ctx context.Context, info repo.Info, check bool) error {
	args := []string{"fmt", "--all"}
	if check {
		args = append(args, "--", "--check")
	}

	if err := runStreaming(ctx, info.Root, "cargo", args...); err != nil {
		return remediation(fmt.Errorf("cargo fmt: %w", err), "lw local fmt")
	}

	return nil
}

func fmtStage(info repo.Info, check bool) gate.Stage {
	return gate.Stage{
		Name: "fmt",
		Run: func(ctx context.Context) (gate.StageResult, error) {
			switch info.ID {
			case repo.LightwaveCLI:
				if err := runFmtCLI(ctx, info, check); err != nil {
					return gate.StageResult{Status: gate.StatusFail, Notes: "run `lw local fmt`"}, err
				}
			case repo.LightwaveSys:
				if err := runFmtSys(ctx, info, check); err != nil {
					return gate.StageResult{Status: gate.StatusFail, Notes: "run `lw local fmt`"}, err
				}
			case repo.LightwavePlatform, repo.LightwaveMedia, repo.LightwaveCore, repo.LightwaveUI, repo.Other:
				return skipResult(fmt.Sprintf("fmt not defined for repo %s", info.ID)), nil
			}

			return gate.StageResult{Status: gate.StatusPass}, nil
		},
	}
}
