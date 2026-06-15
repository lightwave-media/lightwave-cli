package docsfactory

import (
	"fmt"
	"slices"
	"strings"
)

// HandoffViolation is one failure of the handoff step contract. StepID is ""
// for handoff-level violations (e.g. a bad status). Rule is a stable id so the
// report and tests can key off it.
type HandoffViolation struct {
	StepID  string
	Rule    string
	Message string
}

// HandoffLintResult bundles a `lw lint handoff` pass.
type HandoffLintResult struct {
	Path       string
	TotalSteps int
	Violations []HandoffViolation
}

// kindsRequiringContract carry a mandatory success+failure pair.
var kindsRequiringContract = map[string]bool{"check": true, "command": true, "template": true}

// LintHandoff enforces the per-step contract from
// spec/sad/0001 § "The HandoffStep contract" against a loaded handoff. It is
// report-all (every rule runs; violations accumulate). schemas supplies the two
// closed enums. A negotiation-only handoff (no steps[]) is valid — only the
// status enum is checked.
func LintHandoff(doc *HandoffDoc, schemas *Schemas) *HandoffLintResult {
	res := &HandoffLintResult{TotalSteps: len(doc.Steps)}
	add := func(stepID, rule, msg string) {
		res.Violations = append(res.Violations, HandoffViolation{StepID: stepID, Rule: rule, Message: msg})
	}

	// R10 status_enum (handoff-level). A required field is absent → leave to the
	// data-schema validator; this step-contract linter only rejects a present
	// status that is not in the closed set.
	if doc.Status != "" && len(schemas.HandoffStatuses) > 0 && !slices.Contains(schemas.HandoffStatuses, doc.Status) {
		add("", "status_enum", fmt.Sprintf("status %q not in [%s]", doc.Status, strings.Join(schemas.HandoffStatuses, ", ")))
	}

	if len(doc.Steps) == 0 {
		return res // negotiation-only handoff
	}

	// Build the step-id set + flag duplicates / missing ids (R2, R3).
	ids := make(map[string]bool, len(doc.Steps))
	seen := make(map[string]bool, len(doc.Steps))
	for _, st := range doc.Steps {
		if strings.TrimSpace(st.StepID) == "" {
			add("", "step_id_present", "a step is missing step_id")
			continue
		}
		if seen[st.StepID] {
			add(st.StepID, "step_id_unique", fmt.Sprintf("duplicate step_id %q", st.StepID))
			continue
		}
		seen[st.StepID] = true
		ids[st.StepID] = true
	}

	// Request ids for R7.
	reqIDs := make(map[string]bool, len(doc.Requests))
	for _, r := range doc.Requests {
		reqIDs[r.RequestID] = true
	}

	for _, st := range doc.Steps {
		// R1 kind_enum
		kindKnown := slices.Contains(schemas.HandoffBlockKinds, st.Kind)
		if !kindKnown {
			add(st.StepID, "kind_enum", fmt.Sprintf("kind %q not in [%s]", st.Kind, strings.Join(schemas.HandoffBlockKinds, ", ")))
		}

		// R6 success_failure_contract (only meaningful for a known kind)
		if kindKnown {
			switch {
			case kindsRequiringContract[st.Kind]:
				if !present(st.Success) {
					add(st.StepID, "success_failure_contract", fmt.Sprintf("%s step %q requires success", st.Kind, st.StepID))
				}
				if !present(st.Failure) {
					add(st.StepID, "success_failure_contract", fmt.Sprintf("%s step %q requires failure", st.Kind, st.StepID))
				}
			case st.Kind == "admonition":
				if st.Success != nil {
					add(st.StepID, "success_failure_contract", fmt.Sprintf("admonition step %q must not carry success", st.StepID))
				}
				if st.Failure != nil {
					add(st.StepID, "success_failure_contract", fmt.Sprintf("admonition step %q must not carry failure", st.StepID))
				}
				// "inputs" → success/failure optional; no check.
			}
		}

		// R4 depends_on_resolves + R8 no_self_dependency
		for _, dep := range st.DependsOn {
			if dep == st.StepID {
				add(st.StepID, "no_self_dependency", fmt.Sprintf("step %q depends on itself", st.StepID))
				continue
			}
			if !ids[dep] {
				add(st.StepID, "depends_on_resolves", fmt.Sprintf("depends_on references unknown step %q", dep))
			}
		}

		// R5 consumes_resolves + R8 (self) + R11 consumes_implies_depends_on
		for _, c := range st.Consumes {
			if c == st.StepID {
				add(st.StepID, "no_self_dependency", fmt.Sprintf("step %q consumes itself", st.StepID))
				continue
			}
			if !ids[c] {
				add(st.StepID, "consumes_resolves", fmt.Sprintf("consumes references unknown step %q", c))
				continue
			}
			if !slices.Contains(st.DependsOn, c) {
				add(st.StepID, "consumes_implies_depends_on", fmt.Sprintf("step %q consumes %q but does not depends_on it (depends_on ⊇ consumes)", st.StepID, c))
			}
		}

		// R7 request_ref_resolves
		if st.RequestRef != nil && *st.RequestRef != "" && !reqIDs[*st.RequestRef] {
			add(st.StepID, "request_ref_resolves", fmt.Sprintf("request_ref %q matches no request_id in requests[]", *st.RequestRef))
		}
	}

	// R9 dag_acyclic — depends_on graph, edges only to resolving ids.
	for _, cyc := range detectCycles(doc.Steps, ids) {
		add(cyc[0], "dag_acyclic", "dependency cycle: "+strings.Join(cyc, " → "))
	}

	return res
}

// present reports whether an optional contract string is set and non-blank.
// nil = omitted, &"" = present-but-blank — both fail a REQUIRED kind.
func present(s *string) bool {
	return s != nil && strings.TrimSpace(*s) != ""
}

// detectCycles finds dependency cycles in the depends_on graph via three-color
// DFS, returning each distinct cycle as an ordered, closed path
// (S1 → S2 → S1). Self-edges are handled by R8 and excluded here. Only edges to
// ids that resolve are followed (dangling refs are R4's job).
func detectCycles(steps []HandoffStep, ids map[string]bool) [][]string {
	adj := make(map[string][]string, len(steps))
	order := make([]string, 0, len(steps))
	for _, st := range steps {
		if st.StepID == "" || !ids[st.StepID] {
			continue
		}
		if _, ok := adj[st.StepID]; !ok {
			order = append(order, st.StepID)
		}
		for _, dep := range st.DependsOn {
			if dep != st.StepID && ids[dep] {
				adj[st.StepID] = append(adj[st.StepID], dep)
			}
		}
		if _, ok := adj[st.StepID]; !ok {
			adj[st.StepID] = nil
		}
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	state := make(map[string]int, len(order))
	var stack []string
	var found [][]string
	seenCycle := make(map[string]bool)

	var dfs func(node string)
	dfs = func(node string) {
		state[node] = gray
		stack = append(stack, node)
		for _, nb := range adj[node] {
			switch state[nb] {
			case white:
				dfs(nb)
			case gray:
				// Cycle: from nb's position in the stack to the top, closed by nb.
				idx := slices.Index(stack, nb)
				if idx >= 0 {
					cyc := append(append([]string{}, stack[idx:]...), nb)
					if key := cycleKey(cyc); !seenCycle[key] {
						seenCycle[key] = true
						found = append(found, cyc)
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = black
	}

	for _, node := range order {
		if state[node] == white {
			dfs(node)
		}
	}
	return found
}

// cycleKey normalizes a cycle (rotate to its smallest member) so the same loop
// discovered from different entry points dedupes to one violation.
func cycleKey(cyc []string) string {
	if len(cyc) <= 1 {
		return strings.Join(cyc, ">")
	}
	nodes := cyc[:len(cyc)-1] // drop the repeated closing node
	min := 0
	for i := range nodes {
		if nodes[i] < nodes[min] {
			min = i
		}
	}
	rot := append(append([]string{}, nodes[min:]...), nodes[:min]...)
	return strings.Join(rot, ">")
}
