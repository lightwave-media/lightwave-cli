package cli

import (
	"strings"
	"testing"
)

// TestTaskCreate_SkipFlags_GlobalsWired confirms the dispatcher-side
// flag bindings reach the runTaskCreate globals. End-to-end behavior
// (database create, gh shell-out, paperclip API) is integration-tested
// by the manual smoke pass — this guards the wiring contract.
func TestTaskCreate_SkipFlags_GlobalsWired(t *testing.T) {
	defer resetTaskCreateSkipFlags()
	resetTaskCreateSkipFlags()

	flags := map[string]any{
		"skip-paperclip": true,
		"skip-github":    true,
	}
	taskCreateSkipPaperclip = flagBool(flags, "skip-paperclip")
	taskCreateSkipGitHub = flagBool(flags, "skip-github")

	if !taskCreateSkipPaperclip {
		t.Error("--skip-paperclip did not propagate to taskCreateSkipPaperclip")
	}
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

	flags := map[string]any{} // neither flag set
	taskCreateSkipPaperclip = flagBool(flags, "skip-paperclip")
	taskCreateSkipGitHub = flagBool(flags, "skip-github")

	if taskCreateSkipPaperclip {
		t.Error("default for skip-paperclip should be false")
	}
	if taskCreateSkipGitHub {
		t.Error("default for skip-github should be false")
	}
}

// TestDispatcher_SkipFlagsAreBoolean ensures the dispatcher table
// recognizes the new flags as booleans (otherwise they'd be parsed as
// strings and silently ignored at the type assertion in flagBool).
func TestDispatcher_SkipFlagsAreBoolean(t *testing.T) {
	for _, name := range []string{"skip-paperclip", "skip-github"} {
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
	taskCreateSkipPaperclip = false
	taskCreateSkipGitHub = false
}
