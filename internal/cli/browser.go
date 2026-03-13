package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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

		// Use AppleScript to open URL in Chrome
		script := fmt.Sprintf(`
			tell application "Google Chrome"
				activate
				open location "%s"
			end tell
		`, url)

		if _, err := exec.Command("osascript", "-e", script).Output(); err != nil {
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
			url := tab.URL
			if len(url) > 50 {
				url = url[:47] + "..."
			}

			table.Append([]string{
				tab.ID[:8],
				title,
				url,
			})
		}

		table.Render()
		return nil
	},
}

var browserScreenshotCmd = &cobra.Command{
	Use:   "screenshot [output-file]",
	Short: "Take a screenshot of the current page",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		outputFile := "screenshot.png"
		if len(args) > 0 {
			outputFile = args[0]
		}

		// Use screencapture focused on Chrome
		tmpFile := fmt.Sprintf("/tmp/lw_browser_screenshot_%d.png", os.Getpid())
		defer os.Remove(tmpFile)

		// Focus Chrome first
		focusScript := `
			tell application "Google Chrome"
				activate
			end tell
		`
		exec.Command("osascript", "-e", focusScript).Run()
		time.Sleep(200 * time.Millisecond)

		// Capture the Chrome window
		err := exec.Command("screencapture", "-x", "-o", tmpFile).Run()
		if err != nil {
			return fmt.Errorf("failed to capture screenshot: %w", err)
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

		// Use AppleScript to click
		script := fmt.Sprintf(`
			tell application "System Events"
				click at {%d, %d}
			end tell
		`, int(x), int(y))

		if _, err := exec.Command("osascript", "-e", script).Output(); err != nil {
			return fmt.Errorf("failed to click: %w", err)
		}

		fmt.Printf("%s Clicked at (%d, %d)\n", color.GreenString("✓"), int(x), int(y))
		return nil
	},
}

var browserTypeCmd = &cobra.Command{
	Use:   "type <text>",
	Short: "Type text in the browser",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text := args[0]

		script := fmt.Sprintf(`
			tell application "System Events"
				keystroke "%s"
			end tell
		`, text)

		if _, err := exec.Command("osascript", "-e", script).Output(); err != nil {
			return fmt.Errorf("failed to type: %w", err)
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
			tell application "Google Chrome"
				set URL of active tab of front window to "%s"
			end tell
		`, url)

		if _, err := exec.Command("osascript", "-e", script).Output(); err != nil {
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

		script := fmt.Sprintf(`
			tell application "Google Chrome"
				tell active tab of front window
					execute javascript "%s"
				end tell
			end tell
		`, js)

		out, err := exec.Command("osascript", "-e", script).Output()
		if err != nil {
			return fmt.Errorf("failed to execute JavaScript: %w", err)
		}

		if len(out) > 0 {
			fmt.Println(string(out))
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
		if err := chromeCmd.Start(); err != nil {
			return fmt.Errorf("failed to start Chrome: %w", err)
		}

		fmt.Printf("%s Chrome started with remote debugging on port %d\n",
			color.GreenString("✓"), browserDebugPort)
		fmt.Printf("   PID: %d\n", chromeCmd.Process.Pid)

		return nil
	},
}

func init() {
	// Browser flags
	browserCmd.PersistentFlags().IntVar(&browserDebugPort, "port", 9222, "Chrome debugging port")
	browserStartCmd.Flags().BoolVar(&browserHeadless, "headless", false, "Start in headless mode")

	// Add browser subcommands
	browserCmd.AddCommand(browserOpenCmd)
	browserCmd.AddCommand(browserTabsCmd)
	browserCmd.AddCommand(browserScreenshotCmd)
	browserCmd.AddCommand(browserClickCmd)
	browserCmd.AddCommand(browserTypeCmd)
	browserCmd.AddCommand(browserNavigateCmd)
	browserCmd.AddCommand(browserExecuteCmd)
	browserCmd.AddCommand(browserStartCmd)

	// Add browser to root
	rootCmd.AddCommand(browserCmd)
}

// =============================================================================
// CDP Helper Functions
// =============================================================================

// BrowserTab represents a browser tab
type BrowserTab struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Type  string `json:"type"`
}

// getBrowserTabs fetches tabs via CDP
func getBrowserTabs() ([]BrowserTab, error) {
	url := fmt.Sprintf("http://localhost:%d/json/list", browserDebugPort)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Chrome (is it running with --remote-debugging-port=%d?): %w",
			browserDebugPort, err)
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
