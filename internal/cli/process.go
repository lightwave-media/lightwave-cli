package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

// processCmd is the parent command for process management
var processCmd = &cobra.Command{
	Use:   "process",
	Short: "Process management",
	Long:  `List, spawn, and manage system processes.`,
}

var processListCmd = &cobra.Command{
	Use:   "list [filter]",
	Short: "List running processes",
	Long: `List running processes.

With a filter argument, only shows processes matching the filter.

Examples:
  lw process list
  lw process list chrome
  lw process list node`,
	RunE: func(cmd *cobra.Command, args []string) error {
		filter := ""
		if len(args) > 0 {
			filter = strings.ToLower(args[0])
		}

		processes, err := listProcesses()
		if err != nil {
			return err
		}

		// Filter processes
		var filtered []ProcessEntry
		for _, p := range processes {
			if filter == "" || strings.Contains(strings.ToLower(p.Command), filter) {
				filtered = append(filtered, p)
			}
		}

		if len(filtered) == 0 {
			fmt.Println(color.YellowString("No processes found"))
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"PID", "PPID", "User", "Command"})
		table.SetBorder(false)
		table.SetColWidth(60)

		for _, p := range filtered {
			command := p.Command
			if len(command) > 60 {
				command = command[:57] + "..."
			}

			table.Append([]string{
				fmt.Sprintf("%d", p.PID),
				fmt.Sprintf("%d", p.PPID),
				p.User,
				command,
			})
		}

		table.Render()
		fmt.Printf("\nTotal: %d processes\n", len(filtered))
		return nil
	},
}

var processSpawnCmd = &cobra.Command{
	Use:   "spawn <command> [args...]",
	Short: "Spawn a new process",
	Long: `Spawn a new process and return its PID.

The process runs in the background.

Examples:
  lw process spawn sleep 60
  lw process spawn python3 -m http.server 8000`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		command := args[0]
		commandArgs := args[1:]

		proc := exec.Command(command, commandArgs...)
		proc.Stdout = os.Stdout
		proc.Stderr = os.Stderr

		if err := proc.Start(); err != nil {
			return fmt.Errorf("failed to spawn process: %w", err)
		}

		fmt.Printf("%s Process spawned\n", color.GreenString("✓"))
		fmt.Printf("   PID: %d\n", proc.Process.Pid)
		fmt.Printf("   Command: %s %s\n", command, strings.Join(commandArgs, " "))

		return nil
	},
}

var processKillCmd = &cobra.Command{
	Use:   "kill <pid>",
	Short: "Kill a process",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid PID: %s", args[0])
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("process not found: %w", err)
		}

		if err := process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}

		fmt.Printf("%s Process %d killed\n", color.GreenString("✓"), pid)
		return nil
	},
}

var processForceKillCmd = &cobra.Command{
	Use:   "force-kill <pid>",
	Short: "Force kill a process (SIGKILL)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid PID: %s", args[0])
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("process not found: %w", err)
		}

		if err := process.Signal(syscall.SIGKILL); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}

		fmt.Printf("%s Process %d force killed\n", color.GreenString("✓"), pid)
		return nil
	},
}

var processInfoCmd = &cobra.Command{
	Use:   "info <pid>",
	Short: "Get information about a process",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid PID: %s", args[0])
		}

		info, err := getProcessInfo(pid)
		if err != nil {
			return err
		}

		fmt.Println()
		fmt.Printf("%s %d\n", color.CyanString("PID:"), info.PID)
		fmt.Printf("%s %d\n", color.CyanString("PPID:"), info.PPID)
		fmt.Printf("%s %s\n", color.CyanString("User:"), info.User)
		fmt.Printf("%s %s\n", color.CyanString("Command:"), info.Command)
		fmt.Printf("%s %s\n", color.CyanString("State:"), info.State)
		fmt.Printf("%s %s\n", color.CyanString("CPU:"), info.CPU)
		fmt.Printf("%s %s\n", color.CyanString("Memory:"), info.Memory)
		fmt.Printf("%s %s\n", color.CyanString("Started:"), info.Started)
		fmt.Println()

		return nil
	},
}

var processTreeCmd = &cobra.Command{
	Use:   "tree [pid]",
	Short: "Show process tree",
	Long: `Show process tree.

Without arguments, shows the full process tree.
With a PID, shows the tree for that process.

Examples:
  lw process tree
  lw process tree 1234`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Use pstree if available, otherwise fall back to ps
		pstreeArgs := []string{"-g", "2"}
		if len(args) > 0 {
			pstreeArgs = append(pstreeArgs, "-p", args[0])
		}

		out, err := exec.Command("pstree", pstreeArgs...).Output()
		if err != nil {
			// Fall back to ps forest
			psArgs := []string{"axjf"}
			out, err = exec.Command("ps", psArgs...).Output()
			if err != nil {
				return fmt.Errorf("failed to get process tree: %w", err)
			}
		}

		fmt.Print(string(out))
		return nil
	},
}

func init() {
	// Add process subcommands
	processCmd.AddCommand(processListCmd)
	processCmd.AddCommand(processSpawnCmd)
	processCmd.AddCommand(processKillCmd)
	processCmd.AddCommand(processForceKillCmd)
	processCmd.AddCommand(processInfoCmd)
	processCmd.AddCommand(processTreeCmd)

	// Add process to root
	rootCmd.AddCommand(processCmd)
}

// =============================================================================
// Helper Types and Functions
// =============================================================================

// ProcessEntry represents a process
type ProcessEntry struct {
	PID     int
	PPID    int
	User    string
	Command string
}

// ProcessInfo represents detailed process information
type ProcessInfo struct {
	PID     int
	PPID    int
	User    string
	Command string
	State   string
	CPU     string
	Memory  string
	Started string
}

// listProcesses lists all running processes
func listProcesses() ([]ProcessEntry, error) {
	out, err := exec.Command("ps", "-ax", "-o", "pid,ppid,user,command").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list processes: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	var processes []ProcessEntry

	for i, line := range lines {
		// Skip header
		if i == 0 {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		pid, _ := strconv.Atoi(fields[0])
		ppid, _ := strconv.Atoi(fields[1])
		user := fields[2]
		command := strings.Join(fields[3:], " ")

		processes = append(processes, ProcessEntry{
			PID:     pid,
			PPID:    ppid,
			User:    user,
			Command: command,
		})
	}

	return processes, nil
}

// getProcessInfo gets detailed information about a process
func getProcessInfo(pid int) (*ProcessInfo, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "pid,ppid,user,stat,%cpu,%mem,lstart,command").Output()
	if err != nil {
		return nil, fmt.Errorf("process %d not found: %w", pid, err)
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("process %d not found", pid)
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 8 {
		return nil, fmt.Errorf("failed to parse process info")
	}

	pidVal, _ := strconv.Atoi(fields[0])
	ppidVal, _ := strconv.Atoi(fields[1])

	// lstart takes 5 fields (e.g., "Thu Jan 2 15:04:05 2025")
	started := strings.Join(fields[6:11], " ")
	command := strings.Join(fields[11:], " ")

	return &ProcessInfo{
		PID:     pidVal,
		PPID:    ppidVal,
		User:    fields[2],
		State:   fields[3],
		CPU:     fields[4] + "%",
		Memory:  fields[5] + "%",
		Started: started,
		Command: command,
	}, nil
}
