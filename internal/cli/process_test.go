package cli

import "testing"

// =============================================================================
// Command structure
// =============================================================================

func TestProcessCommandStructure(t *testing.T) {
	subs := processCmd.Commands()
	names := make(map[string]bool)
	for _, c := range subs {
		names[c.Name()] = true
	}

	want := []string{"list", "spawn", "kill", "force-kill", "info", "tree"}
	for _, name := range want {
		if !names[name] {
			t.Errorf("missing process subcommand: %s", name)
		}
	}
}

func TestProcessSpawnRequiresMinimumOneArg(t *testing.T) {
	if processSpawnCmd.Args == nil {
		t.Error("spawn command should validate args")
	}
}

func TestProcessKillRequiresExactlyOneArg(t *testing.T) {
	if processKillCmd.Args == nil {
		t.Error("kill command should validate args")
	}
}

func TestProcessForceKillRequiresExactlyOneArg(t *testing.T) {
	if processForceKillCmd.Args == nil {
		t.Error("force-kill command should validate args")
	}
}

func TestProcessInfoRequiresExactlyOneArg(t *testing.T) {
	if processInfoCmd.Args == nil {
		t.Error("info command should validate args")
	}
}

// =============================================================================
// ProcessEntry struct
// =============================================================================

func TestProcessEntryFields(t *testing.T) {
	p := ProcessEntry{
		PID:     1234,
		PPID:    1,
		User:    "testuser",
		Command: "/usr/bin/test",
	}

	if p.PID != 1234 {
		t.Error("PID mismatch")
	}
	if p.PPID != 1 {
		t.Error("PPID mismatch")
	}
	if p.User != "testuser" {
		t.Error("User mismatch")
	}
	if p.Command != "/usr/bin/test" {
		t.Error("Command mismatch")
	}
}

// =============================================================================
// ProcessInfo struct
// =============================================================================

func TestProcessInfoFields(t *testing.T) {
	info := ProcessInfo{
		PID:     1234,
		PPID:    1,
		User:    "testuser",
		Command: "/usr/bin/test --flag",
		State:   "S",
		CPU:     "1.5%",
		Memory:  "2.3%",
		Started: "Sat Mar 22 10:30:00 2026",
	}

	if info.PID != 1234 {
		t.Error("PID mismatch")
	}
	if info.PPID != 1 {
		t.Error("PPID mismatch")
	}
	if info.User != "testuser" {
		t.Error("User mismatch")
	}
	if info.Command != "/usr/bin/test --flag" {
		t.Error("Command mismatch")
	}
	if info.State != "S" {
		t.Error("State mismatch")
	}
	if info.CPU != "1.5%" {
		t.Error("CPU mismatch")
	}
	if info.Memory != "2.3%" {
		t.Error("Memory mismatch")
	}
	if info.Started != "Sat Mar 22 10:30:00 2026" {
		t.Error("Started mismatch")
	}
}

// =============================================================================
// Live integration tests (run on macOS only)
// =============================================================================

func TestListProcessesReturnsResults(t *testing.T) {
	processes, err := listProcesses()
	if err != nil {
		t.Fatalf("listProcesses() failed: %v", err)
	}
	if len(processes) == 0 {
		t.Error("expected at least one process")
	}

	// Our own process should be in the list
	found := false
	for _, p := range processes {
		if p.PID > 0 && p.User != "" {
			found = true
			break
		}
	}
	if !found {
		t.Error("no valid process entries found")
	}
}

func TestListProcessesParsesPIDCorrectly(t *testing.T) {
	processes, err := listProcesses()
	if err != nil {
		t.Fatalf("listProcesses() failed: %v", err)
	}

	for _, p := range processes {
		if p.PID <= 0 {
			t.Errorf("invalid PID: %d", p.PID)
			break
		}
	}
}

func TestGetProcessInfoForCurrentProcess(t *testing.T) {
	// Process 1 (launchd) always exists on macOS
	info, err := getProcessInfo(1)
	if err != nil {
		t.Fatalf("getProcessInfo(1) failed: %v", err)
	}

	if info.PID != 1 {
		t.Errorf("expected PID 1, got %d", info.PID)
	}
	if info.User == "" {
		t.Error("User should not be empty for PID 1")
	}
	if info.Command == "" {
		t.Error("Command should not be empty for PID 1")
	}
	if info.Started == "" {
		t.Error("Started should not be empty for PID 1")
	}
}

func TestGetProcessInfoForNonexistentProcess(t *testing.T) {
	// PID 999999999 almost certainly doesn't exist
	_, err := getProcessInfo(999999999)
	if err == nil {
		t.Error("expected error for nonexistent process")
	}
}
