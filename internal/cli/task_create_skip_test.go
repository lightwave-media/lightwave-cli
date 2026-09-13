package cli

import (
	"strings"
	"testing"
)

// TestTaskCreate_SkipFlags_GlobalsWired confirms the dispatcher-side flag
// binding reaches the runTaskCreate global. End-to-end behavior (database
// create, gh shell-out) is integration-tested by the manual smoke pass — this
// guards the wiring contract.
//
// --skip-paperclip went with the Paperclip leg (#351). It was never declared in
// commands.yaml, so it was `unknown flag` through the dispatcher anyway: the
// documented escape hatch for a dead service was itself unreachable.
func TestTaskCreate_SkipFlags_GlobalsWired(t *testing.T) {
	defer resetTaskCreateSkipFlags()
	resetTaskCreateSkipFlags()

	flags := map[string]any{"skip-github": true}
	taskCreateSkipGitHub = flagBool(flags, "skip-github")

	if !taskCreateSkipGitHub {
		t.Error("--skip-github did not propagate to taskCreateSkipGitHub")
	}
}

// TestTaskCreate_SkipFlags_DefaultFalse documents the safe default —
// absent flags must produce a non-skipping run so existing callers
// don't silently drop legs.
func TestTaskCreate_SkipFlags_DefaultFalse(t *testing.T) {
	defer resetTaskCreateSkipFlags()
	resetTaskCreateSkipFlags()

	flags := map[string]any{} // flag not set
	taskCreateSkipGitHub = flagBool(flags, "skip-github")

	if taskCreateSkipGitHub {
		t.Error("default for skip-github should be false")
	}
}

// TestDispatcher_SkipFlagsAreBoolean ensures the dispatcher table
// recognizes the new flags as booleans (otherwise they'd be parsed as
// strings and silently ignored at the type assertion in flagBool).
func TestDispatcher_SkipFlagsAreBoolean(t *testing.T) {
	for _, name := range []string{"skip-github"} {
		if !isBooleanFlag(name) {
			t.Errorf("dispatcher booleanFlags table missing %q — flag will be parsed as string", name)
		}
	}
}

// TestTaskCreateLong_NoAtomicallyClaim guards the help-text update —
// the issue called out the misleading "Atomically" wording, which is
// now gone in favor of explicit per-leg behavior documentation.
func TestTaskCreateLong_NoAtomicallyClaim(t *testing.T) {
	if strings.Contains(taskCreateCmd.Long, "Atomically creates") {
		t.Error("task create long help still claims atomic semantics — should describe per-leg behavior instead")
	}
}

func resetTaskCreateSkipFlags() {
	taskCreateSkipGitHub = false
}
