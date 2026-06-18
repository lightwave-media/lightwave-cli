package github

import (
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
