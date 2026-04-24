package cli

import "testing"

// =============================================================================
// escapeJSString
// =============================================================================

func TestEscapeJSStringPlain(t *testing.T) {
	got := escapeJSString("hello world")
	if got != "hello world" {
		t.Errorf("expected plain string unchanged, got %q", got)
	}
}

func TestEscapeJSStringQuotes(t *testing.T) {
	got := escapeJSString(`say "hello"`)
	want := `say \"hello\"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeJSStringBackslashes(t *testing.T) {
	got := escapeJSString(`path\to\file`)
	want := `path\\to\\file`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeJSStringNewlines(t *testing.T) {
	got := escapeJSString("line1\nline2\rline3")
	want := `line1\nline2\rline3`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeJSStringURLWithSpecialChars(t *testing.T) {
	got := escapeJSString(`https://example.com/path?q="test"&a=1`)
	want := `https://example.com/path?q=\"test\"&a=1`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// =============================================================================
// jxaStringLiteral
// =============================================================================

func TestJxaStringLiteralSimple(t *testing.T) {
	got := jxaStringLiteral("document.title")
	want := `"document.title"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestJxaStringLiteralWithQuotes(t *testing.T) {
	got := jxaStringLiteral(`document.querySelector("div")`)
	// JSON encoding escapes the inner quotes
	want := `"document.querySelector(\"div\")"`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestJxaStringLiteralWithNewlines(t *testing.T) {
	got := jxaStringLiteral("var x = 1;\nvar y = 2;")
	want := `"var x = 1;\nvar y = 2;"`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// =============================================================================
// Command structure
// =============================================================================

func TestBrowserCommandStructure(t *testing.T) {
	subs := browserCmd.Commands()
	names := make(map[string]bool)
	for _, c := range subs {
		names[c.Name()] = true
	}

	want := []string{"open", "tabs", "screenshot", "click", "type", "navigate", "execute", "start"}
	for _, name := range want {
		if !names[name] {
			t.Errorf("missing browser subcommand: %s", name)
		}
	}
}

func TestBrowserOpenRequiresOneArg(t *testing.T) {
	if browserOpenCmd.Args == nil {
		t.Error("open command should validate args")
	}
}

func TestBrowserClickRequiresTwoArgs(t *testing.T) {
	if browserClickCmd.Args == nil {
		t.Error("click command should validate args")
	}
}

func TestBrowserTypeRequiresOneArg(t *testing.T) {
	if browserTypeCmd.Args == nil {
		t.Error("type command should validate args")
	}
}

func TestBrowserNavigateRequiresOneArg(t *testing.T) {
	if browserNavigateCmd.Args == nil {
		t.Error("navigate command should validate args")
	}
}

func TestBrowserExecuteRequiresOneArg(t *testing.T) {
	if browserExecuteCmd.Args == nil {
		t.Error("execute command should validate args")
	}
}

func TestBrowserDefaultPort(t *testing.T) {
	// The default debug port should be 9222
	flag := browserCmd.PersistentFlags().Lookup("port")
	if flag == nil {
		t.Fatal("--port flag not found")
	}
	if flag.DefValue != "9222" {
		t.Errorf("expected default port 9222, got %s", flag.DefValue)
	}
}

func TestBrowserHeadlessFlag(t *testing.T) {
	flag := browserStartCmd.Flags().Lookup("headless")
	if flag == nil {
		t.Fatal("--headless flag not found")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default headless false, got %s", flag.DefValue)
	}
}

// =============================================================================
// BrowserTab struct
// =============================================================================

func TestBrowserTabFields(t *testing.T) {
	tab := BrowserTab{
		ID:    "abc12345-def6-7890",
		Title: "Test Page",
		URL:   "https://example.com",
		Type:  "page",
	}

	if tab.Type != "page" {
		t.Error("Type mismatch")
	}
	if tab.ID != "abc12345-def6-7890" {
		t.Error("ID mismatch")
	}
	if tab.Title != "Test Page" {
		t.Error("Title mismatch")
	}
	if tab.URL != "https://example.com" {
		t.Error("URL mismatch")
	}
}
