package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pinStateDir redirects ~/.lightwave/agents to a temp dir for the duration
// of the test, so the suite never touches the real user home.
func pinStateDir(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir, err := StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	return dir
}

func TestNewID(t *testing.T) {
	id := NewID()
	if len(id) != 36 {
		t.Errorf("NewID() = %q (len=%d), expected 36-char UUID", id, len(id))
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	pinStateDir(t)

	now := time.Now().UTC().Truncate(time.Second)
	exit := 0
	want := &Agent{
		ID:        NewID(),
		TaskID:    "T-0001",
		Persona:   "platform-engineer",
		Repo:      "/repo",
		Worktree:  "/repo/.worktrees/agent-abc",
		Branch:    "feature/agent-t-0001-platform-engineer-abcdefab",
		Shell:     "claude",
		ShellArgs: []string{"-p"},
		PID:       99999,
		LogPath:   "/log",
		StartedAt: now,
		ExitedAt:  now.Add(time.Minute),
		ExitCode:  &exit,
		Status:    StatusExited,
	}
	if err := want.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(want.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.TaskID != want.TaskID || got.Branch != want.Branch || got.PID != want.PID {
		t.Errorf("Load mismatch:\nwant %+v\ngot  %+v", want, got)
	}
	if got.Status != StatusExited {
		t.Errorf("Status = %q, want exited", got.Status)
	}
}

func TestLoad_PrefixMatch(t *testing.T) {
	pinStateDir(t)

	a := &Agent{ID: NewID(), StartedAt: time.Now()}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load(a.ID[:8])
	if err != nil {
		t.Fatalf("prefix Load: %v", err)
	}
	if got.ID != a.ID {
		t.Errorf("prefix Load id = %q, want %q", got.ID, a.ID)
	}
}

func TestLoad_AmbiguousPrefix(t *testing.T) {
	pinStateDir(t)

	// Save two agents whose UUIDs share the same first character: collisions
	// are vanishingly rare with real UUIDs, so use fabricated IDs instead.
	dir, _ := StateDir()
	for _, id := range []string{"abc-1", "abc-2"} {
		path := filepath.Join(dir, id+".json")
		if err := os.WriteFile(path, []byte(`{"id":"`+id+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_, err := Load("abc")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got %v", err)
	}
}

func TestList_NewestFirst(t *testing.T) {
	pinStateDir(t)

	older := &Agent{ID: NewID(), StartedAt: time.Now().Add(-1 * time.Hour)}
	newer := &Agent{ID: NewID(), StartedAt: time.Now()}
	if err := older.Save(); err != nil {
		t.Fatal(err)
	}
	if err := newer.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List len = %d, want 2", len(got))
	}
	if got[0].ID != newer.ID {
		t.Errorf("List[0] = %q, want newer %q", got[0].ID, newer.ID)
	}
}

func TestLoadPersonaPrompt_OverrideDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LW_PERSONA_DIR", dir)

	want := "name: test-persona\nrole: tester\n"
	if err := os.WriteFile(filepath.Join(dir, "tester.yaml"), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}

	body, path, err := LoadPersonaPrompt("tester")
	if err != nil {
		t.Fatalf("LoadPersonaPrompt: %v", err)
	}
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	if !strings.HasSuffix(path, "tester.yaml") {
		t.Errorf("path = %q, expected to end with tester.yaml", path)
	}
}

func TestLoadPersonaPrompt_NotFound(t *testing.T) {
	t.Setenv("LW_PERSONA_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	_, _, err := LoadPersonaPrompt("nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, ok := err.(*PersonaNotFoundError); !ok {
		t.Errorf("expected PersonaNotFoundError, got %T: %v", err, err)
	}
}

func TestPidAlive_CurrentProcess(t *testing.T) {
	alive, err := pidAlive(os.Getpid())
	if err != nil {
		t.Fatalf("pidAlive: %v", err)
	}
	if !alive {
		t.Error("current process reported as not alive")
	}
}

func TestPidAlive_DeadPID(t *testing.T) {
	// PID 1 is launchd on macOS — always alive — so we can't easily test a
	// definitely-dead PID without a race. Spawn a child that exits, then
	// poll. The harness uses cmd.Wait() so the kernel has finalised the
	// reap before we check.
	cmd := exec.Command("/usr/bin/true")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn /usr/bin/true: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	alive, err := pidAlive(pid)
	if err != nil {
		t.Fatalf("pidAlive: %v", err)
	}
	if alive {
		t.Errorf("pid %d reported as alive after Wait()", pid)
	}
}

func TestCreateWorktree_HappyPath(t *testing.T) {
	repo := initBareRepo(t)
	id := NewID()

	worktree, branch, err := CreateWorktree(WorktreeOptions{
		Repo:    repo,
		TaskID:  "T-0001",
		Persona: "platform-engineer",
		AgentID: id,
	})
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	short := id[:8]
	wantBranch := "feature/agent-t-0001-platform-engineer-" + short
	if branch != wantBranch {
		t.Errorf("branch = %q, want %q", branch, wantBranch)
	}
	wantSuffix := filepath.Join(".worktrees", "agent-"+short)
	if !strings.HasSuffix(worktree, wantSuffix) {
		t.Errorf("worktree = %q, want suffix %q", worktree, wantSuffix)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Errorf("worktree dir not created: %v", err)
	}

	// Cleanup: remove the worktree to keep the temp dir clean.
	if err := RemoveWorktree(repo, worktree, branch); err != nil {
		t.Errorf("RemoveWorktree: %v", err)
	}
}

func TestSpawn_QuickExit(t *testing.T) {
	pinStateDir(t)
	repo := initBareRepo(t)

	a, err := Spawn(SpawnOptions{
		ID:        NewID(),
		TaskID:    "T-0001",
		Persona:   "platform-engineer",
		Repo:      repo,
		Shell:     "/bin/sh",
		ShellArgs: []string{"-c", "echo hello && exit 0"},
		// PromptStdin=false so the prompt is passed as final argv; the
		// `-c <script>` form ignores additional argv beyond the script, so
		// the prompt is harmless here.
		Prompt: "ignored",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if a.PID == 0 {
		t.Fatalf("expected PID > 0, got 0 (status=%s err=%s)", a.Status, a.Error)
	}
	if a.Status != StatusRunning {
		t.Errorf("status = %q, want running", a.Status)
	}

	// Poll briefly until the child exits + RefreshStatus catches it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := RefreshStatus(a); err != nil {
			t.Fatalf("RefreshStatus: %v", err)
		}
		if a.Status == StatusExited {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if a.Status != StatusExited {
		t.Errorf("status after wait = %q, want exited", a.Status)
	}

	// Cleanup
	_ = RemoveWorktree(a.Repo, a.Worktree, a.Branch)
}

// initBareRepo creates a fresh git repo in a temp dir, makes one commit,
// and returns the repo path. Needed for `git worktree add` (which refuses
// on an empty repo).
func initBareRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
	return repo
}
