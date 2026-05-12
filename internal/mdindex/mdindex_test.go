package mdindex

import (
	"os"
	"path/filepath"
	"testing"
)

func pinHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// fixture monorepo with three artefacts under docs/software/.
func writeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	tasksDir := filepath.Join(root, "docs", "software", "tasks")
	storiesDir := filepath.Join(root, "docs", "software", "user-stories")
	epicsDir := filepath.Join(root, "docs", "software", "epic-briefs")
	for _, d := range []string{tasksDir, storiesDir, epicsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(tasksDir, "T-0001-foo.md"): "---\nid: T-0001\ndomain: software\ntype: task\ntitle: Foo task\nstatus: ready\nowner: joel\nparent_story: US-001\npriority: p2_high\n---\nbody\n",
		filepath.Join(tasksDir, "T-0002-bar.md"): "---\nid: T-0002\ndomain: software\ntype: task\ntitle: Bar task\nstatus: in_progress\nowner: cpe\nparent_epic: EB-001\n---\n",
		filepath.Join(storiesDir, "US-001-x.md"): "---\nid: US-001\ndomain: software\ntype: user-story\ntitle: A story\nstatus: ready\nparent_epic: EB-001\n---\n",
		filepath.Join(epicsDir, "EB-001-y.md"):   "---\nid: EB-001\ndomain: software\ntype: epic-brief\ntitle: An epic\nstatus: in_progress\n---\n",
		// Malformed file — exercises parse_errors counter.
		filepath.Join(tasksDir, "T-0099-bad.md"): "no frontmatter here\n",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestBuild_HappyPath(t *testing.T) {
	root := writeFixture(t)
	idx, err := Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if idx.Stats["total"] != 4 {
		t.Errorf("total = %d, want 4", idx.Stats["total"])
	}
	if idx.Stats["task"] != 2 || idx.Stats["user-story"] != 1 || idx.Stats["epic-brief"] != 1 {
		t.Errorf("stats wrong: %+v", idx.Stats)
	}
	if idx.Stats["parse_errors"] != 1 {
		t.Errorf("parse_errors = %d, want 1", idx.Stats["parse_errors"])
	}

	// Entries are sorted by (kind, ID).
	if idx.Entries[0].Kind != "epic-brief" || idx.Entries[0].ID != "EB-001" {
		t.Errorf("first entry = %+v", idx.Entries[0])
	}

	// Task entry preserves priority and parent_story.
	var foo Entry
	for _, e := range idx.Entries {
		if e.ID == "T-0001" {
			foo = e
		}
	}
	if foo.ParentStory != "US-001" {
		t.Errorf("T-0001 parent_story = %q", foo.ParentStory)
	}
	if foo.Priority != "p2_high" {
		t.Errorf("T-0001 priority = %q", foo.Priority)
	}
}

func TestBuild_MissingDocsDir(t *testing.T) {
	root := t.TempDir()
	idx, err := Build(root)
	if err != nil {
		t.Fatalf("Build on root without docs/ should succeed: %v", err)
	}
	if len(idx.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(idx.Entries))
	}
}

func TestWriteLoad_RoundTrip(t *testing.T) {
	pinHome(t)
	root := writeFixture(t)
	idx, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	path, err := idx.Write()
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("state file missing: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil {
		t.Fatal("Load returned nil after Write")
	}
	if got.Stats["total"] != idx.Stats["total"] {
		t.Errorf("round-trip total = %d, want %d", got.Stats["total"], idx.Stats["total"])
	}
}

func TestLoad_MissingFile(t *testing.T) {
	pinHome(t)
	got, err := Load()
	if err != nil {
		t.Fatalf("Load on fresh home: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestRebuildIsIdempotent(t *testing.T) {
	root := writeFixture(t)
	first, _ := Build(root)
	second, _ := Build(root)
	if first.Stats["total"] != second.Stats["total"] {
		t.Errorf("idempotent total differs: %d vs %d",
			first.Stats["total"], second.Stats["total"])
	}
	if len(first.Entries) != len(second.Entries) {
		t.Errorf("idempotent entry-count differs")
	}
	for i := range first.Entries {
		if first.Entries[i].ID != second.Entries[i].ID {
			t.Errorf("idempotent order differs at %d: %q vs %q",
				i, first.Entries[i].ID, second.Entries[i].ID)
		}
	}
}
