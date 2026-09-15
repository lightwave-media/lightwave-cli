package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/lightwave-media/lightwave-cli/internal/cron"
)

func init() {
	RegisterHandler("cron.list", cronListHandler)
}

// cronListHandler reports the stamped scheduled jobs beside the launchd fleet.
//
// Declared in lightwave-core's interfaces/cli/commands.yaml as
// `cron list --json`, `_status: in_development` with lightwave-cli#302 as its
// tracking ref. `--json` is the only flag the stamp declares and the only one
// read here — reading an undeclared flag is the #367 defect, where the
// dispatcher never registers it and the read silently returns the default.
func cronListHandler(ctx context.Context, _ []string, flags map[string]any) error {
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	jobs, err := cron.LoadJobs(cfg.Paths.LightwaveRoot)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}

	agents, err := cron.LoadAgents(ctx, home)
	if err != nil {
		return err
	}

	report := cron.Reconcile(jobs, agents, shippedVerbs())

	if flagBool(flags, "json") {
		out, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}

		fmt.Println(string(out))

		return nil
	}

	writeCronReport(report)

	return nil
}

// shippedVerbs is the set of top-level commands this binary exposes.
//
// Taken from the assembled cobra tree rather than from `lw <verb> --help`'s
// exit status. An unknown verb makes cobra print root help and exit 0, so an
// exit-status probe reports every conceivable verb as present — that false
// positive is what hid the missing-handler finding until someone parsed the
// command list instead (scheduled_job_drift.py says so in its own comment).
func shippedVerbs() map[string]bool {
	verbs := make(map[string]bool)

	for _, c := range rootCmd.Commands() {
		if c.IsAvailableCommand() {
			verbs[c.Name()] = true
		}
	}

	return verbs
}

func writeCronReport(report cron.Report) {
	fmt.Printf("scheduled jobs — %d declared, %d launchd agent(s)\n\n",
		report.Counts[cron.VerdictScheduled]+report.Counts[cron.VerdictDeclaredNotScheduled],
		report.Counts[cron.VerdictScheduled]+report.Counts[cron.VerdictAgentNotDeclared])

	for _, row := range report.Rows {
		switch row.Verdict {
		case cron.VerdictScheduled:
			fmt.Printf("  %s %-30s %-14s %s\n",
				color.GreenString("OK  "), row.JobID, row.Schedule, row.Label)
		case cron.VerdictDeclaredNotScheduled:
			fmt.Printf("  %s %-30s %-14s %s\n",
				color.YellowString("GAP "), row.JobID, row.Schedule,
				color.YellowString("declared, nothing schedules it"))
		case cron.VerdictAgentNotDeclared:
			fmt.Printf("  %s %-30s %-14s %s\n",
				color.CyanString("EXTRA"), row.Label, "",
				"runs, not declared in the stamp")
		}

		if row.VerbMissing != "" {
			fmt.Printf("       %s handler runs `lw %s`, which this build does not expose\n",
				color.RedString("DEAD"), row.VerbMissing)
		}
	}

	fmt.Println()

	if report.UnrunnableJobs > 0 {
		fmt.Println(color.RedString(
			"%d declared job(s) invoke a verb that does not exist — they cannot have run.",
			report.UnrunnableJobs))
	}

	if report.Counts[cron.VerdictDeclaredNotScheduled] > 0 {
		fmt.Printf("%d declared job(s) have no launchd agent. `lw cron sync` would render them; it is not built yet (#302).\n",
			report.Counts[cron.VerdictDeclaredNotScheduled])
	}

	if report.Counts[cron.VerdictScheduled] == 0 &&
		report.Counts[cron.VerdictAgentNotDeclared] > 0 {
		fmt.Println(color.YellowString(
			"No declared job matched a launchd agent. The fleet is entirely hand-maintained."))
	}
}
