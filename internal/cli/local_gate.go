package cli

import (
	"context"
	"fmt"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/git"
	"github.com/lightwave-media/lightwave-cli/internal/local/gate"
	"github.com/lightwave-media/lightwave-cli/internal/repo"
)

// shortSHADisplayLen matches the gate package's short-sha length.
const shortSHADisplayLen = 8

func init() {
	RegisterHandler("local.gate", localGateHandler)
}

// localGateHandler runs the composite gate for the detected repo:
//
//	doctor → fmt → lint → test → test-harness (sys only) → act
//
// After every stage finishes (or the first one fails) it writes the
// report to ~/.local/state/lightwave/dev-gate/<repo>/<sha>.json and
// prints the terse status table from the persona spec. Exit 0 when
// overall == pass, 1 otherwise.
//
// Honors --json to emit the report shape on stdout instead of the table.
// Honors --skip <stage> to omit one stage from the run (used during
// migrations when a stage is known-broken and being fixed in parallel).
func localGateHandler(ctx context.Context, _ []string, flags map[string]any) error {
	info, err := detectRepo()
	if err != nil {
		return err
	}

	sha, err := headSHA(info)
	if err != nil {
		return err
	}

	stages := composeStages(info, flags)
	report, runErr := gate.Run(ctx, &gate.Report{Repo: string(info.ID), SHA: sha}, stages)

	path, writeErr := gate.Write(&report)
	if writeErr != nil {
		fmt.Println(color.YellowString("warning: report not persisted: %v", writeErr))
	}

	if flagBool(flags, "json") {
		_ = emitJSON(report)
	} else {
		printGateTable(&report, info, path)
	}

	if runErr != nil {
		return fmt.Errorf("gate failed: %w", runErr)
	}

	if report.Overall != gate.StatusPass {
		return fmt.Errorf("gate failed (overall=%s)", report.Overall)
	}

	return nil
}

// composeStages picks the stage set for the detected repo. fmt runs in
// --check mode under the gate (do not mutate files), but the standalone
// `lw local fmt` defaults to write-back.
func composeStages(info repo.Info, flags map[string]any) []gate.Stage {
	skip := map[string]bool{}
	if s := flagStr(flags, "skip"); s != "" {
		skip[s] = true
	}

	all := []gate.Stage{
		doctorStage(info),
		fmtStage(info, true),
		lintStage(info),
		testStage(info),
		testHarnessStage(info),
		actStage(info),
	}

	out := make([]gate.Stage, 0, len(all))

	for _, s := range all {
		if skip[s.Name] {
			continue
		}

		out = append(out, s)
	}

	return out
}

func headSHA(info repo.Info) (string, error) {
	g := git.NewGit(info.Root)

	sha, err := g.Rev("HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD in %s: %w", info.Root, err)
	}

	return sha, nil
}

func printGateTable(r *gate.Report, info repo.Info, reportPath string) {
	for name, st := range r.Stages {
		mark := color.GreenString("✅")

		switch st.Status {
		case gate.StatusFail:
			mark = color.RedString("❌")
		case gate.StatusSkip:
			mark = color.YellowString("—")
		case gate.StatusPass:
			// keep default mark
		}

		dur := fmt.Sprintf("%dms", st.DurationMS)

		note := st.Notes
		if note != "" {
			note = "  " + color.HiBlackString(note)
		}

		fmt.Printf("▶ %-14s %s  %s%s\n", name, mark, dur, note)
	}

	fmt.Println("─────────────────────────────────────")

	overall := color.GreenString("PASS")
	if r.Overall != gate.StatusPass {
		overall = color.RedString("FAIL")
	}

	fmt.Printf("overall: %s  •  sha: %s  •  repo: %s\n", overall, short(r.SHA), info.ID)

	if reportPath != "" {
		fmt.Printf("report: %s\n", color.HiBlackString(reportPath))
	}

	if r.Overall == gate.StatusPass {
		fmt.Println(color.GreenString("ready for `lw local pr`"))
	}
}

func short(s string) string {
	if len(s) > shortSHADisplayLen {
		return s[:shortSHADisplayLen]
	}

	return s
}
