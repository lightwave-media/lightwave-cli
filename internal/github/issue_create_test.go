package github

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestBuildIssueBody_FeatureRequest(t *testing.T) {
	body, err := BuildIssueBody(IssueCreateOpts{
		Kind:           KindFeatureRequest,
		Repo:           "lightwave-media/lightwave-core",
		Motivation:     "Need enforced issue filing",
		ProposedChange: "Add lw issue create",
		KindDetail:     "New schema",
		Refs:           []string{"lightwave-cli#150", "#285"},
		Origin:         "lightwave-ai#14",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"### Kind",
		"New schema",
		"### Motivation",
		"Need enforced issue filing",
		"### Proposed change",
		"Refs lightwave-media/lightwave-cli#150",
		"Refs lightwave-media/lightwave-core#285",
		"Origin: lightwave-media/lightwave-ai#14",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestBuildIssueBody_ToolGapRequiresProposedChange(t *testing.T) {
	_, err := BuildIssueBody(IssueCreateOpts{
		Kind:       KindToolGap,
		Motivation: "missing verb",
	})
	if err == nil {
		t.Fatal("expected error for missing proposed change")
	}
}

func TestBuildIssueBody_BugReport(t *testing.T) {
	body, err := BuildIssueBody(IssueCreateOpts{
		Kind:       KindBugReport,
		Scope:      "src/schemas/foo.yaml",
		Motivation: "1. run test\n2. fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "### Reproduction") {
		t.Fatalf("expected reproduction section: %s", body)
	}
}

func TestDefaultLabelsForKind(t *testing.T) {
	if got := DefaultLabelsForKind(KindToolGap); len(got) != 2 || got[0] != "tool-gap" {
		t.Fatalf("tool_gap labels: %v", got)
	}
}

// dryRunOutput runs CreateCompliantIssue in dry-run mode and returns what it printed.
func dryRunOutput(t *testing.T, opts IssueCreateOpts) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	stdout := os.Stdout
	os.Stdout = writer
	opts.DryRun = true
	_, createErr := CreateCompliantIssue(opts)
	os.Stdout = stdout

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if createErr != nil {
		t.Fatal(createErr)
	}

	return string(out)
}

func TestCreateCompliantIssue_LinksNoProjectByDefault(t *testing.T) {
	out := dryRunOutput(t, IssueCreateOpts{
		Repo:           "lightwave-media/lightwave-core",
		Title:          "no board",
		Motivation:     "the org board is closed",
		ProposedChange: "file the issue without linking a project",
	})
	if strings.Contains(out, "project:") {
		t.Fatalf("an issue filed without --project must not name a project:\n%s", out)
	}
}

func TestCreateCompliantIssue_LinksTheNamedProject(t *testing.T) {
	out := dryRunOutput(t, IssueCreateOpts{
		Repo:           "lightwave-media/lightwave-core",
		Title:          "named board",
		Motivation:     "a caller can still opt in",
		ProposedChange: "link the project the caller names",
		ProjectNumber:  7,
	})
	if !strings.Contains(out, "project: lightwave-media#7") {
		t.Fatalf("expected the named project in dry-run output:\n%s", out)
	}
}
