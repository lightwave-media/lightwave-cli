package mdtasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/mddocs"
)

// withFixtureRoot creates a fake lightwave monorepo root under t.TempDir()
// and returns its absolute path. docs/ exists; nothing else.
func withFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestNextTaskID_EmptyDocs(t *testing.T) {
	root := withFixtureRoot(t)
	id, err := NextTaskID(root)
	if err != nil {
		t.Fatalf("NextTaskID: %v", err)
	}
	if id != "T-0001" {
		t.Errorf("first id = %q, want T-0001", id)
	}
}

func TestNextTaskID_HighestPlusOne(t *testing.T) {
	root := withFixtureRoot(t)
	tasksDir := filepath.Join(root, "docs", "software", "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"T-0001-foo.md", "T-0007-bar.md", "T-0003-baz.md"} {
		if err := os.WriteFile(filepath.Join(tasksDir, name), []byte("---\nid: x\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id, err := NextTaskID(root)
	if err != nil {
		t.Fatalf("NextTaskID: %v", err)
	}
	if id != "T-0008" {
		t.Errorf("next id = %q, want T-0008", id)
	}
}

func TestNew_WritesFrontmatterAndBody(t *testing.T) {
	root := withFixtureRoot(t)

	path, id, err := New(NewOptions{
		LightwaveRoot:  root,
		Domain:         "software",
		Title:          "Audit CDN allowlist",
		Body:           "Steps:\n1. read assets.yaml\n2. diff against bucket",
		Owner:          "joel",
		AssignedTo:     "platform-engineer",
		ParentStory:    "US-003",
		AssignedSprint: "SPR-001",
		Priority:       "p2_high",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if id != "T-0001" {
		t.Errorf("id = %q, want T-0001", id)
	}
	if !strings.Contains(path, "T-0001-audit-cdn-allowlist.md") {
		t.Errorf("path = %q, expected slug T-0001-audit-cdn-allowlist", path)
	}

	a, err := mddocs.Parse(path)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	if a.Frontmatter.ID != "T-0001" {
		t.Errorf("frontmatter id = %q", a.Frontmatter.ID)
	}
	if a.Frontmatter.Domain != "software" {
		t.Errorf("domain = %q", a.Frontmatter.Domain)
	}
	if a.Frontmatter.Title != "Audit CDN allowlist" {
		t.Errorf("title = %q", a.Frontmatter.Title)
	}
	if a.Frontmatter.Owner != "joel" {
		t.Errorf("owner = %q", a.Frontmatter.Owner)
	}
	if a.Frontmatter.CreatedBy != "joel" {
		t.Errorf("created_by defaulted = %q, want joel", a.Frontmatter.CreatedBy)
	}
	if a.Frontmatter.AssignedTo != "platform-engineer" {
		t.Errorf("assigned_to = %q", a.Frontmatter.AssignedTo)
	}
	if a.Frontmatter.ParentStory != "US-003" {
		t.Errorf("parent_story = %q", a.Frontmatter.ParentStory)
	}
	if a.Frontmatter.AssignedSprint != "SPR-001" {
		t.Errorf("assigned_sprint = %q", a.Frontmatter.AssignedSprint)
	}
	if prio, _ := a.Frontmatter.Extra["priority"].(string); prio != "p2_high" {
		t.Errorf("priority = %v", a.Frontmatter.Extra["priority"])
	}
	if !strings.Contains(a.Body, "Steps:") {
		t.Errorf("body lost: %q", a.Body)
	}
}

func TestNew_RejectsMissingRequired(t *testing.T) {
	root := withFixtureRoot(t)
	cases := []NewOptions{
		{LightwaveRoot: "", Domain: "x", Title: "y"},
		{LightwaveRoot: root, Domain: "", Title: "y"},
		{LightwaveRoot: root, Domain: "x", Title: ""},
	}
	for _, opts := range cases {
		if _, _, err := New(opts); err == nil {
			t.Errorf("New(%+v) should have errored", opts)
		}
	}
}

func TestEdit_UpdatesAndPreservesUnknownKeys(t *testing.T) {
	root := withFixtureRoot(t)
	_, id, err := New(NewOptions{
		LightwaveRoot: root,
		Domain:        "software",
		Title:         "Test",
		Owner:         "joel",
		Priority:      "p3_medium",
	})
	if err != nil {
		t.Fatal(err)
	}

	status := "ready"
	priority := "p1_urgent"
	path, err := Edit(id, EditOptions{
		LightwaveRoot: root,
		Status:        &status,
		Priority:      &priority,
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	a, err := mddocs.Parse(path)
	if err != nil {
		t.Fatalf("Parse after Edit: %v", err)
	}
	if a.Frontmatter.Status != "ready" {
		t.Errorf("status = %q, want ready", a.Frontmatter.Status)
	}
	if prio, _ := a.Frontmatter.Extra["priority"].(string); prio != "p1_urgent" {
		t.Errorf("priority = %v, want p1_urgent", a.Frontmatter.Extra["priority"])
	}
}

func TestClose_SetsStatusDone(t *testing.T) {
	root := withFixtureRoot(t)
	_, id, err := New(NewOptions{
		LightwaveRoot: root,
		Domain:        "software",
		Title:         "Test close",
	})
	if err != nil {
		t.Fatal(err)
	}

	path, err := Close(id, root, "")
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	a, err := mddocs.Parse(path)
	if err != nil {
		t.Fatalf("Parse after Close: %v", err)
	}
	if a.Frontmatter.Status != "done" {
		t.Errorf("status = %q, want done", a.Frontmatter.Status)
	}
}

func TestSplitFrontmatter(t *testing.T) {
	body := "---\nid: T-1\ntitle: x\n---\nhello world\n"
	fm, rest, err := splitFrontmatter([]byte(body))
	if err != nil {
		t.Fatalf("splitFrontmatter: %v", err)
	}
	if !strings.Contains(string(fm), "id: T-1") {
		t.Errorf("frontmatter = %q", fm)
	}
	if !strings.Contains(rest, "hello world") {
		t.Errorf("body = %q", rest)
	}

	// Missing closing fence.
	if _, _, err := splitFrontmatter([]byte("---\nid: T-1\n")); err == nil {
		t.Error("expected unterminated-frontmatter error")
	}
	// Missing leading fence.
	if _, _, err := splitFrontmatter([]byte("no fence here")); err == nil {
		t.Error("expected missing-fence error")
	}
}
