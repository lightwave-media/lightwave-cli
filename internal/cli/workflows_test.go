package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildRPIPayloadMapsGovernanceAndLineageFields(t *testing.T) {
	opts := rpiRequestOptions{
		Goal:                   "Align CLI governance",
		Workspace:              "/tmp/workspace",
		AgentType:              "software_architect",
		UserID:                 "user-1",
		ConversationID:         "run-123",
		IdempotencyKey:         "idem-123",
		WorkflowProfile:        "general",
		RequiredAWSProfile:     "platform-dev",
		TargetEnvironment:      "staging",
		RequireTraceability:    true,
		RequireApproval:        true,
		ApprovalDecision:       "approve",
		RequireEvidence:        true,
		EnforcePreflight:       true,
		EnforceTerragruntReads: false,
		PRDID:                  "PRD-01",
		EpicID:                 "EPIC-06",
		UserStoryID:            "US-1",
		SprintID:               "SPRINT-24",
		TaskID:                 "TASK-4",
		DDDRef:                 "ddd://context",
		APISpecRef:             "openapi://spec",
		ImplementationPlanRef:  "plan://epic06",
	}

	payload := buildRPIPayload(opts)

	assertMapValue(t, payload, "goal", opts.Goal)
	assertMapValue(t, payload, "workspace", opts.Workspace)
	assertMapValue(t, payload, "agent_type", opts.AgentType)
	assertMapValue(t, payload, "user_id", opts.UserID)
	assertMapValue(t, payload, "conversation_id", opts.ConversationID)
	assertMapValue(t, payload, "session_key", opts.IdempotencyKey)
	assertMapValue(t, payload, "require_traceability", opts.RequireTraceability)
	assertMapValue(t, payload, "require_approval", opts.RequireApproval)
	assertMapValue(t, payload, "approval_decision", opts.ApprovalDecision)
	assertMapValue(t, payload, "require_evidence_contract", opts.RequireEvidence)
	assertMapValue(t, payload, "prd_id", opts.PRDID)
	assertMapValue(t, payload, "epic_id", opts.EpicID)
	assertMapValue(t, payload, "user_story_id", opts.UserStoryID)
	assertMapValue(t, payload, "sprint_id", opts.SprintID)
	assertMapValue(t, payload, "task_id", opts.TaskID)
	assertMapValue(t, payload, "ddd_ref", opts.DDDRef)
	assertMapValue(t, payload, "api_spec_ref", opts.APISpecRef)
	assertMapValue(t, payload, "implementation_plan_ref", opts.ImplementationPlanRef)
}

func TestBuildRPIPayloadOmitsBlankApprovalDecision(t *testing.T) {
	opts := rpiRequestOptions{
		Goal:             "Test",
		Workspace:        "/tmp/workspace",
		AgentType:        "software_architect",
		UserID:           "user-1",
		ConversationID:   "run-123",
		IdempotencyKey:   "idem-123",
		ApprovalDecision: "  ",
	}

	payload := buildRPIPayload(opts)
	if _, exists := payload["approval_decision"]; exists {
		t.Fatal("expected approval_decision to be omitted when blank")
	}
}

func TestNormalizeRunMetadataGeneratesDefaults(t *testing.T) {
	runID, key := normalizeRunMetadata("", "", func() string {
		return "run-generated"
	})

	if runID != "run-generated" {
		t.Fatalf("expected generated run id, got %q", runID)
	}
	if key != "workflow:rpi:run-generated" {
		t.Fatalf("expected generated idempotency key, got %q", key)
	}
}

func TestNormalizeRunMetadataRespectsInputs(t *testing.T) {
	runID, key := normalizeRunMetadata("run-fixed", "idem-fixed", func() string {
		return "ignored"
	})

	if runID != "run-fixed" {
		t.Fatalf("expected provided run id, got %q", runID)
	}
	if key != "idem-fixed" {
		t.Fatalf("expected provided idempotency key, got %q", key)
	}
}

func TestFormatContractHTTPErrorRendersContractFields(t *testing.T) {
	body := []byte(`{
		"ok": false,
		"error": {
			"code": "TRACEABILITY_MISSING",
			"message": "Missing required lineage field(s): prd_id",
			"category": "validation",
			"http_status": 422,
			"run_id": "run-contract",
			"retryable": false,
			"details": {
				"missing_fields": ["prd_id"],
				"required_fields": ["prd_id", "epic_id", "user_story_id", "sprint_id", "task_id"]
			}
		}
	}`)

	err := formatContractHTTPError(422, body, "run-fallback")
	if err == nil {
		t.Fatal("expected error")
	}

	msg := err.Error()
	if !strings.Contains(msg, `code=TRACEABILITY_MISSING`) {
		t.Fatalf("expected error code in output, got %q", msg)
	}
	if !strings.Contains(msg, `message="Missing required lineage field(s): prd_id"`) {
		t.Fatalf("expected message in output, got %q", msg)
	}
	if !strings.Contains(msg, `run_id=run-contract`) {
		t.Fatalf("expected run_id in output, got %q", msg)
	}
	if !strings.Contains(msg, `"missing_fields":["prd_id"]`) {
		t.Fatalf("expected compact details in output, got %q", msg)
	}
}

func TestFormatContractHTTPErrorFallsBackToGenericMessage(t *testing.T) {
	body := []byte(`{"status":"error","message":"bad request"}`)

	err := formatContractHTTPError(400, body, "run-fallback")
	if err == nil {
		t.Fatal("expected error")
	}

	msg := err.Error()
	if !strings.Contains(msg, `status=400`) {
		t.Fatalf("expected status in output, got %q", msg)
	}
	if !strings.Contains(msg, `message="bad request"`) {
		t.Fatalf("expected message in output, got %q", msg)
	}
	if !strings.Contains(msg, `run_id=run-fallback`) {
		t.Fatalf("expected fallback run_id in output, got %q", msg)
	}
}

func TestBuildRunsQueryMapsFiltersToAPIKeys(t *testing.T) {
	query := buildRunsQuery(runsFilterOptions{
		RunID:    "run-1",
		Status:   "completed",
		Phase:    "done",
		AgentID:  "software_architect",
		PRDID:    "PRD-1",
		EpicID:   "EPIC-1",
		SprintID: "SPRINT-1",
		TaskID:   "TASK-1",
		Limit:    25,
	})

	assertQueryValue(t, query.Get("run_id"), "run-1")
	assertQueryValue(t, query.Get("status"), "completed")
	assertQueryValue(t, query.Get("phase"), "done")
	assertQueryValue(t, query.Get("agent_id"), "software_architect")
	assertQueryValue(t, query.Get("prd_id"), "PRD-1")
	assertQueryValue(t, query.Get("epic_id"), "EPIC-1")
	assertQueryValue(t, query.Get("sprint_id"), "SPRINT-1")
	assertQueryValue(t, query.Get("task_id"), "TASK-1")
	assertQueryValue(t, query.Get("limit"), "25")
}

func TestBuildDecisionsQueryMapsFiltersToAPIKeys(t *testing.T) {
	query := buildDecisionsQuery(decisionFilterOptions{
		DecisionStatus: "open",
		DecisionType:   "approval",
		DecisionRunID:  "run-7",
		Limit:          25,
	})

	assertQueryValue(t, query.Get("decision_status"), "open")
	assertQueryValue(t, query.Get("decision_type"), "approval")
	assertQueryValue(t, query.Get("decision_run_id"), "run-7")
	assertQueryValue(t, query.Get("limit"), "25")
}

func TestBuildReviewPayloadOmitsBlankOptionalFields(t *testing.T) {
	payload := buildReviewPayload(reviewRequestOptions{
		RunID:      "run-1",
		Action:     "approve",
		Actor:      "cli-operator",
		Notes:      "  ",
		DecisionID: "",
	})

	assertMapValue(t, payload, "action", "approve")
	assertMapValue(t, payload, "actor", "cli-operator")

	if _, exists := payload["notes"]; exists {
		t.Fatal("expected notes to be omitted when blank")
	}
	if _, exists := payload["decision_id"]; exists {
		t.Fatal("expected decision_id to be omitted when blank")
	}
}

func TestIsAllowedReviewAction(t *testing.T) {
	if !isAllowedReviewAction("approve") {
		t.Fatal("expected approve to be allowed")
	}
	if !isAllowedReviewAction("request_changes") {
		t.Fatal("expected request_changes to be allowed")
	}
	if !isAllowedReviewAction("defer") {
		t.Fatal("expected defer to be allowed")
	}
	if isAllowedReviewAction("ship_it") {
		t.Fatal("expected ship_it to be rejected")
	}
}

func TestEvidenceStatusesExtractsVerifyAndReview(t *testing.T) {
	verify, review := evidenceStatuses(map[string]any{
		"verify": map[string]any{"status": "pass"},
		"review": map[string]any{"status": "approved"},
	})

	if verify != "pass" {
		t.Fatalf("expected verify status pass, got %q", verify)
	}
	if review != "approved" {
		t.Fatalf("expected review status approved, got %q", review)
	}
}

func TestRunsResponseMappingReadsContractFields(t *testing.T) {
	raw := []byte(`{
		"status": "success",
		"count": 1,
		"runs": [
			{
				"run_id": "run-123",
				"status": "completed",
				"phase": "done",
				"agent_id": "software_architect",
				"prd_id": "PRD-1",
				"epic_id": "EPIC-6",
				"user_story_id": "US-5",
				"sprint_id": "SPRINT-2",
				"task_id": "TASK-4",
				"error_code": "",
				"error_message": "",
				"policy_results": {
					"traceability_required": true,
					"approval_required": false,
					"evidence_required": true
				},
				"evidence": {
					"verify": {"status": "pass"},
					"review": {"status": "approved"}
				}
			}
		]
	}`)

	var resp runsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("expected response to unmarshal: %v", err)
	}

	if resp.Count != 1 || len(resp.Runs) != 1 {
		t.Fatalf("expected one run in response, got count=%d len=%d", resp.Count, len(resp.Runs))
	}

	run := resp.Runs[0]
	if run.RunID != "run-123" || run.EpicID != "EPIC-6" || run.TaskID != "TASK-4" {
		t.Fatalf("unexpected run mapping: %+v", run)
	}

	verify, review := evidenceStatuses(run.Evidence)
	if verify != "pass" || review != "approved" {
		t.Fatalf("unexpected evidence statuses verify=%q review=%q", verify, review)
	}
}

func assertMapValue(t *testing.T, m map[string]any, key string, expected any) {
	t.Helper()
	value, ok := m[key]
	if !ok {
		t.Fatalf("expected payload key %q to exist", key)
	}
	if value != expected {
		t.Fatalf("expected payload[%q]=%v, got %v", key, expected, value)
	}
}

func assertQueryValue(t *testing.T, got, expected string) {
	t.Helper()
	if got != expected {
		t.Fatalf("expected query value %q, got %q", expected, got)
	}
}
