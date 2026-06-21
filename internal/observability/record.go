// Package observability appends measured CLI events to ~/.lightwave/observability
// and emits agent feedback lessons on failure.
package observability

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	channelOperatorCLI = "operator-cli.jsonl"
	channelReleaseQA   = "release-qa.jsonl"
	feedbackTool       = "lw"
	dirPerm            = 0o755
	filePerm           = 0o644
)

// OperatorCLIEvent is the print shape for ~/.lightwave/observability/operator-cli.jsonl.
// Stamp: lightwave://schemas/data/meta/operator_cli_event
type OperatorCLIEvent struct {
	Measurements map[string]any `json:"measurements,omitempty"`
	TS           string         `json:"ts"`
	Surface      string         `json:"surface"`
	Verb         string         `json:"verb"`
	Outcome      string         `json:"outcome"`
	SessionID    string         `json:"session_id,omitempty"`
	Detail       string         `json:"detail,omitempty"`
	DurationMS   int64          `json:"duration_ms"`
	ExitCode     int            `json:"exit_code"`
}

// ReleaseQAEvent records release_qa_pass.sh matrix outcomes.
type ReleaseQAEvent struct {
	Measurements map[string]any `json:"measurements,omitempty"`
	TS           string         `json:"ts"`
	Outcome      string         `json:"outcome"`
	ArtefactDir  string         `json:"artefact_dir,omitempty"`
	Detail       string         `json:"detail,omitempty"`
	Pass         int            `json:"pass"`
	Fail         int            `json:"fail"`
	Skip         int            `json:"skip"`
}

// ToolFeedbackLesson is written to ~/.lightwave/brain/tool-feedback/lw/ on failure.
type ToolFeedbackLesson struct {
	TS         string `json:"ts"`
	Lesson     string `json:"lesson"`
	Cure       string `json:"cure,omitempty"`
	SourceVerb string `json:"source_verb"`
	Audience   string `json:"audience"`
}

// RecordOperatorCLI appends a measured CLI event and emits agent feedback on failure.
func RecordOperatorCLI(ev *OperatorCLIEvent) error {
	if ev.TS == "" {
		ev.TS = time.Now().UTC().Format(time.RFC3339)
	}

	if ev.Surface == "" {
		ev.Surface = "lw"
	}

	if ev.SessionID == "" {
		ev.SessionID = os.Getenv("LW_SESSION")
	}

	if err := appendJSONL(channelOperatorCLI, ev); err != nil {
		return err
	}

	if ev.Outcome == "fail" {
		_ = appendToolFeedback(&ToolFeedbackLesson{
			TS:         ev.TS,
			Lesson:     fmt.Sprintf("%s failed: %s", ev.Verb, ev.Detail),
			Cure:       cureForVerb(ev.Verb),
			SourceVerb: ev.Verb,
			Audience:   "agent-harness",
		})
	}

	return nil
}

// RecordReleaseQA appends QA matrix measurement and feedback on fail.
func RecordReleaseQA(ev *ReleaseQAEvent) error {
	if ev.TS == "" {
		ev.TS = time.Now().UTC().Format(time.RFC3339)
	}

	if err := appendJSONL(channelReleaseQA, ev); err != nil {
		return err
	}

	if ev.Outcome == "fail" || ev.Fail > 0 {
		_ = appendToolFeedback(&ToolFeedbackLesson{
			TS:         ev.TS,
			Lesson:     fmt.Sprintf("release QA matrix fail=%d skip=%d: %s", ev.Fail, ev.Skip, ev.Detail),
			Cure:       "bash dev/release_qa_pass.sh <artefact_dir>; fix FAIL rows; re-run lw home sync",
			SourceVerb: "release.qa_pass",
			Audience:   "v_qa-engineer",
		})
	}

	return nil
}

func appendJSONL(filename string, v any) error {
	dir := observabilityDir()
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	line, err := json.Marshal(v)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(filepath.Join(dir, filename), os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(append(line, '\n'))

	return err
}

func appendToolFeedback(lesson *ToolFeedbackLesson) error {
	if lesson.TS == "" {
		lesson.TS = time.Now().UTC().Format(time.RFC3339)
	}

	dir := filepath.Join(homeLightwave(), "brain", "tool-feedback", feedbackTool)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}

	name := time.Now().UTC().Format("2006-01-02") + ".jsonl"

	line, err := json.Marshal(lesson)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(append(line, '\n'))

	return err
}

func observabilityDir() string {
	if p := os.Getenv("LW_OBSERVABILITY_DIR"); p != "" {
		return p
	}

	return filepath.Join(homeLightwave(), "observability")
}

func homeLightwave() string {
	if p := os.Getenv("LW_HOME_PRINT"); p != "" {
		return p
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".lightwave")
}

func cureForVerb(verb string) string {
	switch verb {
	case "home.sync":
		return "cd ~/dev && mise run lw:sync"
	case "release.flag":
		return "lw home sync  # refresh flags registry stamp→print"
	case "release.merge":
		return "lw release flag autonomous_qa_release_pass --on; emit 06-qa-release-verdict.md with blockers=0"
	default:
		return "lw home sync && mise run lw:sync"
	}
}

// RecentOperatorEvents reads the tail of operator-cli.jsonl for reporting.
func RecentOperatorEvents(limit int) ([]OperatorCLIEvent, error) {
	path := filepath.Join(observabilityDir(), channelOperatorCLI)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	lines := splitNonEmptyLines(string(data))
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}

	out := make([]OperatorCLIEvent, 0, len(lines))
	for _, line := range lines {
		var ev OperatorCLIEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}

		out = append(out, ev)
	}

	return out, nil
}

func splitNonEmptyLines(s string) []string {
	var out []string

	start := 0

	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			if line != "" {
				out = append(out, line)
			}

			start = i + 1
		}
	}

	return out
}
