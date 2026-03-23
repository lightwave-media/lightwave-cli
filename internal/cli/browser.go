package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	browserDebugPort int
	browserHeadless  bool
)

// browserCmd is the parent command for browser automation
var browserCmd = &cobra.Command{
	Use:   "browser",
	Short: "Browser automation (Chrome DevTools Protocol)",
	Long:  `Control Chrome browser via CDP - navigate, screenshot, click, type.`,
}

var browserOpenCmd = &cobra.Command{
	Use:   "open <url>",
	Short: "Open a URL in the browser",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		url := args[0]

		// Use JXA — handles URLs with special chars safely
		script := fmt.Sprintf(`
			var chrome = Application("Google Chrome");
			chrome.activate();
			chrome.openLocation("%s");
		`, escapeJSString(url))

		if _, err := exec.Command("osascript", "-l", "JavaScript", "-e", script).Output(); err != nil {
			return fmt.Errorf("failed to open URL: %w", err)
		}

		fmt.Printf("%s Opened %s\n", color.GreenString("✓"), url)
		return nil
	},
}

var browserTabsCmd = &cobra.Command{
	Use:   "tabs",
	Short: "List open browser tabs",
	RunE: func(cmd *cobra.Command, args []string) error {
		tabs, err := getBrowserTabs()
		if err != nil {
			return err
		}

		if len(tabs) == 0 {
			fmt.Println(color.YellowString("No tabs found (is Chrome running with --remote-debugging-port?)"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Title", "URL"})
		table.SetBorder(false)
		table.SetColWidth(50)

		for _, tab := range tabs {
			title := tab.Title
			if len(title) > 40 {
				title = title[:37] + "..."
			}
			tabURL := tab.URL
			if len(tabURL) > 50 {
				tabURL = tabURL[:47] + "..."
			}

			id := tab.ID
			if len(id) > 8 {
				id = id[:8]
			}

			table.Append([]string{
				id,
				title,
				tabURL,
			})
		}

		table.Render()
		return nil
	},
}

var browserScreenshotCmd = &cobra.Command{
	Use:   "screenshot [output-file]",
	Short: "Take a screenshot of the Chrome window",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFile := "screenshot.png"
		if len(args) > 0 {
			outputFile = args[0]
		}

		// Get Chrome's CGWindowID via JXA and capture that specific window
		jxaScript := `
			ObjC.import('CoreGraphics');
			var windows = $.CGWindowListCopyWindowInfo($.kCGWindowListOptionOnScreenOnly, $.kCGNullWindowID);
			var count = ObjC.unwrap(windows).length;
			var found = "";
			for (var i = 0; i < count; i++) {
				var w = ObjC.unwrap(windows)[i];
				var name = ObjC.unwrap(w.kCGWindowOwnerName);
				var layer = ObjC.unwrap(w.kCGWindowLayer);
				if (name === "Google Chrome" && layer === 0) {
					found = ObjC.unwrap(w.kCGWindowNumber).toString();
					break;
				}
			}
			found;
		`

		cgIDOut, err := exec.Command("osascript", "-l", "JavaScript", "-e", jxaScript).Output()
		cgID := strings.TrimSpace(string(cgIDOut))

		tmpFile := fmt.Sprintf("/tmp/lw_browser_screenshot_%d.png", os.Getpid())
		defer os.Remove(tmpFile)

		if err != nil || cgID == "" {
			// Fallback: focus Chrome and capture frontmost window
			focusScript := `var chrome = Application("Google Chrome"); chrome.activate();`
			exec.Command("osascript", "-l", "JavaScript", "-e", focusScript).Run()
			time.Sleep(200 * time.Millisecond)

			if err := exec.Command("screencapture", "-x", "-o", "-w", tmpFile).Run(); err != nil {
				return fmt.Errorf("failed to capture screenshot: %w", err)
			}
		} else {
			if err := exec.Command("screencapture", "-x", "-o", "-l", cgID, tmpFile).Run(); err != nil {
				return fmt.Errorf("failed to capture Chrome window: %w", err)
			}
		}

		data, err := os.ReadFile(tmpFile)
		if err != nil {
			return fmt.Errorf("failed to read screenshot: %w", err)
		}

		if err := os.WriteFile(outputFile, data, 0644); err != nil {
			return fmt.Errorf("failed to write screenshot: %w", err)
		}

		fmt.Printf("%s Screenshot saved to %s\n", color.GreenString("✓"), outputFile)
		return nil
	},
}

var browserClickCmd = &cobra.Command{
	Use:   "click <x> <y>",
	Short: "Click at coordinates in the browser",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var x, y float64
		if _, err := fmt.Sscanf(args[0], "%f", &x); err != nil {
			return fmt.Errorf("invalid x coordinate: %s", args[0])
		}
		if _, err := fmt.Sscanf(args[1], "%f", &y); err != nil {
			return fmt.Errorf("invalid y coordinate: %s", args[1])
		}

		// Use cliclick (Homebrew) for reliable coordinate clicking
		// Falls back to AppleScript mouse event via JXA
		ix, iy := int(x), int(y)

		if _, err := exec.LookPath("cliclick"); err == nil {
			if err := exec.Command("cliclick", fmt.Sprintf("c:%d,%d", ix, iy)).Run(); err != nil {
				return fmt.Errorf("failed to click at (%d, %d): %w", ix, iy, err)
			}
		} else {
			// JXA + CoreGraphics mouse events
			script := fmt.Sprintf(`
				ObjC.import('CoreGraphics');
				var point = $.CGPointMake(%d, %d);
				var mouseDown = $.CGEventCreateMouseEvent(null, $.kCGEventLeftMouseDown, point, $.kCGMouseButtonLeft);
				var mouseUp = $.CGEventCreateMouseEvent(null, $.kCGEventLeftMouseUp, point, $.kCGMouseButtonLeft);
				$.CGEventPost($.kCGHIDEventTap, mouseDown);
				$.CGEventPost($.kCGHIDEventTap, mouseUp);
				"ok";
			`, ix, iy)

			if _, err := exec.Command("osascript", "-l", "JavaScript", "-e", script).Output(); err != nil {
				return fmt.Errorf("failed to click at (%d, %d): %w", ix, iy, err)
			}
		}

		fmt.Printf("%s Clicked at (%d, %d)\n", color.GreenString("✓"), ix, iy)
		return nil
	},
}

var browserTypeCmd = &cobra.Command{
	Use:   "type <text>",
	Short: "Type text in the browser",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text := args[0]

		// Use AppleScript keystroke which handles most printable chars.
		// Escape backslashes and quotes for the AppleScript string.
		script := fmt.Sprintf(`
			tell application "System Events"
				keystroke "%s"
			end tell
		`, escapeAppleScript(text))

		if _, err := exec.Command("osascript", "-e", script).Output(); err != nil {
			return fmt.Errorf("failed to type text: %w", err)
		}

		fmt.Printf("%s Typed text\n", color.GreenString("✓"))
		return nil
	},
}

var browserNavigateCmd = &cobra.Command{
	Use:   "navigate <url>",
	Short: "Navigate the current tab to a URL",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		url := args[0]

		script := fmt.Sprintf(`
			var chrome = Application("Google Chrome");
			chrome.windows[0].activeTab.url = "%s";
		`, escapeJSString(url))

		if _, err := exec.Command("osascript", "-l", "JavaScript", "-e", script).Output(); err != nil {
			return fmt.Errorf("failed to navigate: %w", err)
		}

		fmt.Printf("%s Navigated to %s\n", color.GreenString("✓"), url)
		return nil
	},
}

var browserExecuteCmd = &cobra.Command{
	Use:   "execute <javascript>",
	Short: "Execute JavaScript in the browser",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		js := args[0]

		// Use JXA to execute JS in Chrome — pass the JS code safely
		script := fmt.Sprintf(`
			var chrome = Application("Google Chrome");
			var tab = chrome.windows[0].activeTab;
			tab.execute({javascript: %s});
		`, jxaStringLiteral(js))

		out, err := exec.Command("osascript", "-l", "JavaScript", "-e", script).Output()
		if err != nil {
			return fmt.Errorf("failed to execute JavaScript: %w", err)
		}

		result := strings.TrimSpace(string(out))
		if result != "" {
			fmt.Println(result)
		}
		return nil
	},
}

var browserStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start Chrome with remote debugging enabled",
	Long: `Start Chrome with remote debugging enabled.

This allows CDP-based automation tools to connect.

Examples:
  lw browser start
  lw browser start --port 9223
  lw browser start --headless`,
	RunE: func(cmd *cobra.Command, args []string) error {
		chromePath := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

		chromeArgs := []string{
			fmt.Sprintf("--remote-debugging-port=%d", browserDebugPort),
			"--no-first-run",
			"--no-default-browser-check",
		}

		if browserHeadless {
			chromeArgs = append(chromeArgs, "--headless=new")
		}

		chromeCmd := exec.Command(chromePath, chromeArgs...)
		chromeCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		chromeCmd.Stdout = nil
		chromeCmd.Stderr = nil

		if err := chromeCmd.Start(); err != nil {
			return fmt.Errorf("failed to start Chrome: %w", err)
		}

		pid := chromeCmd.Process.Pid
		_ = chromeCmd.Process.Release()

		fmt.Printf("%s Chrome started with remote debugging on port %d\n",
			color.GreenString("✓"), browserDebugPort)
		fmt.Printf("   PID: %d\n", pid)

		return nil
	},
}

func init() {
	browserCmd.PersistentFlags().IntVar(&browserDebugPort, "port", 9222, "Chrome debugging port")
	browserStartCmd.Flags().BoolVar(&browserHeadless, "headless", false, "Start in headless mode")

	browserCmd.AddCommand(browserOpenCmd)
	browserCmd.AddCommand(browserTabsCmd)
	browserCmd.AddCommand(browserScreenshotCmd)
	browserCmd.AddCommand(browserClickCmd)
	browserCmd.AddCommand(browserTypeCmd)
	browserCmd.AddCommand(browserNavigateCmd)
	browserCmd.AddCommand(browserExecuteCmd)
	browserCmd.AddCommand(browserStartCmd)

	rootCmd.AddCommand(browserCmd)
}

// =============================================================================
// Helper Functions
// =============================================================================

// BrowserTab represents a browser tab
type BrowserTab struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Type  string `json:"type"`
}

// escapeJSString escapes a string for safe embedding in a JavaScript double-quoted string.
func escapeJSString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}

// jxaStringLiteral returns a properly quoted and escaped JXA string literal
// for embedding arbitrary JavaScript code inside a JXA script.
func jxaStringLiteral(s string) string {
	// Use JSON encoding which handles all escaping correctly
	b, _ := json.Marshal(s)
	return string(b)
}

// getBrowserTabs fetches tabs via CDP, falling back to JXA if CDP is unavailable.
func getBrowserTabs() ([]BrowserTab, error) {
	// Try CDP first
	tabs, err := getBrowserTabsCDP()
	if err == nil {
		return tabs, nil
	}

	// Fallback: JXA — Chrome's AppleScript/JXA API exposes windows[].tabs[]
	return getBrowserTabsJXA()
}

// getBrowserTabsCDP fetches tabs via Chrome DevTools Protocol.
func getBrowserTabsCDP() ([]BrowserTab, error) {
	tabURL := fmt.Sprintf("http://localhost:%d/json/list", browserDebugPort)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(tabURL)
	if err != nil {
		return nil, fmt.Errorf("CDP unavailable on port %d: %w", browserDebugPort, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var tabs []BrowserTab
	if err := json.Unmarshal(body, &tabs); err != nil {
		return nil, fmt.Errorf("failed to parse tabs: %w", err)
	}

	// Filter to only page types
	var pages []BrowserTab
	for _, tab := range tabs {
		if tab.Type == "page" {
			pages = append(pages, tab)
		}
	}

	return pages, nil
}

// getBrowserTabsJXA fetches tabs using Chrome's JXA (JavaScript for Automation) API.
func getBrowserTabsJXA() ([]BrowserTab, error) {
	script := `
		var chrome = Application("Google Chrome");
		var result = [];
		var windows = chrome.windows();
		for (var w = 0; w < windows.length; w++) {
			var tabs = windows[w].tabs();
			for (var t = 0; t < tabs.length; t++) {
				result.push({
					id: w + "-" + t,
					title: tabs[t].title(),
					url: tabs[t].url(),
					type: "page"
				});
			}
		}
		JSON.stringify(result);
	`

	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", script).Output()
	if err != nil {
		return nil, fmt.Errorf("JXA tab listing failed (is Chrome running?): %w", err)
	}

	var tabs []BrowserTab
	if err := json.Unmarshal(out, &tabs); err != nil {
		return nil, fmt.Errorf("failed to parse JXA tab output: %w", err)
	}

	return tabs, nil
}
