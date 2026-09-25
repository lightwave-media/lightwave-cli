package runbook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

// auditRow is one audit_event (lightwave-core data/meta/audit_event.yaml,
// surface=runbook), appended to the channel lightwave-core
// policy/security/shell_tool.yaml names for shell invocations. Every step
// decision leaves one: what ran, how, and what it returned (lightwave-cli#348).
type auditRow struct {
	Metadata      map[string]string `json:"metadata"`
	TS            string            `json:"ts"`
	Surface       string            `json:"surface"`
	AgentID       string            `json:"agent_id"`
	Action        string            `json:"action"`
	Decision      string            `json:"decision"`
	CorrelationID string            `json:"correlation_id"`
	RunbookID     string            `json:"runbook_id"`
	Outcome       string            `json:"outcome"`
}

// DefaultAuditPath is ~/.lightwave/observability/shell.jsonl.
func DefaultAuditPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".lightwave", "observability", "shell.jsonl")
}

// audit appends one row for step's decision. An empty path records nothing.
func audit(path string, inst *Instance, step *Step, decision, outcome string, res stepResult) error {
	if path == "" {
		return nil
	}

	row := auditRow{
		TS:            nowUTC(),
		Surface:       "runbook",
		AgentID:       inst.AgentID,
		Action:        "step_exec",
		Decision:      decision,
		CorrelationID: inst.InstanceID,
		RunbookID:     inst.RunbookSlug,
		Outcome:       outcome,
		Metadata: map[string]string{
			"step":       step.ID,
			"kind":       step.Kind,
			"mode":       res.mode,
			"exit_code":  strconv.Itoa(res.exit),
			"high_blast": strconv.FormatBool(step.HighBlast),
			"dry_run":    strconv.FormatBool(inst.DryRun),
		},
	}

	line, err := json.Marshal(row)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm) //nolint:gosec // the stamped audit channel
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(append(line, '\n'))

	return err
}
