package cli

import "testing"

// =============================================================================
// escapeAppleScript
// =============================================================================

func TestEscapeAppleScriptPlainString(t *testing.T) {
	got := escapeAppleScript("hello world")
	if got != "hello world" {
		t.Errorf("expected plain string unchanged, got %q", got)
	}
}

func TestEscapeAppleScriptQuotes(t *testing.T) {
	got := escapeAppleScript(`say "hello"`)
	want := `say \"hello\"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeAppleScriptBackslashes(t *testing.T) {
	got := escapeAppleScript(`path\to\file`)
	want := `path\\to\\file`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeAppleScriptMixed(t *testing.T) {
	got := escapeAppleScript(`he said "it's a \"test\""`)
	want := `he said \"it's a \\\"test\\\"\"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// =============================================================================
// Command structure
// =============================================================================

func TestSystemCommandStructure(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
	}{
		{"system exists", systemCmd.Use},
		{"windows exists", windowsCmd.Use},
		{"clipboard exists", clipboardCmd.Use},
		{"notify exists", notifyCmd.Use},
		{"applescript exists", applescriptCmd.Use},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cmd == "" {
				t.Error("command Use field is empty")
			}
		})
	}
}

func TestWindowsSubcommands(t *testing.T) {
	subs := windowsCmd.Commands()
	names := make(map[string]bool)
	for _, c := range subs {
		names[c.Name()] = true
	}

	for _, want := range []string{"list", "focus", "capture"} {
		if !names[want] {
			t.Errorf("missing windows subcommand: %s", want)
		}
	}
}

func TestClipboardSubcommands(t *testing.T) {
	subs := clipboardCmd.Commands()
	names := make(map[string]bool)
	for _, c := range subs {
		names[c.Name()] = true
	}

	for _, want := range []string{"get", "set"} {
		if !names[want] {
			t.Errorf("missing clipboard subcommand: %s", want)
		}
	}
}

func TestWindowsFocusRequiresOneArg(t *testing.T) {
	if windowsFocusCmd.Args == nil {
		t.Error("focus command should validate args")
	}
}

func TestWindowsCaptureRequiresArgs(t *testing.T) {
	if windowsCaptureCmd.Args == nil {
		t.Error("capture command should validate args")
	}
}

func TestNotifyAcceptsOneOrTwoArgs(t *testing.T) {
	if notifyCmd.Args == nil {
		t.Error("notify command should validate args")
	}
}

func TestApplescriptRequiresOneArg(t *testing.T) {
	if applescriptCmd.Args == nil {
		t.Error("applescript command should validate args")
	}
}

// =============================================================================
// WindowInfo struct
// =============================================================================

func TestWindowInfoFields(t *testing.T) {
	w := WindowInfo{
		ID:        1,
		Title:     "Test Window",
		AppName:   "TestApp",
		PID:       1234,
		IsVisible: true,
	}

	if w.ID != 1 {
		t.Error("ID mismatch")
	}
	if w.Title != "Test Window" {
		t.Error("Title mismatch")
	}
	if w.AppName != "TestApp" {
		t.Error("AppName mismatch")
	}
	if !w.IsVisible {
		t.Error("IsVisible should be true")
	}
}
