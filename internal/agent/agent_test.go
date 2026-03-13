package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestGenerateName(t *testing.T) {
	// Pattern: <role>-<word>-<4hex>
	re := regexp.MustCompile(`^(backend|frontend|infra|vcore)-[a-z]+-[0-9a-f]{4}$`)

	for _, role := range []Role{RoleBackend, RoleFrontend, RoleInfra, RoleVCore} {
		name, err := GenerateName(role)
		if err != nil {
			t.Fatalf("GenerateName(%s): %v", role, err)
		}
		if !re.MatchString(name) {
			t.Errorf("GenerateName(%s) = %q, does not match pattern", role, name)
		}
	}
}

func TestGenerateNameUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		name, err := GenerateName(RoleBackend)
		if err != nil {
			t.Fatalf("GenerateName: %v", err)
		}
		if seen[name] {
			t.Errorf("duplicate name after %d iterations: %s", i, name)
		}
		seen[name] = true
	}
}

func TestGenerateNameUnknownRole(t *testing.T) {
	_, err := GenerateName("unknown")
	if err == nil {
		t.Fatal("expected error for unknown role")
	}
}

func TestTmuxSessionName(t *testing.T) {
	got := TmuxSessionName("backend-forge-a1b2")
	want := "lw-backend-forge-a1b2"
	if got != want {
		t.Errorf("TmuxSessionName = %q, want %q", got, want)
	}
}

func TestBranchName(t *testing.T) {
	got := BranchName("infra-terra-c3d4")
	want := "lw/infra-terra-c3d4"
	if got != want {
		t.Errorf("BranchName = %q, want %q", got, want)
	}
}

func TestSaveAndLoad(t *testing.T) {
	// Override BaseDir to use temp directory.
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	a := &Agent{
		Name:   "backend-forge-abcd",
		Role:   RoleBackend,
		State:  StateWorking,
		Repo:   "git@github.com:example/repo.git",
		TaskID: "TASK-42",
	}

	if err := a.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify state file exists.
	stateFile := filepath.Join(tmpDir, ".lw", "agents", "backend-forge-abcd", "state.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state file missing: %v", err)
	}

	// Load and verify roundtrip.
	loaded, err := Load("backend-forge-abcd")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Name != a.Name {
		t.Errorf("Name = %q, want %q", loaded.Name, a.Name)
	}
	if loaded.Role != a.Role {
		t.Errorf("Role = %q, want %q", loaded.Role, a.Role)
	}
	if loaded.State != a.State {
		t.Errorf("State = %q, want %q", loaded.State, a.State)
	}
	if loaded.TaskID != a.TaskID {
		t.Errorf("TaskID = %q, want %q", loaded.TaskID, a.TaskID)
	}
}

func TestSetState(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	a := &Agent{
		Name:  "frontend-wave-1234",
		Role:  RoleFrontend,
		State: StateWorking,
	}
	if err := a.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := a.SetState(StateStuck); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	loaded, err := Load("frontend-wave-1234")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.State != StateStuck {
		t.Errorf("State = %q, want %q", loaded.State, StateStuck)
	}
}

func TestListAllEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create the agents directory but leave it empty.
	if err := os.MkdirAll(filepath.Join(tmpDir, ".lw", "agents"), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	agents, err := ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("ListAll = %d agents, want 0", len(agents))
	}
}

func TestListAllNonExistentDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Don't create the directory at all.
	agents, err := ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if agents != nil {
		t.Errorf("ListAll = %v, want nil", agents)
	}
}
