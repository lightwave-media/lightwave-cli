package docsfactory_test

import (
	"testing"

	"github.com/lightwave-media/lightwave-cli/internal/docsfactory"
	"github.com/stretchr/testify/assert"
)

// fakeHandoffSchemas injects the two closed enum sets so the handoff linter is
// testable without a checked-out lightwave-core (mirrors fakeSchemas()).
func fakeHandoffSchemas() *docsfactory.Schemas {
	return &docsfactory.Schemas{
		HandoffBlockKinds: []string{"check", "command", "inputs", "template", "admonition"},
		HandoffStatuses:   []string{"received", "triaged", "partially_accepted", "accepted", "rejected", "in_progress", "completed"},
	}
}

func sp(s string) *string { return &s }

// validDoc is a clean handoff: inputs S1 → command S2 (consumes+depends S1) →
// check S3 (depends S2). Each per-rule test mutates a copy to trip one rule.
func validDoc() *docsfactory.HandoffDoc {
	return &docsfactory.HandoffDoc{
		ID:     "2026-06-13-test-handoff",
		Status: "received",
		Requests: []docsfactory.HandoffRequest{
			{RequestID: "R1"}, {RequestID: "R2"},
		},
		Steps: []docsfactory.HandoffStep{
			{StepID: "S1", Kind: "inputs", Produces: []string{"k"}, RequestRef: sp("R2")},
			{StepID: "S2", Kind: "command", DependsOn: []string{"S1"}, Consumes: []string{"S1"}, Produces: []string{"p"}, Success: sp("ok"), Failure: sp("bad"), RequestRef: sp("R2")},
			{StepID: "S3", Kind: "check", DependsOn: []string{"S2"}, Success: sp("ok"), Failure: sp("bad")},
		},
	}
}

func rulesFired(res *docsfactory.HandoffLintResult) map[string]string {
	out := map[string]string{}
	for _, v := range res.Violations {
		out[v.Rule] = v.Message // last message for that rule is fine for assertions
	}
	return out
}

func TestLintHandoff_GoldenValid_IsClean(t *testing.T) {
	t.Parallel()
	res := docsfactory.LintHandoff(validDoc(), fakeHandoffSchemas())
	assert.Empty(t, res.Violations, "expected a clean handoff, got: %+v", res.Violations)
	assert.Equal(t, 3, res.TotalSteps)
}

func TestLintHandoff_NegotiationOnly_NoSteps_IsClean(t *testing.T) {
	t.Parallel()
	doc := &docsfactory.HandoffDoc{ID: "x", Status: "received"}
	res := docsfactory.LintHandoff(doc, fakeHandoffSchemas())
	assert.Empty(t, res.Violations)
	assert.Equal(t, 0, res.TotalSteps)
}

func TestLintHandoff_InputsSuccessFailureOptional(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	// S1 is inputs with neither success nor failure → must stay clean.
	res := docsfactory.LintHandoff(doc, fakeHandoffSchemas())
	assert.Empty(t, res.Violations)
}

func TestLintHandoff_DuplicateStepID(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps = append(doc.Steps, docsfactory.HandoffStep{StepID: "S1", Kind: "inputs"})
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "step_id_unique")
	assert.Contains(t, got["step_id_unique"], "duplicate step_id")
}

func TestLintHandoff_StepIDMissing(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps[2].StepID = ""
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "step_id_present")
}

func TestLintHandoff_DanglingDependsOn(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps[2].DependsOn = []string{"SX"}
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "depends_on_resolves")
	assert.Contains(t, got["depends_on_resolves"], "SX")
}

func TestLintHandoff_DanglingConsumes(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps[1].Consumes = []string{"SX"}
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "consumes_resolves")
}

func TestLintHandoff_Cycle(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps[0].DependsOn = []string{"S2"} // S1→S2 and S2→S1 = cycle
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "dag_acyclic")
	assert.Contains(t, got["dag_acyclic"], "cycle")
}

func TestLintHandoff_SelfDependency(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps[1].DependsOn = []string{"S1", "S2"}
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "no_self_dependency")
	assert.Contains(t, got["no_self_dependency"], "itself")
}

func TestLintHandoff_AdmonitionWithSuccess(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps[2].Kind = "admonition" // S3 already carries success + failure
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "success_failure_contract")
	assert.Contains(t, got["success_failure_contract"], "must not carry")
}

func TestLintHandoff_CommandMissingSuccess(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps[1].Success = nil
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "success_failure_contract")
	assert.Contains(t, got["success_failure_contract"], "requires success")
}

func TestLintHandoff_CommandBlankSuccessFails(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps[1].Success = sp("   ") // present but blank → still fails REQUIRED
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "success_failure_contract")
}

func TestLintHandoff_BadKind(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps[2].Kind = "frobnicate"
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "kind_enum")
	assert.Contains(t, got["kind_enum"], "frobnicate")
}

func TestLintHandoff_UnresolvedRequestRef(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps[0].RequestRef = sp("R9")
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "request_ref_resolves")
}

func TestLintHandoff_BadStatus(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Status = "frozen"
	res := docsfactory.LintHandoff(doc, fakeHandoffSchemas())
	got := rulesFired(res)
	assert.Contains(t, got, "status_enum")
	// status_enum is a handoff-level violation (empty StepID).
	var found bool
	for _, v := range res.Violations {
		if v.Rule == "status_enum" {
			found = v.StepID == ""
		}
	}
	assert.True(t, found, "status_enum should be handoff-level (StepID empty)")
}

func TestLintHandoff_ConsumesImpliesDependsOn(t *testing.T) {
	t.Parallel()
	doc := validDoc()
	doc.Steps[1].DependsOn = nil // S2 still consumes S1 but no longer depends_on it
	got := rulesFired(docsfactory.LintHandoff(doc, fakeHandoffSchemas()))
	assert.Contains(t, got, "consumes_implies_depends_on")
	assert.Contains(t, got["consumes_implies_depends_on"], "⊇")
}

func TestLintHandoff_FromYAML_GoldenExampleShape(t *testing.T) {
	t.Parallel()
	const y = `
id: "2026-06-13-yaml-test"
status: "received"
requests:
  - request_id: "R1"
steps:
  - step_id: "S1"
    kind: "inputs"
    produces: ["k"]
  - step_id: "S2"
    kind: "command"
    depends_on: ["S1"]
    consumes: ["S1"]
    success: "done"
    failure: "broke"
    request_ref: "R1"
`
	doc, err := docsfactory.ParseHandoffYAML([]byte(y))
	assert.NoError(t, err)
	res := docsfactory.LintHandoff(doc, fakeHandoffSchemas())
	assert.Empty(t, res.Violations, "yaml golden should be clean: %v", res.Violations)
}
