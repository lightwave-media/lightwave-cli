package cli

import (
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/observability"
)

func emitOperatorCLI(verb, outcome, detail string, exitCode int, start time.Time, measurements map[string]any) {
	_ = observability.RecordOperatorCLI(&observability.OperatorCLIEvent{
		Verb:         verb,
		Outcome:      outcome,
		DurationMS:   time.Since(start).Milliseconds(),
		ExitCode:     exitCode,
		Detail:       detail,
		Measurements: measurements,
	})
}
