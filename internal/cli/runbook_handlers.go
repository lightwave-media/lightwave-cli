package cli

// runbook_handlers.go — dispatcher registration for the runbook verbs.
//
// The five verbs shipped in #335 on the hardcoded cobra tree in runbook.go.
// lightwave-core then published the domain (removed `_status: in_development`)
// with a note that the dispatcher "will attach them when RegisterHandler is
// wired". This is that wiring — without it `lw check schema` reports five
// missing handlers for commands that work, which is exactly the cobra-tree
// blind spot tracked in #337 and the reason the newly armed gate failed.
//
// Each handler copies the dispatcher's flag map into the package-level vars the
// cobra commands bind, then delegates to the same run function. The command var
// is passed rather than nil because the run functions write through
// cmd.OutOrStdout().

import "context"

func init() {
	RegisterHandler("runbook.start", runbookStartHandler)
	RegisterHandler("runbook.status", runbookStatusHandler)
	RegisterHandler("runbook.apply", runbookApplyHandler)
	RegisterHandler("runbook.step-complete", runbookStepCompleteHandler)
	RegisterHandler("runbook.cancel", runbookCancelHandler)
}

func runbookStartHandler(ctx context.Context, _ []string, flags map[string]any) error {
	runbookSlug = flagStr(flags, "slug")
	runbookAgent = flagStr(flags, "agent")
	runbookTask = flagStr(flags, "task")
	runbookRepo = flagStr(flags, "repo")
	runbookBranch = flagStr(flags, "branch")
	runbookSession = flagStr(flags, "session")
	runbookDryRun = flagBool(flags, "dry-run")

	runbookStartCmd.SetContext(ctx)

	return runRunbookStart(runbookStartCmd, nil)
}

func runbookStatusHandler(ctx context.Context, _ []string, flags map[string]any) error {
	runbookTask = flagStr(flags, "task")
	runbookInstance = flagStr(flags, "instance")
	runbookRequire = flagStr(flags, "require")

	runbookStatusCmd.SetContext(ctx)

	return runRunbookStatus(runbookStatusCmd, nil)
}

func runbookApplyHandler(ctx context.Context, _ []string, flags map[string]any) error {
	runbookTask = flagStr(flags, "task")
	runbookInstance = flagStr(flags, "instance")
	runbookCwd = flagStr(flags, "cwd")

	runbookApplyCmd.SetContext(ctx)

	// contextcheck traces runRunbookApply -> Apply -> runCheck and wants ctx as
	// a parameter. It is threaded, via SetContext above, which the analyzer
	// cannot follow. Giving Apply an explicit ctx parameter is the real fix, but
	// it changes the runbook package's API and that file is being actively
	// worked (#325) — not something to land inside a CI-arming change.
	//nolint:contextcheck // ctx threaded via cmd.Context(); see #325 for the signature change
	return runRunbookApply(runbookApplyCmd, nil)
}

func runbookStepCompleteHandler(ctx context.Context, _ []string, flags map[string]any) error {
	runbookTask = flagStr(flags, "task")
	runbookInstance = flagStr(flags, "instance")
	runbookStep = flagStr(flags, "step")
	runbookSignoffTier = flagStr(flags, "signoff-tier")

	runbookStepCompleteCmd.SetContext(ctx)

	return runRunbookStepComplete(runbookStepCompleteCmd, nil)
}

func runbookCancelHandler(ctx context.Context, _ []string, flags map[string]any) error {
	runbookTask = flagStr(flags, "task")
	runbookInstance = flagStr(flags, "instance")
	runbookReason = flagStr(flags, "reason")

	runbookCancelCmd.SetContext(ctx)

	return runRunbookCancel(runbookCancelCmd, nil)
}
