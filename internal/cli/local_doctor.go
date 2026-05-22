package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/local/gate"
	"github.com/lightwave-media/lightwave-cli/internal/repo"
)

// versionPrefixLines is the number of lines from a `--version` invocation
// to display. The first line is what every tool prints as the canonical
// version string.
const versionPrefixLines = 2

func init() {
	RegisterHandler("local.doctor", localDoctorHandler)
}

// localDoctorHandler is the first stage of `lw local gate` and a useful
// standalone command. It probes the host for the tools each stage will
// later invoke; if a tool is missing here, the failing stage downstream
// gets a clearer signal than "exec: not found".
//
// For lightwave-cli: go, golangci-lint, mise, gh, git.
// For lightwave-sys: cargo, docker, zeroclaw, gh, git.
// For everything else: git + gh + the language toolchain we can detect.
//
// Result is always printed; exits non-zero when any required tool is
// missing or any health probe fails.
func localDoctorHandler(ctx context.Context, _ []string, _ map[string]any) error {
	info, err := detectRepo()
	if err != nil {
		return err
	}

	res, runErr := runDoctor(ctx, info)
	printDoctor(info, res)

	return runErr
}

type doctorCheck struct {
	Name   string
	Detail string
	Fix    string
	OK     bool
}

func runDoctor(ctx context.Context, info repo.Info) ([]doctorCheck, error) {
	var checks []doctorCheck

	checks = append(checks,
		probeBinary(ctx, "git", "--version", ""),
		probeBinary(ctx, "gh", "--version", "brew install gh && gh auth login"),
	)

	switch info.ID {
	case repo.LightwaveCLI:
		checks = append(checks,
			probeBinary(ctx, "go", "version", "brew install go"),
			probeBinary(ctx, "golangci-lint", "version", "mise install golangci-lint"),
			probeBinary(ctx, "mise", "--version", "brew install mise"),
		)
	case repo.LightwaveSys:
		checks = append(checks,
			probeBinary(ctx, "cargo", "--version", "rustup install stable"),
			probeBinary(ctx, "docker", "version", "open -a Docker"),
		)

		if commandExists("zeroclaw") {
			checks = append(checks, probeBinary(ctx, "zeroclaw", "--version", ""))
		}
	case repo.LightwavePlatform, repo.LightwaveMedia, repo.LightwaveCore, repo.LightwaveUI, repo.Other:
		// No additional repo-specific probes.
	}

	failed := 0

	for _, c := range checks {
		if !c.OK {
			failed++
		}
	}

	if failed > 0 {
		return checks, fmt.Errorf("%d tool(s) missing or unhealthy", failed)
	}

	return checks, nil
}

func probeBinary(ctx context.Context, name, versionArg, fixHint string) doctorCheck {
	if !commandExists(name) {
		return doctorCheck{Name: name, OK: false, Detail: "not on PATH", Fix: fixHint}
	}

	out, err := exec.CommandContext(ctx, name, versionArg).CombinedOutput()
	if err != nil {
		return doctorCheck{Name: name, OK: false, Detail: strings.TrimSpace(string(out)), Fix: fixHint}
	}

	first := strings.SplitN(strings.TrimSpace(string(out)), "\n", versionPrefixLines)[0]

	return doctorCheck{Name: name, OK: true, Detail: first}
}

func printDoctor(info repo.Info, checks []doctorCheck) {
	fmt.Printf("%s %s @ %s\n", color.CyanString("repo:"), info.ID, info.Root)

	for _, c := range checks {
		mark := color.GreenString("✓")
		if !c.OK {
			mark = color.RedString("✗")
		}

		fmt.Printf("  %s %-16s %s\n", mark, c.Name, c.Detail)

		if !c.OK && c.Fix != "" {
			fmt.Printf("      %s %s\n", color.YellowString("→ fix:"), c.Fix)
		}
	}
}

// doctorStage builds a gate.Stage that runs the same probes as the
// handler. Used by `lw local gate`.
func doctorStage(info repo.Info) gate.Stage {
	return gate.Stage{
		Name: "doctor",
		Run: func(ctx context.Context) (gate.StageResult, error) {
			checks, err := runDoctor(ctx, info)
			if err != nil {
				var missing []string

				for _, c := range checks {
					if !c.OK {
						missing = append(missing, c.Name)
					}
				}

				notes := fmt.Sprintf("missing: %s — run `lw local doctor` for fix hints", strings.Join(missing, ", "))

				return gate.StageResult{Status: gate.StatusFail, Notes: notes}, err
			}

			return gate.StageResult{
				Status: gate.StatusPass,
				Notes:  fmt.Sprintf("%d checks passed", len(checks)),
			}, nil
		},
	}
}

// errNotFoundForRepo is returned when a required script (e.g. dev/ci.sh)
// is not present in the detected repo or the canonical home location.
// Single sentinel keeps the perfsprint linter quiet across the per-stage
// handlers that all need to say the same thing.
var errSysDevCISh = errors.New("dev/ci.sh not found for lightwave-sys")
