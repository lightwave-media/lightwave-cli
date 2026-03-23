package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/lightwave-media/lightwave-cli/internal/config"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

const workflowHTTPTimeout = 10 * time.Minute

var (
	workflowsBaseURL      string
	workflowsServiceToken string
	workflowsOutput       string

	rpiGoal                   string
	rpiWorkspace              string
	rpiAgentType              string
	rpiUserID                 string
	rpiConversationID         string
	rpiIdempotencyKey         string
	rpiWorkflowProfile        string
	rpiRequiredAWSProfile     string
	rpiTargetEnvironment      string
	rpiRequireTraceability    bool
	rpiRequireApproval        bool
	rpiApprovalDecision       string
	rpiRequireEvidence        bool
	rpiEnforcePreflight       bool
	rpiEnforceTerragruntReads bool

	rpiPRDID                 string
	rpiEpicID                string
	rpiUserStoryID           string
	rpiSprintID              string
	rpiTaskID                string
	rpiDDDRef                string
	rpiAPISpecRef            string
	rpiImplementationPlanRef string

	runsRunID    string
	runsStatus   string
	runsPhase    string
	runsAgentID  string
	runsPRDID    string
	runsEpicID   string
	runsSprintID string
	runsTaskID   string
	runsLimit    int

	decisionsStatus string
	decisionsType   string
	decisionsRunID  string
	decisionsLimit  int

	reviewRunID          string
	reviewAction         string
	reviewDecisionID     string
	reviewActor          string
	reviewNotes          string
	reviewClassification string

	kpisLimit int
)

var workflowsCmd = &cobra.Command{
	Use:   "workflows",
	Short: "Governed orchestrator workflows",
	Long: `Run and inspect governed orchestrator workflows.

Outputs are deterministic key=value or TSV by default, or JSON with --output=json.`,
}

var workflowsRPICmd = &cobra.Command{
	Use:   "rpi",
	Short: "Run Research-Plan-Implement workflow",
	Long: `Execute POST /api/workflows/rpi with governance-aware lineage, idempotency,
and policy/evidence flags.`,
	Example: `  lw workflows rpi --goal "Implement EPIC-06" --workspace /repo/path \
    --prd-id PRD-004 --epic-id EPIC-06 --user-story-id US-12 --sprint-id S24 --task-id TASK-99 \
    --require-traceability --idempotency-key epic06-task99`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkflowRPI(cmd.Context(), cmd.OutOrStdout())
	},
}

var workflowsRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List governed workflow runs",
	Long:  "Query GET /api/workflows/runs with registry filters.",
	Example: `  lw workflows runs --status completed --epic-id EPIC-06 --limit 25
  lw workflows runs --run-id 2f4f9f3d-f9e7-427d-a90f-13289a72a2fe --output json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkflowRuns(cmd.Context(), cmd.OutOrStdout())
	},
}

var workflowsDecisionsCmd = &cobra.Command{
	Use:   "decisions",
	Short: "List workflow decision queue items",
	Long:  "Query GET /api/workflows/decisions for open/resolved/deferred decision items.",
	Example: `  lw workflows decisions --decision-status open --limit 50
  lw workflows decisions --decision-run-id 2f4f9f3d-f9e7-427d-a90f-13289a72a2fe --output json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkflowDecisions(cmd.Context(), cmd.OutOrStdout())
	},
}

var workflowsReviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Apply review action to a workflow decision",
	Long:  "Execute POST /api/workflows/runs/:run_id/review for approve/request_changes/defer actions.",
	Example: `  lw workflows review --run-id 2f4f9f3d-f9e7-427d-a90f-13289a72a2fe --action approve
  lw workflows review --run-id 2f4f9f3d-f9e7-427d-a90f-13289a72a2fe --action request_changes --notes "missing tests"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkflowReview(cmd.Context(), cmd.OutOrStdout())
	},
}

var workflowsKPIsCmd = &cobra.Command{
	Use:   "kpis",
	Short: "Read workflow KPI launch-gate snapshot",
	Long:  "Query GET /api/workflows/kpis and print PRD KPI formula outputs.",
	Example: `  lw workflows kpis
  lw workflows kpis --limit 200 --output json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkflowKPIs(cmd.Context(), cmd.OutOrStdout())
	},
}

func init() {
	workflowsCmd.PersistentFlags().StringVar(&workflowsBaseURL, "orchestrator-url", "", "Orchestrator base URL (default: LW_ORCHESTRATOR_URL or derived from config)")
	workflowsCmd.PersistentFlags().StringVar(&workflowsServiceToken, "service-token", "", "Service token header value (default: LW_SERVICE_TOKEN or LW_AGENT_KEY)")
	workflowsCmd.PersistentFlags().StringVar(&workflowsOutput, "output", "text", "Output format: text|json")

	workflowsRPICmd.Flags().StringVar(&rpiGoal, "goal", "", "Workflow objective")
	workflowsRPICmd.Flags().StringVar(&rpiWorkspace, "workspace", "", "Workspace path for execution")
	workflowsRPICmd.Flags().StringVar(&rpiAgentType, "agent-type", "software_architect", "Agent type")
	workflowsRPICmd.Flags().StringVar(&rpiUserID, "user-id", "anonymous", "User identifier")
	workflowsRPICmd.Flags().StringVar(&rpiConversationID, "conversation-id", "", "Conversation ID used as run_id (auto-generated when omitted)")
	workflowsRPICmd.Flags().StringVar(&rpiIdempotencyKey, "idempotency-key", "", "Idempotency key (maps to API session_key; auto-generated when omitted)")
	workflowsRPICmd.Flags().StringVar(&rpiWorkflowProfile, "workflow-profile", "", "Optional workflow profile")
	workflowsRPICmd.Flags().StringVar(&rpiRequiredAWSProfile, "required-aws-profile", "", "Optional required AWS profile for preflight checks")
	workflowsRPICmd.Flags().StringVar(&rpiTargetEnvironment, "target-environment", "", "Optional target environment")
	workflowsRPICmd.Flags().BoolVar(&rpiRequireTraceability, "require-traceability", false, "Require full lineage IDs")
	workflowsRPICmd.Flags().BoolVar(&rpiRequireApproval, "require-approval", false, "Require explicit approval decision")
	workflowsRPICmd.Flags().StringVar(&rpiApprovalDecision, "approval-decision", "", "Approval decision (approve|deny|timeout)")
	workflowsRPICmd.Flags().BoolVar(&rpiRequireEvidence, "require-evidence-contract", false, "Require verify/review evidence contract")
	workflowsRPICmd.Flags().BoolVar(&rpiEnforcePreflight, "enforce-preflight", true, "Enforce preflight policy checks")
	workflowsRPICmd.Flags().BoolVar(&rpiEnforceTerragruntReads, "enforce-terragrunt-reads", false, "Enforce terragrunt read policy checks")

	workflowsRPICmd.Flags().StringVar(&rpiPRDID, "prd-id", "", "Lineage PRD ID")
	workflowsRPICmd.Flags().StringVar(&rpiEpicID, "epic-id", "", "Lineage epic ID")
	workflowsRPICmd.Flags().StringVar(&rpiUserStoryID, "user-story-id", "", "Lineage user story ID")
	workflowsRPICmd.Flags().StringVar(&rpiSprintID, "sprint-id", "", "Lineage sprint ID")
	workflowsRPICmd.Flags().StringVar(&rpiTaskID, "task-id", "", "Lineage task ID")
	workflowsRPICmd.Flags().StringVar(&rpiDDDRef, "ddd-ref", "", "Traceability DDD reference")
	workflowsRPICmd.Flags().StringVar(&rpiAPISpecRef, "api-spec-ref", "", "Traceability API spec reference")
	workflowsRPICmd.Flags().StringVar(&rpiImplementationPlanRef, "implementation-plan-ref", "", "Traceability implementation plan reference")

	_ = workflowsRPICmd.MarkFlagRequired("goal")
	_ = workflowsRPICmd.MarkFlagRequired("workspace")

	workflowsRunsCmd.Flags().StringVar(&runsRunID, "run-id", "", "Filter by run ID")
	workflowsRunsCmd.Flags().StringVar(&runsStatus, "status", "", "Filter by run status")
	workflowsRunsCmd.Flags().StringVar(&runsPhase, "phase", "", "Filter by run phase")
	workflowsRunsCmd.Flags().StringVar(&runsAgentID, "agent-id", "", "Filter by agent ID")
	workflowsRunsCmd.Flags().StringVar(&runsPRDID, "prd-id", "", "Filter by PRD ID")
	workflowsRunsCmd.Flags().StringVar(&runsEpicID, "epic-id", "", "Filter by epic ID")
	workflowsRunsCmd.Flags().StringVar(&runsSprintID, "sprint-id", "", "Filter by sprint ID")
	workflowsRunsCmd.Flags().StringVar(&runsTaskID, "task-id", "", "Filter by task ID")
	workflowsRunsCmd.Flags().IntVar(&runsLimit, "limit", 50, "Maximum runs to return")

	workflowsDecisionsCmd.Flags().StringVar(&decisionsStatus, "decision-status", "open", "Filter decisions by status: open|resolved|deferred|all")
	workflowsDecisionsCmd.Flags().StringVar(&decisionsType, "decision-type", "", "Filter by decision type (approval|review|...)")
	workflowsDecisionsCmd.Flags().StringVar(&decisionsRunID, "decision-run-id", "", "Filter by run ID")
	workflowsDecisionsCmd.Flags().IntVar(&decisionsLimit, "limit", 50, "Maximum decisions to return")

	workflowsReviewCmd.Flags().StringVar(&reviewRunID, "run-id", "", "Run ID for review action")
	workflowsReviewCmd.Flags().StringVar(&reviewAction, "action", "", "Review action: approve|request_changes|defer")
	workflowsReviewCmd.Flags().StringVar(&reviewDecisionID, "decision-id", "", "Optional decision ID")
	workflowsReviewCmd.Flags().StringVar(&reviewActor, "actor", "cli-operator", "Actor identity")
	workflowsReviewCmd.Flags().StringVar(&reviewNotes, "notes", "", "Optional review notes")
	workflowsReviewCmd.Flags().StringVar(&reviewClassification, "classification", "", "Optional decision classification")
	_ = workflowsReviewCmd.MarkFlagRequired("run-id")
	_ = workflowsReviewCmd.MarkFlagRequired("action")

	workflowsKPIsCmd.Flags().IntVar(&kpisLimit, "limit", 0, "Optional run sample limit for KPI computation")

	workflowsCmd.AddCommand(workflowsRPICmd)
	workflowsCmd.AddCommand(workflowsRunsCmd)
	workflowsCmd.AddCommand(workflowsDecisionsCmd)
	workflowsCmd.AddCommand(workflowsReviewCmd)
	workflowsCmd.AddCommand(workflowsKPIsCmd)
	rootCmd.AddCommand(workflowsCmd)
}

type rpiRequestOptions struct {
	Goal                   string
	Workspace              string
	AgentType              string
	UserID                 string
	ConversationID         string
	IdempotencyKey         string
	WorkflowProfile        string
	RequiredAWSProfile     string
	TargetEnvironment      string
	RequireTraceability    bool
	RequireApproval        bool
	ApprovalDecision       string
	RequireEvidence        bool
	EnforcePreflight       bool
	EnforceTerragruntReads bool
	PRDID                  string
	EpicID                 string
	UserStoryID            string
	SprintID               string
	TaskID                 string
	DDDRef                 string
	APISpecRef             string
	ImplementationPlanRef  string
}

type contractErrorEnvelope struct {
	OK    bool                 `json:"ok"`
	Error contractErrorPayload `json:"error"`
}

type contractErrorPayload struct {
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	Category   string         `json:"category"`
	HTTPStatus int            `json:"http_status"`
	RunID      string         `json:"run_id"`
	Retryable  bool           `json:"retryable"`
	Details    map[string]any `json:"details"`
}

type rpiResponse struct {
	Status       string         `json:"status"`
	WorkflowType string         `json:"workflow_type"`
	Result       rpiResult      `json:"result"`
	Evidence     map[string]any `json:"evidence"`
}

type rpiResult struct {
	ConversationID      string         `json:"conversation_id"`
	SessionID           string         `json:"session_id"`
	Status              string         `json:"status"`
	PreflightValidation map[string]any `json:"preflight_validation"`
	Traceability        map[string]any `json:"traceability"`
}

type runsResponse struct {
	Status string        `json:"status"`
	Count  int           `json:"count"`
	Runs   []workflowRun `json:"runs"`
}

type workflowRun struct {
	RunID         string         `json:"run_id"`
	Status        string         `json:"status"`
	Phase         string         `json:"phase"`
	AgentID       string         `json:"agent_id"`
	PRDID         string         `json:"prd_id"`
	EpicID        string         `json:"epic_id"`
	UserStoryID   string         `json:"user_story_id"`
	SprintID      string         `json:"sprint_id"`
	TaskID        string         `json:"task_id"`
	ErrorCode     string         `json:"error_code"`
	ErrorMessage  string         `json:"error_message"`
	PolicyResults map[string]any `json:"policy_results"`
	Evidence      map[string]any `json:"evidence"`
}

type runsFilterOptions struct {
	RunID    string
	Status   string
	Phase    string
	AgentID  string
	PRDID    string
	EpicID   string
	SprintID string
	TaskID   string
	Limit    int
}

type decisionsResponse struct {
	Status    string             `json:"status"`
	Count     int                `json:"count"`
	Decisions []workflowDecision `json:"decisions"`
}

type workflowDecision struct {
	DecisionID string         `json:"decision_id"`
	RunID      string         `json:"run_id"`
	Status     string         `json:"status"`
	Type       string         `json:"type"`
	Priority   string         `json:"priority"`
	ReasonCode string         `json:"reason_code"`
	Lineage    map[string]any `json:"lineage"`
}

type decisionFilterOptions struct {
	DecisionStatus string
	DecisionType   string
	DecisionRunID  string
	Limit          int
}

type reviewResponse struct {
	Status string       `json:"status"`
	Review reviewResult `json:"review"`
}

type reviewResult struct {
	RunID         string           `json:"run_id"`
	Status        string           `json:"status"`
	Phase         string           `json:"phase"`
	DecisionQueue []map[string]any `json:"decision_queue"`
	ReviewActions []map[string]any `json:"review_actions"`
}

type reviewRequestOptions struct {
	RunID          string
	Action         string
	DecisionID     string
	Actor          string
	Notes          string
	Classification string
}

type kpiResponse struct {
	Status   string      `json:"status"`
	Snapshot kpiSnapshot `json:"snapshot"`
}

type kpiSnapshot struct {
	GeneratedAt string               `json:"generated_at"`
	SampleSize  int                  `json:"sample_size"`
	KPIs        map[string]kpiMetric `json:"kpis"`
}

type kpiMetric struct {
	Numerator   int     `json:"numerator"`
	Denominator int     `json:"denominator"`
	Value       float64 `json:"value"`
	Target      float64 `json:"target"`
	PassesGate  bool    `json:"passes_gate"`
}

func runWorkflowRPI(ctx context.Context, out io.Writer) error {
	outputJSON, err := useJSONOutput(workflowsOutput)
	if err != nil {
		return err
	}

	conversationID, idempotencyKey := normalizeRunMetadata(rpiConversationID, rpiIdempotencyKey, generateRunID)
	opts := rpiRequestOptions{
		Goal:                   rpiGoal,
		Workspace:              rpiWorkspace,
		AgentType:              rpiAgentType,
		UserID:                 rpiUserID,
		ConversationID:         conversationID,
		IdempotencyKey:         idempotencyKey,
		WorkflowProfile:        rpiWorkflowProfile,
		RequiredAWSProfile:     rpiRequiredAWSProfile,
		TargetEnvironment:      rpiTargetEnvironment,
		RequireTraceability:    rpiRequireTraceability,
		RequireApproval:        rpiRequireApproval,
		ApprovalDecision:       rpiApprovalDecision,
		RequireEvidence:        rpiRequireEvidence,
		EnforcePreflight:       rpiEnforcePreflight,
		EnforceTerragruntReads: rpiEnforceTerragruntReads,
		PRDID:                  rpiPRDID,
		EpicID:                 rpiEpicID,
		UserStoryID:            rpiUserStoryID,
		SprintID:               rpiSprintID,
		TaskID:                 rpiTaskID,
		DDDRef:                 rpiDDDRef,
		APISpecRef:             rpiAPISpecRef,
		ImplementationPlanRef:  rpiImplementationPlanRef,
	}

	payload := buildRPIPayload(opts)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode request: %w", err)
	}

	endpoint, err := orchestratorEndpoint("/api/workflows/rpi")
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("X-Idempotency-Key", idempotencyKey)
	}
	setServiceToken(req.Header)

	status, respBody, err := doWorkflowRequest(req)
	if err != nil {
		return err
	}

	if status < 200 || status >= 300 {
		return formatContractHTTPError(status, respBody, conversationID)
	}

	var resp rpiResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("failed to decode success response: %w", err)
	}

	runID := firstNonEmpty(resp.Result.ConversationID, conversationID)
	verifyStatus, reviewStatus := evidenceStatuses(resp.Evidence)
	state := map[string]any{
		"traceability_required": opts.RequireTraceability,
		"approval_required":     opts.RequireApproval,
		"evidence_required":     opts.RequireEvidence,
		"preflight_enforced":    boolFromMap(resp.Result.PreflightValidation, "enforced?"),
		"aws_profile_matches":   boolFromMap(resp.Result.PreflightValidation, "aws_profile_matches?"),
		"verify_status":         verifyStatus,
		"review_status":         reviewStatus,
	}

	if outputJSON {
		outPayload := map[string]any{
			"status":        resp.Status,
			"workflow_type": resp.WorkflowType,
			"run_id":        runID,
			"session_id":    resp.Result.SessionID,
			"result_status": resp.Result.Status,
			"policy":        state,
			"traceability":  resp.Result.Traceability,
			"evidence":      resp.Evidence,
		}

		return writeJSON(out, outPayload)
	}

	// Structured text output
	statusColor := color.GreenString
	if resp.Status != "completed" {
		statusColor = color.YellowString
	}
	fmt.Fprintf(out, "%s %s\n", color.CyanString("Workflow:"), resp.WorkflowType)
	fmt.Fprintf(out, "%s %s\n", color.CyanString("Run ID: "), runID)
	fmt.Fprintf(out, "%s %s\n", color.CyanString("Status: "), statusColor(resp.Status))
	fmt.Fprintf(out, "%s %s (%s)\n\n", color.CyanString("Session:"), resp.Result.SessionID, resp.Result.Status)

	// Policy gates
	fmt.Fprintf(out, "%s\n", color.CyanString("Policy Gates:"))
	printGate(out, "Traceability required", opts.RequireTraceability)
	printGate(out, "Approval required", opts.RequireApproval)
	printGate(out, "Evidence required", opts.RequireEvidence)
	printGate(out, "Preflight enforced", boolFromMap(resp.Result.PreflightValidation, "enforced?"))
	printGate(out, "AWS profile matches", boolFromMap(resp.Result.PreflightValidation, "aws_profile_matches?"))
	fmt.Fprintln(out)

	// Evidence
	fmt.Fprintf(out, "%s\n", color.CyanString("Evidence:"))
	fmt.Fprintf(out, "  Verify: %s\n", verifyStatus)
	fmt.Fprintf(out, "  Review: %s\n", reviewStatus)

	return nil
}

func runWorkflowRuns(ctx context.Context, out io.Writer) error {
	outputJSON, err := useJSONOutput(workflowsOutput)
	if err != nil {
		return err
	}

	filters := runsFilterOptions{
		RunID:    runsRunID,
		Status:   runsStatus,
		Phase:    runsPhase,
		AgentID:  runsAgentID,
		PRDID:    runsPRDID,
		EpicID:   runsEpicID,
		SprintID: runsSprintID,
		TaskID:   runsTaskID,
		Limit:    runsLimit,
	}

	endpoint, err := orchestratorEndpoint("/api/workflows/runs")
	if err != nil {
		return err
	}

	query := buildRunsQuery(filters)
	if encoded := query.Encode(); encoded != "" {
		endpoint = endpoint + "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	setServiceToken(req.Header)

	status, respBody, err := doWorkflowRequest(req)
	if err != nil {
		return err
	}

	if status < 200 || status >= 300 {
		return formatContractHTTPError(status, respBody, runsRunID)
	}

	var resp runsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("failed to decode runs response: %w", err)
	}

	if outputJSON {
		return writeJSON(out, resp)
	}

	fmt.Fprintf(out, "%s %d runs\n\n", color.CyanString("Found"), resp.Count)

	if len(resp.Runs) == 0 {
		return nil
	}

	table := tablewriter.NewWriter(out)
	table.SetHeader([]string{"Run ID", "Status", "Phase", "Epic", "Task", "Error"})
	table.SetBorder(false)
	table.SetColumnSeparator("|")

	for _, run := range resp.Runs {
		runID := run.RunID
		if len(runID) > 8 {
			runID = runID[:8]
		}
		table.Append([]string{
			runID,
			run.Status,
			run.Phase,
			run.EpicID,
			run.TaskID,
			run.ErrorCode,
		})
	}

	table.Render()
	return nil
}

func runWorkflowDecisions(ctx context.Context, out io.Writer) error {
	outputJSON, err := useJSONOutput(workflowsOutput)
	if err != nil {
		return err
	}

	filters := decisionFilterOptions{
		DecisionStatus: decisionsStatus,
		DecisionType:   decisionsType,
		DecisionRunID:  decisionsRunID,
		Limit:          decisionsLimit,
	}

	endpoint, err := orchestratorEndpoint("/api/workflows/decisions")
	if err != nil {
		return err
	}

	query := buildDecisionsQuery(filters)
	if encoded := query.Encode(); encoded != "" {
		endpoint = endpoint + "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	setServiceToken(req.Header)

	status, respBody, err := doWorkflowRequest(req)
	if err != nil {
		return err
	}

	if status < 200 || status >= 300 {
		return formatContractHTTPError(status, respBody, decisionsRunID)
	}

	var resp decisionsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("failed to decode decisions response: %w", err)
	}

	if outputJSON {
		return writeJSON(out, resp)
	}

	fmt.Fprintf(out, "%s %d decisions\n\n", color.CyanString("Found"), resp.Count)

	if len(resp.Decisions) == 0 {
		return nil
	}

	table := tablewriter.NewWriter(out)
	table.SetHeader([]string{"Decision ID", "Run ID", "Status", "Type", "Priority", "Reason"})
	table.SetBorder(false)
	table.SetColumnSeparator("|")

	for _, decision := range resp.Decisions {
		decID := decision.DecisionID
		if len(decID) > 8 {
			decID = decID[:8]
		}
		runID := decision.RunID
		if len(runID) > 8 {
			runID = runID[:8]
		}
		table.Append([]string{
			decID,
			runID,
			decision.Status,
			decision.Type,
			decision.Priority,
			decision.ReasonCode,
		})
	}

	table.Render()
	return nil
}

func runWorkflowReview(ctx context.Context, out io.Writer) error {
	outputJSON, err := useJSONOutput(workflowsOutput)
	if err != nil {
		return err
	}

	if !isAllowedReviewAction(reviewAction) {
		return fmt.Errorf("invalid --action value %q (expected approve, request_changes, or defer)", reviewAction)
	}

	opts := reviewRequestOptions{
		RunID:          reviewRunID,
		Action:         reviewAction,
		DecisionID:     reviewDecisionID,
		Actor:          reviewActor,
		Notes:          reviewNotes,
		Classification: reviewClassification,
	}

	body, err := json.Marshal(buildReviewPayload(opts))
	if err != nil {
		return fmt.Errorf("failed to encode review request: %w", err)
	}

	endpoint, err := orchestratorEndpoint("/api/workflows/runs/" + url.PathEscape(strings.TrimSpace(opts.RunID)) + "/review")
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	setServiceToken(req.Header)

	status, respBody, err := doWorkflowRequest(req)
	if err != nil {
		return err
	}

	if status < 200 || status >= 300 {
		return formatContractHTTPError(status, respBody, opts.RunID)
	}

	var resp reviewResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("failed to decode review response: %w", err)
	}

	if outputJSON {
		return writeJSON(out, resp)
	}

	openCount := 0
	for _, decision := range resp.Review.DecisionQueue {
		if strings.EqualFold(asString(decision["status"]), "open") {
			openCount++
		}
	}

	fmt.Fprintf(out, "status=%s\n", resp.Status)
	fmt.Fprintf(out, "run_id=%s\n", firstNonEmpty(resp.Review.RunID, opts.RunID))
	fmt.Fprintf(out, "review_status=%s\n", resp.Review.Status)
	fmt.Fprintf(out, "phase=%s\n", resp.Review.Phase)
	fmt.Fprintf(out, "decision_queue_open=%d\n", openCount)
	fmt.Fprintf(out, "review_actions=%d\n", len(resp.Review.ReviewActions))

	return nil
}

func runWorkflowKPIs(ctx context.Context, out io.Writer) error {
	outputJSON, err := useJSONOutput(workflowsOutput)
	if err != nil {
		return err
	}

	endpoint, err := orchestratorEndpoint("/api/workflows/kpis")
	if err != nil {
		return err
	}

	query := url.Values{}
	if kpisLimit > 0 {
		query.Set("limit", strconv.Itoa(kpisLimit))
	}
	if encoded := query.Encode(); encoded != "" {
		endpoint = endpoint + "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	setServiceToken(req.Header)

	status, respBody, err := doWorkflowRequest(req)
	if err != nil {
		return err
	}

	if status < 200 || status >= 300 {
		return formatContractHTTPError(status, respBody, "")
	}

	var resp kpiResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("failed to decode kpi response: %w", err)
	}

	if outputJSON {
		return writeJSON(out, resp)
	}

	fmt.Fprintf(out, "%s KPI Snapshot — %s (sample: %d)\n\n",
		color.CyanString("Workflow"), resp.Snapshot.GeneratedAt, resp.Snapshot.SampleSize)

	metricOrder := []string{
		"first_pass_compliant_completion_rate",
		"decision_surface_precision",
		"policy_gated_run_coverage",
		"evidence_completeness",
		"end_to_end_git_test_flow_success",
	}

	table := tablewriter.NewWriter(out)
	table.SetHeader([]string{"Metric", "Value", "Target", "Gate", "N", "D"})
	table.SetBorder(false)
	table.SetColumnSeparator("|")

	for _, metricKey := range metricOrder {
		metric, ok := resp.Snapshot.KPIs[metricKey]
		if !ok {
			continue
		}

		gateStr := color.RedString("FAIL")
		if metric.PassesGate {
			gateStr = color.GreenString("PASS")
		}

		table.Append([]string{
			metricKey,
			fmt.Sprintf("%.4f", metric.Value),
			fmt.Sprintf("%.4f", metric.Target),
			gateStr,
			fmt.Sprintf("%d", metric.Numerator),
			fmt.Sprintf("%d", metric.Denominator),
		})
	}

	table.Render()
	return nil
}

func buildRPIPayload(opts rpiRequestOptions) map[string]any {
	payload := map[string]any{
		"goal":                      opts.Goal,
		"workspace":                 opts.Workspace,
		"agent_type":                opts.AgentType,
		"user_id":                   opts.UserID,
		"conversation_id":           opts.ConversationID,
		"session_key":               opts.IdempotencyKey,
		"enforce_preflight":         opts.EnforcePreflight,
		"enforce_terragrunt_reads":  opts.EnforceTerragruntReads,
		"require_traceability":      opts.RequireTraceability,
		"require_approval":          opts.RequireApproval,
		"require_evidence_contract": opts.RequireEvidence,
		"prd_id":                    opts.PRDID,
		"epic_id":                   opts.EpicID,
		"user_story_id":             opts.UserStoryID,
		"sprint_id":                 opts.SprintID,
		"task_id":                   opts.TaskID,
		"ddd_ref":                   opts.DDDRef,
		"api_spec_ref":              opts.APISpecRef,
		"implementation_plan_ref":   opts.ImplementationPlanRef,
	}

	if trimmed := strings.TrimSpace(opts.ApprovalDecision); trimmed != "" {
		payload["approval_decision"] = trimmed
	}
	if trimmed := strings.TrimSpace(opts.WorkflowProfile); trimmed != "" {
		payload["workflow_profile"] = trimmed
	}
	if trimmed := strings.TrimSpace(opts.RequiredAWSProfile); trimmed != "" {
		payload["required_aws_profile"] = trimmed
	}
	if trimmed := strings.TrimSpace(opts.TargetEnvironment); trimmed != "" {
		payload["target_environment"] = trimmed
	}

	return payload
}

func normalizeRunMetadata(conversationID, idempotencyKey string, runIDGenerator func() string) (string, string) {
	runID := strings.TrimSpace(conversationID)
	if runID == "" {
		runID = runIDGenerator()
	}

	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		key = "workflow:rpi:" + runID
	}

	return runID, key
}

func buildRunsQuery(filters runsFilterOptions) url.Values {
	query := url.Values{}
	setQuery(query, "run_id", filters.RunID)
	setQuery(query, "status", filters.Status)
	setQuery(query, "phase", filters.Phase)
	setQuery(query, "agent_id", filters.AgentID)
	setQuery(query, "prd_id", filters.PRDID)
	setQuery(query, "epic_id", filters.EpicID)
	setQuery(query, "sprint_id", filters.SprintID)
	setQuery(query, "task_id", filters.TaskID)

	if filters.Limit > 0 {
		query.Set("limit", strconv.Itoa(filters.Limit))
	}

	return query
}

func buildDecisionsQuery(filters decisionFilterOptions) url.Values {
	query := url.Values{}
	setQuery(query, "decision_status", filters.DecisionStatus)
	setQuery(query, "decision_type", filters.DecisionType)
	setQuery(query, "decision_run_id", filters.DecisionRunID)

	if filters.Limit > 0 {
		query.Set("limit", strconv.Itoa(filters.Limit))
	}

	return query
}

func buildReviewPayload(opts reviewRequestOptions) map[string]any {
	payload := map[string]any{
		"action": opts.Action,
	}

	if trimmed := strings.TrimSpace(opts.DecisionID); trimmed != "" {
		payload["decision_id"] = trimmed
	}
	if trimmed := strings.TrimSpace(opts.Actor); trimmed != "" {
		payload["actor"] = trimmed
	}
	if trimmed := strings.TrimSpace(opts.Notes); trimmed != "" {
		payload["notes"] = trimmed
	}
	if trimmed := strings.TrimSpace(opts.Classification); trimmed != "" {
		payload["classification"] = trimmed
	}

	return payload
}

func isAllowedReviewAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "approve", "request_changes", "defer":
		return true
	default:
		return false
	}
}

func setQuery(query url.Values, key, value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		query.Set(key, trimmed)
	}
}

func useJSONOutput(format string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text":
		return false, nil
	case "json":
		return true, nil
	default:
		return false, fmt.Errorf("invalid --output value %q (expected text or json)", format)
	}
}

func orchestratorEndpoint(path string) (string, error) {
	base := resolveOrchestratorBaseURL()
	if base == "" {
		return "", fmt.Errorf("orchestrator base URL is not configured (set --orchestrator-url or LW_ORCHESTRATOR_URL)")
	}

	endpoint, err := url.JoinPath(base, path)
	if err != nil {
		return "", fmt.Errorf("failed to build orchestrator endpoint: %w", err)
	}

	return endpoint, nil
}

func resolveOrchestratorBaseURL() string {
	if value := strings.TrimSpace(workflowsBaseURL); value != "" {
		return strings.TrimRight(value, "/")
	}

	if value := strings.TrimSpace(os.Getenv("LW_ORCHESTRATOR_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}

	cfg := config.Get()
	if cfg == nil {
		return ""
	}

	return strings.TrimRight(cfg.GetOrchestratorURL(), "/")
}

func deriveBaseURL(apiURL string) string {
	trimmed := strings.TrimSpace(apiURL)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return strings.TrimRight(trimmed, "/")
	}

	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/api/createos")
	path = strings.TrimSuffix(path, "/api")
	parsed.Path = path
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return strings.TrimRight(parsed.String(), "/")
}

func setServiceToken(headers http.Header) {
	token := strings.TrimSpace(workflowsServiceToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("LW_SERVICE_TOKEN"))
	}
	if token == "" {
		token = strings.TrimSpace(config.GetAgentKey())
	}
	if token != "" {
		headers.Set("X-Service-Token", token)
	}
}

func doWorkflowRequest(req *http.Request) (int, []byte, error) {
	client := &http.Client{Timeout: workflowHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read response: %w", err)
	}

	return resp.StatusCode, body, nil
}

func formatContractHTTPError(status int, body []byte, fallbackRunID string) error {
	authHint := ""
	if status == 401 {
		authHint = "\n\nSet LW_SERVICE_TOKEN or LW_AGENT_KEY to authenticate with the Elixir orchestrator."
	}

	var envelope contractErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Code != "" {
		runID := firstNonEmpty(envelope.Error.RunID, fallbackRunID)
		details := compactDetails(envelope.Error.Details)
		return fmt.Errorf(
			"code=%s message=%q run_id=%s details=%s%s",
			envelope.Error.Code,
			envelope.Error.Message,
			runID,
			details,
			authHint,
		)
	}

	message := extractErrorMessage(body)
	if message == "" {
		message = http.StatusText(status)
	}
	runID := strings.TrimSpace(fallbackRunID)
	if runID == "" {
		runID = "unknown"
	}

	return fmt.Errorf("status=%d message=%q run_id=%s%s", status, message, runID, authHint)
}

func extractErrorMessage(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err == nil {
		if msg := asString(payload["message"]); msg != "" {
			return msg
		}
		if msg := asString(payload["error"]); msg != "" {
			return msg
		}
	}

	if len(trimmed) > 280 {
		return trimmed[:277] + "..."
	}
	return trimmed
}

func compactDetails(details map[string]any) string {
	if len(details) == 0 {
		return "{}"
	}

	encoded, err := json.Marshal(details)
	if err != nil {
		return "{}"
	}
	if len(encoded) > 400 {
		return string(encoded[:397]) + "..."
	}
	return string(encoded)
}

func writeJSON(out io.Writer, payload any) error {
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode output: %w", err)
	}

	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

func printGate(out io.Writer, name string, ok bool) {
	icon := color.GreenString("✓")
	if !ok {
		icon = color.RedString("✗")
	}
	fmt.Fprintf(out, "  %s %s\n", icon, name)
}

func boolFromMap(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	value, ok := m[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func evidenceStatuses(evidence map[string]any) (string, string) {
	if evidence == nil {
		return "", ""
	}

	verify := ""
	if raw, ok := evidence["verify"]; ok {
		if typed, ok := raw.(map[string]any); ok {
			verify = asString(typed["status"])
		}
	}

	review := ""
	if raw, ok := evidence["review"]; ok {
		if typed, ok := raw.(map[string]any); ok {
			review = asString(typed["status"])
		}
	}

	return verify, review
}

func asString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func generateRunID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}

	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80

	hexValue := hex.EncodeToString(bytes[:])
	return fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		hexValue[0:8],
		hexValue[8:12],
		hexValue[12:16],
		hexValue[16:20],
		hexValue[20:32],
	)
}
