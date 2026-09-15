// Package cron reconciles the scheduled jobs the stamp declares against the
// launchd agents this machine actually runs.
//
// Nothing had ever compared the two. The stamp declares jobs under
// workflows/scheduled_jobs/ — id, cron schedule, handler command, owning
// persona — and the LaunchAgent fleet is hand-maintained, so the two drift
// silently and neither side knows. Measured 2026-09-13 by
// ~/.lightwave/lib/maintenance/scheduled_job_drift.py: two declared jobs name a
// handler verb that does not exist, so they have never run once and their
// observability channels were never created.
//
// That is CLAUDE.md §17's declared-vs-registered finding one layer out. §17
// asked whether every declared ENFORCEMENT surface is registered; this asks
// whether every declared SCHEDULED surface runs. Both failures look identical
// from outside: a quiet machine that is either healthy or not doing the work.
//
// This package is pure. It takes jobs and agents as values and returns a
// verdict, so the reconciliation is testable without a filesystem, without
// launchd, and without a lightwave-core checkout.
package cron

import (
	"sort"
	"strings"
)

// Job is one scheduled job as the stamp declares it.
type Job struct {
	// ID is the stable slug, e.g. "notion_knowledge_hygiene".
	ID string
	// Title is the human label.
	Title string
	// Schedule is the declared cron expression, e.g. "*/30 * * * *".
	Schedule string
	// Transport is handler.transport; only "cli" is meaningful here.
	Transport string
	// Command is handler.command verbatim, e.g. "lw knowledge sync --json".
	Command string
	// Source is the stamp key the job was read from, for reporting.
	Source string
}

// Agent is one launchd agent found on this machine.
type Agent struct {
	// Label is the plist Label, e.g. "com.lightwave.pr-review".
	Label string
	// Program is the flattened ProgramArguments, for command matching.
	Program string
	// Loaded reports whether launchctl currently lists it.
	Loaded bool
}

// Verdict is what reconciliation concluded about one row.
type Verdict string

const (
	// VerdictScheduled — the declared job's command is run by an agent.
	VerdictScheduled Verdict = "scheduled"
	// VerdictDeclaredNotScheduled — declared, but no agent runs it.
	VerdictDeclaredNotScheduled Verdict = "declared-not-scheduled"
	// VerdictAgentNotDeclared — an agent runs something the stamp never declared.
	VerdictAgentNotDeclared Verdict = "agent-not-declared"
)

// Row is one line of the reconciliation.
type Row struct {
	Verdict Verdict `json:"verdict"`
	// JobID is empty for an undeclared agent.
	JobID string `json:"job_id,omitempty"`
	// Label is empty for a declared job nothing schedules.
	Label    string `json:"label,omitempty"`
	Schedule string `json:"schedule,omitempty"`
	Command  string `json:"command,omitempty"`
	// VerbMissing names the `lw <verb>` a declared job invokes that this
	// binary does not expose. A job can be scheduled and still do nothing,
	// which is the worse failure because launchd reports success either way.
	VerbMissing string `json:"verb_missing,omitempty"`
	Loaded      bool   `json:"loaded,omitempty"`
}

// Report is the whole reconciliation.
type Report struct {
	Counts map[Verdict]int `json:"counts"`
	Rows   []Row           `json:"rows"`
	// UnrunnableJobs counts declared jobs whose handler verb does not exist.
	UnrunnableJobs int `json:"unrunnable_jobs"`
}

// HandlerVerb extracts the `lw <verb>` a handler command invokes.
//
// Returns "" when the command does not start with `lw`, which is not a defect
// — a job may legitimately run a script. Only `lw` commands can be checked
// against this binary's own surface.
func HandlerVerb(command string) string {
	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "lw" {
		return ""
	}

	verb := fields[1]
	if strings.HasPrefix(verb, "-") {
		return ""
	}

	return verb
}

// Reconcile pairs declared jobs with the agents that run them.
//
// Matching is on the declared COMMAND appearing in an agent's program
// arguments, not on a label convention. That is deliberate: no stamp declares
// how a job id maps to a launchd Label — cron_job.yaml describes jobs that
// "v_core dispatches on schedule" and predates launchd here — so a label-based
// match would mean inventing a convention, which is a stamp-shaped decision
// this package has no standing to make (CLAUDE.md §7).
//
// knownVerbs is the set of top-level commands this binary exposes. Pass nil to
// skip the verb check.
func Reconcile(jobs []Job, agents []Agent, knownVerbs map[string]bool) Report {
	report := Report{Counts: map[Verdict]int{}}

	matchedAgent := make(map[string]bool, len(agents))

	for _, job := range jobs {
		row := Row{
			JobID:    job.ID,
			Schedule: job.Schedule,
			Command:  job.Command,
			Verdict:  VerdictDeclaredNotScheduled,
		}

		if verb := HandlerVerb(job.Command); verb != "" && knownVerbs != nil && !knownVerbs[verb] {
			row.VerbMissing = verb
			report.UnrunnableJobs++
		}

		if agent, ok := agentRunning(agents, job.Command); ok {
			row.Verdict = VerdictScheduled
			row.Label = agent.Label
			row.Loaded = agent.Loaded
			matchedAgent[agent.Label] = true
		}

		report.Rows = append(report.Rows, row)
		report.Counts[row.Verdict]++
	}

	for _, agent := range agents {
		if matchedAgent[agent.Label] {
			continue
		}

		report.Rows = append(report.Rows, Row{
			Verdict: VerdictAgentNotDeclared,
			Label:   agent.Label,
			Loaded:  agent.Loaded,
		})
		report.Counts[VerdictAgentNotDeclared]++
	}

	sort.SliceStable(report.Rows, func(i, j int) bool {
		if rank(report.Rows[i].Verdict) != rank(report.Rows[j].Verdict) {
			return rank(report.Rows[i].Verdict) < rank(report.Rows[j].Verdict)
		}

		return report.Rows[i].JobID+report.Rows[i].Label < report.Rows[j].JobID+report.Rows[j].Label
	})

	return report
}

// rank orders verdicts most-actionable-first.
//
// Explicit rather than alphabetical on the Verdict string: sorting by the
// string puts "agent-not-declared" first purely because 'a' sorts before 'd',
// which buries the one row a reader has to do something about under a list of
// agents that are merely undeclared. The order a report chooses is a claim
// about what matters, so it should be made on purpose.
func rank(v Verdict) int {
	switch v {
	case VerdictDeclaredNotScheduled:
		return rankGap // declared and not running — the gap this verb exists to find
	case VerdictScheduled:
		return rankWorking
	case VerdictAgentNotDeclared:
		return rankInformational // running but unstamped
	default:
		return rankUnknown
	}
}

const (
	rankGap = iota
	rankWorking
	rankInformational
	rankUnknown
)

// agentRunning finds an agent whose program arguments contain the command.
//
// The declared command carries flags ("lw knowledge sync --json") and a plist
// may add its own, so this is a containment test on the declared string rather
// than equality.
func agentRunning(agents []Agent, command string) (Agent, bool) {
	needle := strings.TrimSpace(command)
	if needle == "" {
		return Agent{}, false
	}

	for _, agent := range agents {
		if strings.Contains(agent.Program, needle) {
			return agent, true
		}
	}

	return Agent{}, false
}
