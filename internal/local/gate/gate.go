package gate

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Stage is one step in the composite gate. Run returns the StageResult
// to record. A returned error short-circuits the run (subsequent stages
// skipped) AND marks Overall as fail — the result returned alongside the
// error is still recorded so the report explains the failure.
type Stage struct {
	Run  func(ctx context.Context) (StageResult, error)
	Name string
}

// Run executes stages in order, stopping on the first non-pass result.
// Stages that error out are recorded with Status=fail. Stages downstream
// of a fail are recorded with Status=skip so the report shows which
// gates never ran.
//
// The returned Report has Stages populated for every stage in stages,
// Overall=pass iff every recorded stage is pass, otherwise fail.
// Repo and SHA on the input Report are preserved into the output; TS is
// set to the moment Run returned. Caller passes a *Report so the value
// isn't copied through the heavy struct.
func Run(ctx context.Context, base *Report, stages []Stage) (Report, error) {
	if base == nil {
		return Report{}, errors.New("base is nil")
	}

	if base.Repo == "" {
		return Report{}, errors.New("base.Repo is required")
	}

	if base.SHA == "" {
		return Report{}, errors.New("base.SHA is required")
	}

	if base.Stages == nil {
		base.Stages = make(map[string]StageResult, len(stages))
	}

	var firstErr error

	failed := false

	for _, s := range stages {
		if failed {
			base.Stages[s.Name] = StageResult{Status: StatusSkip, Notes: "skipped after upstream failure"}
			continue
		}

		start := time.Now()
		res, err := s.Run(ctx)
		res.DurationMS = time.Since(start).Milliseconds()

		if err != nil {
			res.Status = StatusFail

			if res.Notes == "" {
				res.Notes = err.Error()
			} else {
				res.Notes = fmt.Sprintf("%s: %v", res.Notes, err)
			}

			if firstErr == nil {
				firstErr = err
			}

			failed = true
		} else if res.Status == "" {
			res.Status = StatusPass
		}

		base.Stages[s.Name] = res

		if res.Status == StatusFail {
			failed = true
		}
	}

	base.TS = time.Now().UTC()

	if failed {
		base.Overall = StatusFail
	} else {
		base.Overall = StatusPass
	}

	return *base, firstErr
}
