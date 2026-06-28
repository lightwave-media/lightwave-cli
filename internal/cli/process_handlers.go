package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

func init() {
	RegisterHandler("process.list", processListHandler)
}

// processTopLimit caps the rows shown under --top (heaviest CPU consumers).
const processTopLimit = 20

// psMinFields is the column count of `ps -axo pid=,pcpu=,pmem=,comm=` output
// (pid, cpu, mem, and at least one comm token).
const psMinFields = 4

// procInfo is one row of the host process inventory. Field order is tuned for
// struct alignment (string first); JSON shape is fixed by the tags.
type procInfo struct {
	Name string  `json:"name"`
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
	PID  int     `json:"pid"`
}

// parsePsOutput parses headerless `ps -axo pid=,pcpu=,pmem=,comm=` output into
// process records. Malformed lines are skipped rather than failing the whole
// inventory; the command name (comm) may contain spaces, so the tail is
// rejoined. Kept pure (no I/O) so it is unit-testable from a fixture.
func parsePsOutput(raw string) []procInfo {
	lines := strings.Split(raw, "\n")
	procs := make([]procInfo, 0, len(lines))

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < psMinFields {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		cpu, _ := strconv.ParseFloat(fields[1], 64)
		mem, _ := strconv.ParseFloat(fields[2], 64)

		procs = append(procs, procInfo{
			PID:  pid,
			CPU:  cpu,
			Mem:  mem,
			Name: strings.Join(fields[3:], " "),
		})
	}

	return procs
}

// processListHandler implements `lw process list` — a read-only host process
// inventory (pid/cpu/mem/name) for v_core host-monitoring. --top sorts by CPU
// descending and caps the output; --json emits machine-readable output.
func processListHandler(ctx context.Context, _ []string, flags map[string]any) error {
	start := time.Now()

	raw, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,pcpu=,pmem=,comm=").Output()
	if err != nil {
		emitOperatorCLI("process.list", "fail", err.Error(), 1, start, nil)

		return fmt.Errorf("process list: ps failed: %w", err)
	}

	procs := parsePsOutput(string(raw))

	if flagBool(flags, "top") {
		sort.Slice(procs, func(i, j int) bool { return procs[i].CPU > procs[j].CPU })

		if len(procs) > processTopLimit {
			procs = procs[:processTopLimit]
		}
	}

	if flagBool(flags, "json") {
		enc, merr := json.MarshalIndent(procs, "", "  ")
		if merr != nil {
			return fmt.Errorf("process list: marshal json: %w", merr)
		}

		fmt.Println(string(enc))
		emitOperatorCLI("process.list", "pass", fmt.Sprintf("%d processes (json)", len(procs)), 0, start, nil)

		return nil
	}

	fmt.Printf("%-8s %6s %6s  %s\n", "PID", "CPU%", "MEM%", "NAME")

	for _, p := range procs {
		fmt.Printf("%-8d %6.1f %6.1f  %s\n", p.PID, p.CPU, p.Mem, p.Name)
	}

	emitOperatorCLI("process.list", "pass", fmt.Sprintf("%d processes", len(procs)), 0, start, nil)

	return nil
}
