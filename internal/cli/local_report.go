package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/local/gate"
)

// exitNoReport is the conventional exit code for "no gate report exists
// for HEAD" — same code as exitMissingTool in local_act.go (it's a
// missing artifact, not a gate failure).
const exitNoReport = 2

func init() {
	RegisterHandler("local.report", localReportHandler)
}

// localReportHandler prints the gate report for the current repo's HEAD.
// Default output is the same terse table `lw local gate` emits; --json
// prints the raw report shape (for tooling).
//
// Exit codes:
//
//	0 — report exists and overall == pass
//	1 — report exists and overall != pass (or stale vs HEAD)
//	2 — no report on disk (gate has never been run for HEAD)
func localReportHandler(_ context.Context, _ []string, flags map[string]any) error {
	info, err := detectRepo()
	if err != nil {
		return err
	}

	sha, err := headSHA(info)
	if err != nil {
		return err
	}

	r, err := gate.Load(string(info.ID), sha)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, color.YellowString("no gate report for %s@%s — run `lw local gate`", info.ID, short(sha)))
			os.Exit(exitNoReport)
		}

		return fmt.Errorf("load report: %w", err)
	}

	if flagBool(flags, "json") {
		return emitJSON(r)
	}

	path, _ := gate.StatePath(string(info.ID), sha)
	printGateTable(&r, info, path)

	if r.Overall != gate.StatusPass {
		os.Exit(1)
	}

	return nil
}
