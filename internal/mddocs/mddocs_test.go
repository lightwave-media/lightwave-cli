package mddocs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testRoot returns the fixture LightWave root: testdata/ stands in for
// `~/dev/lightwave-media/` and contains a docs/ subtree mirroring the
// real layout (docs/<domain>/<kind>/<ID>-*.md).
func testRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("abs testdata: %v", err)
	}
	return abs
}

func TestKindFromID(t *testing.T) {
	cases := []struct {
		id       string
		wantKind Kind
		wantOK   bool
	}{
		{"T-0001", KindTask, true},
		{"US-001", KindUserStory, true},
		{"EB-001", KindEpicBrief, true},
		{"SPR-001", KindSprint, true},
		{"DDD-foo", KindDDD, true},
		{"IP-bar", KindIP, true},
		{"X-0001", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := KindFromID(tc.id)
		if ok != tc.wantOK || got != tc.wantKind {
			t.Errorf("KindFromID(%q) = (%q, %v), want (%q, %v)",
				tc.id, got, ok, tc.wantKind, tc.wantOK)
		}
	}
}

func TestParse_HappyPath(t *testing.T) {
	a, err := Parse(filepath.Join(testRoot(t), "docs/software/tasks/T-0001-smoke.md"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a.Frontmatter.ID != "T-0001" {
		t.Errorf("ID = %q, want T-0001", a.Frontmatter.ID)
	}
	if a.Frontmatter.ParentStory != "US-001" {
		t.Errorf("ParentStory = %q, want US-001", a.Frontmatter.ParentStory)
	}
	if a.Frontmatter.RefsSAD != "../../architecture/SAD-stub.md" {
		t.Errorf("RefsSAD = %q", a.Frontmatter.RefsSAD)
	}
	if !strings.Contains(a.Body, "Body of the smoke task") {
		t.Errorf("Body missing expected text: %q", a.Body)
	}
	// Extra captures unknown YAML keys (priority, etc.)
	if a.Frontmatter.Extra["priority"] != "p2_high" {
		t.Errorf("Extra[priority] = %v, want p2_high", a.Frontmatter.Extra["priority"])
	}
}

func TestParse_MissingFrontmatterFence(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.md")
	if err := writeFile(bad, "no fence at top\n# Title\nbody\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), "missing frontmatter") {
		t.Errorf("expected missing-frontmatter error, got %v", err)
	}
}

func TestParse_UnterminatedFrontmatter(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.md")
	if err := writeFile(bad, "---\nid: T-0001\ntitle: never closed\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), "unterminated frontmatter") {
		t.Errorf("expected unterminated-frontmatter error, got %v", err)
	}
}

func TestFindByID_HappyPath(t *testing.T) {
	a, err := FindByID(testRoot(t), "software", "T-0001")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if a.Frontmatter.ID != "T-0001" {
		t.Errorf("ID = %q", a.Frontmatter.ID)
	}
}

func TestFindByID_AcrossDomains(t *testing.T) {
	// Empty domain → search every domain under docs/.
	a, err := FindByID(testRoot(t), "", "EB-001")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if a.Frontmatter.ID != "EB-001" {
		t.Errorf("ID = %q", a.Frontmatter.ID)
	}
}

func TestFindByID_NotFound(t *testing.T) {
	_, err := FindByID(testRoot(t), "software", "T-9999")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFindByID_UnrecognisedPrefix(t *testing.T) {
	_, err := FindByID(testRoot(t), "software", "Z-0001")
	if err == nil || !strings.Contains(err.Error(), "unrecognised artefact id") {
		t.Errorf("expected unrecognised-prefix error, got %v", err)
	}
}

func TestBuildBundle_FullChain(t *testing.T) {
	root := testRoot(t)
	seed, err := FindByID(root, "software", "T-0001")
	if err != nil {
		t.Fatalf("FindByID seed: %v", err)
	}

	b, err := BuildBundle(root, seed)
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}

	if b.Seed.Frontmatter.ID != "T-0001" {
		t.Errorf("seed = %q", b.Seed.Frontmatter.ID)
	}
	if b.Story == nil || b.Story.Frontmatter.ID != "US-001" {
		t.Errorf("story = %+v", b.Story)
	}
	if b.Epic == nil || b.Epic.Frontmatter.ID != "EB-001" {
		t.Errorf("epic = %+v", b.Epic)
	}
	if b.Sprint == nil || b.Sprint.Frontmatter.ID != "SPR-001" {
		t.Errorf("sprint = %+v", b.Sprint)
	}
	if len(b.Refs) != 1 || b.Refs[0].Kind != "SAD" {
		t.Errorf("refs = %+v", b.Refs)
	}
	if len(b.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", b.Warnings)
	}

	rendered := b.Render()
	for _, want := range []string{
		"# Context Bundle",
		"`T-0001`",
		"## Task — T-0001",
		"## User Story — US-001",
		"## Epic Brief — EB-001",
		"## Sprint — SPR-001",
		"## SAD reference",
		"Stub System Architecture Document",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered bundle missing %q\n---\n%s", want, rendered)
		}
	}
}

func TestBuildBundle_WarnsOnMissingParents(t *testing.T) {
	root := testRoot(t)
	seed, err := FindByID(root, "software", "T-0002")
	if err != nil {
		t.Fatalf("FindByID seed: %v", err)
	}

	b, err := BuildBundle(root, seed)
	if err != nil {
		t.Fatalf("BuildBundle: %v", err)
	}

	if b.Story != nil {
		t.Errorf("expected story=nil, got %+v", b.Story)
	}
	if b.Epic != nil {
		t.Errorf("expected epic=nil, got %+v", b.Epic)
	}
	// Warnings: US-999, EB-999, SAD-missing
	if len(b.Warnings) < 3 {
		t.Errorf("expected at least 3 warnings, got %d: %v", len(b.Warnings), b.Warnings)
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}
