package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/vcore"
	"github.com/spf13/cobra"
)

// `lw v_core` — Phase 3 / EB-001 plan §3 daemon lifecycle wrapper.
//
// Supervises the vcore binary (which lives in lightwave-sys). Singleton
// — one daemon at a time. State at ~/.lightwave/v_core/state.json so
// `lw v_core status` works across CLI invocations.

var vcoreCmd = &cobra.Command{
	Use:   "v_core",
	Short: "Supervise the v_core orchestrator daemon (start / stop / status / logs)",
	Long: `Lifecycle wrapper around the vcore binary in lightwave-sys.

Binary resolution: $LW_VCORE_BINARY, then 'vcore' on PATH, then
~/dev/lightwave-sys/target/release/vcore.

State and logs live under ~/.lightwave/v_core/.`,
}

var (
	vcoreStartArgs []string
	vcoreStartJSON bool

	vcoreStopForce bool

	vcoreLogsTail   int
	vcoreLogsFollow bool

	vcoreStatusJSON bool
)

var vcoreStartCmd = &cobra.Command{
	Use:          "start [-- <vcore-args>...]",
	Short:        "Launch the v_core daemon detached",
	SilenceUsage: true,
	RunE:         runVcoreStart,
}

var vcoreStopCmd = &cobra.Command{
	Use:          "stop",
	Short:        "Send SIGTERM (or SIGKILL with --force) to the daemon",
	SilenceUsage: true,
	RunE:         runVcoreStop,
}

var vcoreStatusCmd = &cobra.Command{
	Use:          "status",
	Short:        "Show whether the daemon is running, PID, uptime",
	SilenceUsage: true,
	RunE:         runVcoreStatus,
}

var vcoreLogsCmd = &cobra.Command{
	Use:          "logs",
	Short:        "Print the daemon's log file (last N lines or full)",
	SilenceUsage: true,
	RunE:         runVcoreLogs,
}

func init() {
	vcoreStartCmd.Flags().StringSliceVar(&vcoreStartArgs, "arg", nil, "Additional argv to pass to the vcore binary (repeatable)")
	vcoreStartCmd.Flags().BoolVar(&vcoreStartJSON, "json", false, "Emit JSON state envelope")

	vcoreStopCmd.Flags().BoolVar(&vcoreStopForce, "force", false, "Send SIGKILL instead of SIGTERM")

	vcoreStatusCmd.Flags().BoolVar(&vcoreStatusJSON, "json", false, "Emit JSON state envelope")

	vcoreLogsCmd.Flags().IntVar(&vcoreLogsTail, "tail", 50, "Print last N lines (0 for full file)")
	vcoreLogsCmd.Flags().BoolVar(&vcoreLogsFollow, "follow", false, "Follow log output (-f); blocks until Ctrl-C")

	vcoreCmd.AddCommand(vcoreStartCmd)
	vcoreCmd.AddCommand(vcoreStopCmd)
	vcoreCmd.AddCommand(vcoreStatusCmd)
	vcoreCmd.AddCommand(vcoreLogsCmd)
}

func runVcoreStart(_ *cobra.Command, _ []string) error {
	s, err := vcore.Start(vcoreStartArgs)
	if err != nil {
		if errors.Is(err, vcore.ErrAlreadyRunning) {
			fmt.Fprintf(os.Stderr, "v_core is already running (pid %d, started %s)\n",
				s.PID, s.StartedAt.Local().Format(time.RFC3339))
			os.Exit(1)
		}
		return err
	}
	if vcoreStartJSON {
		return emitJSON(s)
	}
	fmt.Printf("started v_core (pid %s) — binary %s\n",
		color.CyanString("%d", s.PID), s.Binary)
	fmt.Printf("  log: %s\n", s.LogPath)
	return nil
}

func runVcoreStop(_ *cobra.Command, _ []string) error {
	s, err := vcore.Status()
	if err != nil {
		return err
	}
	if s == nil {
		fmt.Println(color.YellowString("v_core is not running"))
		return nil
	}
	if err := vcore.Stop(vcoreStopForce); err != nil {
		return err
	}
	fmt.Printf("stopped v_core (was pid %d)\n", s.PID)
	return nil
}

func runVcoreStatus(_ *cobra.Command, _ []string) error {
	s, err := vcore.Status()
	if err != nil {
		return err
	}
	if vcoreStatusJSON {
		payload := map[string]any{"running": s != nil}
		if s != nil {
			payload["pid"] = s.PID
			payload["binary"] = s.Binary
			payload["started_at"] = s.StartedAt
			payload["log_path"] = s.LogPath
			payload["uptime_seconds"] = int(time.Since(s.StartedAt).Seconds())
		}
		return emitJSON(payload)
	}
	if s == nil {
		fmt.Println(color.YellowString("v_core is not running"))
		return nil
	}
	fmt.Printf("%s %s\n", color.CyanString("PID:"), color.GreenString("%d", s.PID))
	fmt.Printf("%s %s\n", color.CyanString("Binary:"), s.Binary)
	fmt.Printf("%s %s\n", color.CyanString("Started:"), s.StartedAt.Local().Format("2006-01-02 15:04:05"))
	fmt.Printf("%s %s\n", color.CyanString("Uptime:"), time.Since(s.StartedAt).Truncate(time.Second))
	fmt.Printf("%s %s\n", color.CyanString("Log:"), s.LogPath)
	return nil
}

func runVcoreLogs(_ *cobra.Command, _ []string) error {
	path, err := vcore.LogPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		fmt.Println(color.YellowString("no log yet — v_core has never run"))
		return nil
	}

	if vcoreLogsFollow {
		return tailFollow(path)
	}
	return tailN(path, vcoreLogsTail)
}

// tailN prints the last n lines (or whole file if n <= 0). Implementation
// reads the whole file — log files for an orchestrator daemon are small
// in MVP. Optimised tail can come later when this grows.
func tailN(path string, n int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if n <= 0 {
		_, err := os.Stdout.Write(data)
		return err
	}
	// Walk backwards counting newlines.
	cut := len(data)
	lines := 0
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == '\n' {
			lines++
			if lines > n {
				cut = i + 1
				break
			}
		}
	}
	if lines <= n {
		cut = 0
	}
	_, err = os.Stdout.Write(data[cut:])
	return err
}

// tailFollow mimics `tail -f` — blocks polling for new content every
// 200ms. Exits on Ctrl-C (handled by cobra's signal plumbing).
func tailFollow(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Seek to end so we only print new content.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if _, werr := os.Stdout.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if n == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}
}
