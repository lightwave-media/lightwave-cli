package runbook

import "errors"

// Named dead ends from brief 2. Callers print these; they do not invent steps.
var (
	ErrCatalogUnreachable = errors.New("published catalog unreachable")
	ErrNoMatch            = errors.New("no published runbook matches; file a tool-gap, do not improvise")
	ErrOnMain             = errors.New("checkout is main / not the task worktree")
	ErrNotWorktree        = errors.New("checkout is not a task worktree")
	ErrEditionMismatch    = errors.New("edition/hash does not match the published stamp")
	ErrCheckFailed        = errors.New("a check or command failed; do not mark the task done")
	ErrDenied             = errors.New("operator denied or deferred; leave instance waiting")
)
