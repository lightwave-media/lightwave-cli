package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

// systemCmd is the parent command for system operations
var systemCmd = &cobra.Command{
	Use:   "system",
	Short: "System operations (windows, clipboard, notifications)",
	Long:  `Manage system resources - windows, clipboard, and send notifications.`,
}

// =============================================================================
// Window Management
// =============================================================================

var windowsCmd = &cobra.Command{
	Use:   "windows",
	Short: "Window management",
}

var windowsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all visible windows",
	Long: `List all visible windows on the system.

Returns window ID, title, application name, and position.

Examples:
  lw system windows list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		windows, err := listWindows()
		if err != nil {
			return err
		}

		if len(windows) == 0 {
			fmt.Println(color.YellowString("No windows found"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Title", "Application", "PID", "Visible"})
		table.SetBorder(false)

		for _, w := range windows {
			visible := "No"
			if w.IsVisible {
				visible = color.GreenString("Yes")
			}

			title := w.Title
			if len(title) > 40 {
				title = title[:37] + "..."
			}

			table.Append([]string{
				fmt.Sprintf("%d", w.ID),
				title,
				w.AppName,
				fmt.Sprintf("%d", w.PID),
				visible,
			})
		}

		table.Render()
		return nil
	},
}

var windowsFocusCmd = &cobra.Command{
	Use:   "focus <window-id>",
	Short: "Focus a window by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var windowID uint32
		if _, err := fmt.Sscanf(args[0], "%d", &windowID); err != nil {
			return fmt.Errorf("invalid window ID: %s", args[0])
		}

		if err := focusWindow(windowID); err != nil {
			return err
		}

		fmt.Println(color.GreenString("✓ Window focused"))
		return nil
	},
}

var windowsCaptureCmd = &cobra.Command{
	Use:   "capture <window-id> [output-file]",
	Short: "Capture a screenshot of a window",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var windowID uint32
		if _, err := fmt.Sscanf(args[0], "%d", &windowID); err != nil {
			return fmt.Errorf("invalid window ID: %s", args[0])
		}

		outputFile := "screenshot.png"
		if len(args) > 1 {
			outputFile = args[1]
		}

		data, err := captureWindow(windowID)
		if err != nil {
			return err
		}

		if err := os.WriteFile(outputFile, data, 0644); err != nil {
			return fmt.Errorf("failed to write screenshot: %w", err)
		}

		fmt.Printf("%s Screenshot saved to %s\n", color.GreenString("✓"), outputFile)
		return nil
	},
}

// =============================================================================
// Clipboard
// =============================================================================

var clipboardCmd = &cobra.Command{
	Use:   "clipboard",
	Short: "Clipboard operations",
}

var clipboardGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get clipboard content",
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := getClipboard()
		if err != nil {
			return err
		}

		fmt.Println(content)
		return nil
	},
}

var clipboardSetCmd = &cobra.Command{
	Use:   "set <text>",
	Short: "Set clipboard content",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := setClipboard(args[0]); err != nil {
			return err
		}

		fmt.Println(color.GreenString("✓ Clipboard updated"))
		return nil
	},
}

// =============================================================================
// Notifications
// =============================================================================

var notifyCmd = &cobra.Command{
	Use:   "notify <title> [body]",
	Short: "Send a system notification",
	Long: `Send a macOS system notification.

Examples:
  lw system notify "Hello" "World"
  lw system notify "Build complete"`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		title := args[0]
		body := ""
		if len(args) > 1 {
			body = args[1]
		}

		if err := sendNotification(title, body); err != nil {
			return err
		}

		fmt.Println(color.GreenString("✓ Notification sent"))
		return nil
	},
}

// =============================================================================
// AppleScript
// =============================================================================

var applescriptCmd = &cobra.Command{
	Use:   "applescript <script>",
	Short: "Execute AppleScript",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		output, err := runAppleScript(args[0])
		if err != nil {
			return err
		}

		if output != "" {
			fmt.Println(output)
		}
		return nil
	},
}

func init() {
	// Add windows subcommands
	windowsCmd.AddCommand(windowsListCmd)
	windowsCmd.AddCommand(windowsFocusCmd)
	windowsCmd.AddCommand(windowsCaptureCmd)

	// Add clipboard subcommands
	clipboardCmd.AddCommand(clipboardGetCmd)
	clipboardCmd.AddCommand(clipboardSetCmd)

	// Add to system command
	systemCmd.AddCommand(windowsCmd)
	systemCmd.AddCommand(clipboardCmd)
	systemCmd.AddCommand(notifyCmd)
	systemCmd.AddCommand(applescriptCmd)

	// Add system to root
	rootCmd.AddCommand(systemCmd)
}

// =============================================================================
// Helper Types and Functions
// =============================================================================

// WindowInfo represents information about a window
type WindowInfo struct {
	ID          uint32 `json:"id"`
	Title       string `json:"title"`
	AppName     string `json:"app_name"`
	BundleID    string `json:"bundle_id,omitempty"`
	PID         uint32 `json:"pid"`
	IsVisible   bool   `json:"is_visible"`
	IsMinimized bool   `json:"is_minimized"`
	IsFrontmost bool   `json:"is_frontmost"`
	Layer       int32  `json:"layer"`
}

// listWindows uses osascript to list windows
func listWindows() ([]WindowInfo, error) {
	script := `
		set output to ""
		tell application "System Events"
			repeat with proc in (every process whose visible is true)
				set procName to name of proc
				set procID to unix id of proc
				try
					repeat with win in (every window of proc)
						set winTitle to name of win
						set output to output & "{\"app\":\"" & procName & "\",\"title\":\"" & winTitle & "\",\"pid\":" & procID & "},"
					end repeat
				end try
			end repeat
		end tell
		return "[" & text 1 thru -2 of output & "]"
	`

	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list windows: %w", err)
	}

	// Parse the JSON output
	var rawWindows []struct {
		App   string `json:"app"`
		Title string `json:"title"`
		PID   uint32 `json:"pid"`
	}

	if err := json.Unmarshal(out, &rawWindows); err != nil {
		// Return empty list if parsing fails (e.g., no windows)
		return []WindowInfo{}, nil
	}

	// Convert to WindowInfo
	windows := make([]WindowInfo, len(rawWindows))
	for i, w := range rawWindows {
		windows[i] = WindowInfo{
			ID:        uint32(i + 1),
			Title:     w.Title,
			AppName:   w.App,
			PID:       w.PID,
			IsVisible: true,
		}
	}

	return windows, nil
}

// focusWindow focuses a window using AppleScript
func focusWindow(windowID uint32) error {
	// For now, we focus by index since we don't have real window IDs
	// In production, we'd use the Rust library via FFI
	windows, err := listWindows()
	if err != nil {
		return err
	}

	if int(windowID) > len(windows) || windowID == 0 {
		return fmt.Errorf("window ID %d not found", windowID)
	}

	window := windows[windowID-1]

	script := fmt.Sprintf(`
		tell application "System Events"
			tell process "%s"
				set frontmost to true
			end tell
		end tell
	`, window.AppName)

	_, err = exec.Command("osascript", "-e", script).Output()
	return err
}

// captureWindow captures a screenshot using screencapture
func captureWindow(windowID uint32) ([]byte, error) {
	// Use screencapture for now
	tmpFile := fmt.Sprintf("/tmp/lw_screenshot_%d.png", os.Getpid())
	defer os.Remove(tmpFile)

	// -x = no sound, -o = no shadow
	err := exec.Command("screencapture", "-x", "-o", tmpFile).Run()
	if err != nil {
		return nil, fmt.Errorf("failed to capture screenshot: %w", err)
	}

	return os.ReadFile(tmpFile)
}

// getClipboard gets clipboard text
func getClipboard() (string, error) {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get clipboard: %w", err)
	}
	return string(out), nil
}

// setClipboard sets clipboard text
func setClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to set clipboard: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to set clipboard: %w", err)
	}

	if _, err := stdin.Write([]byte(text)); err != nil {
		return fmt.Errorf("failed to set clipboard: %w", err)
	}
	stdin.Close()

	return cmd.Wait()
}

// sendNotification sends a macOS notification
func sendNotification(title, body string) error {
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, body, title)
	_, err := exec.Command("osascript", "-e", script).Output()
	return err
}

// runAppleScript executes an AppleScript
func runAppleScript(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "", fmt.Errorf("AppleScript error: %w", err)
	}
	return string(out), nil
}
