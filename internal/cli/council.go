package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// =============================================================================
// Augusta base URL
// =============================================================================

func augustaBaseURL() string {
	if u := os.Getenv("LW_AUGUSTA_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:9700"
}

// =============================================================================
// Persona gate
// =============================================================================

var councilAllowedPersonas = map[string]bool{
	"v_core":       true,
	"council":      true,
	"orchestrator": true,
}

func checkCouncilPersona() error {
	p := os.Getenv("LW_PERSONA")
	if p == "" {
		return fmt.Errorf("LW_PERSONA is not set; council requires v_core, council, or orchestrator persona")
	}
	if !councilAllowedPersonas[p] {
		return fmt.Errorf("LW_PERSONA=%q is not authorized for council; must be v_core, council, or orchestrator", p)
	}
	return nil
}

// =============================================================================
// Command tree
// =============================================================================

var councilCmd = &cobra.Command{
	Use:   "council",
	Short: "Execute multi-model council deliberations via Augusta",
	Long: `Call Augusta's council HTTP API (localhost:9700) to run structured
multi-model deliberations.

Personas: requires LW_PERSONA=v_core, council, or orchestrator.

Subcommands:
  start     Begin a deliberation with streaming progress
  status    Check status of a running deliberation
  result    Fetch the final result
  history   List past deliberations
  cancel    Cancel a running deliberation
  config    Print the council configuration`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return checkCouncilPersona()
	},
}

// =============================================================================
// lw council start
// =============================================================================

var (
	councilStartCTO      bool
	councilStartCEO      bool
	councilStartCFO      bool
	councilStartAllRoles bool
	councilStartModels   string
	councilStartChairman string
	councilStartJSON     bool
)

var councilStartCmd = &cobra.Command{
	Use:   "start [question]",
	Short: "Start a council deliberation with streaming progress",
	Long: `POST to Augusta's /council/start and stream progress via SSE.

Roles are selected with --cto, --ceo, --cfo, or --all (default: all three).
The question can be passed as a positional argument or via --question.

Examples:
  lw council start "Which database should we use?"
  lw council start --cto --ceo --question "Evaluate our auth architecture"
  lw council start --all --models "openai/gpt-4o,anthropic/claude-sonnet-4-6"
  lw council start --cto --chairman "anthropic/claude-opus-4-6" "Review the deployment plan"`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		question := councilQuestion(cmd, args)
		if question == "" {
			return fmt.Errorf("question is required (use --question or positional argument)")
		}

		roles := councilRoles()
		if len(roles) == 0 {
			return fmt.Errorf("at least one role is required")
		}

		return runCouncilStart(question, roles)
	},
}

func councilQuestion(cmd *cobra.Command, args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return cmd.Flag("question").Value.String()
}

func councilRoles() []string {
	var roles []string
	if councilStartAllRoles {
		return []string{"cto", "ceo", "cfo"}
	}
	if councilStartCTO {
		roles = append(roles, "cto")
	}
	if councilStartCEO {
		roles = append(roles, "ceo")
	}
	if councilStartCFO {
		roles = append(roles, "cfo")
	}
	if len(roles) == 0 {
		// Default: all three
		return []string{"cto", "ceo", "cfo"}
	}
	return roles
}

// =============================================================================
// API types
// =============================================================================

type councilStartRequest struct {
	Question string   `json:"question"`
	Roles    []string `json:"roles"`
	Caller   string   `json:"caller"`
	Models   []string `json:"models,omitempty"`
	Chairman string   `json:"chairman,omitempty"`
}

type councilStartResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type councilSSEEvent struct {
	Event string          `json:"event"`
	Stage string          `json:"stage,omitempty"`
	Data  json.RawMessage `json:"data"`
}

type councilStageProgress struct {
	Index  int    `json:"index"`
	Total  int    `json:"total"`
	Role   string `json:"role"`
	Model  string `json:"model"`
	Status string `json:"status"` // "running", "succeeded", "failed"
	Error  string `json:"error,omitempty"`
}

type councilResult struct {
	Stage1   []councilStage1Result  `json:"stage1"`
	Stage2   []councilStage2Result  `json:"stage2"`
	Stage3   *councilStage3Result   `json:"stage3"`
	Metadata *councilResultMetadata `json:"metadata"`
	Cost     *councilCost           `json:"cost"`
}

type councilStage1Result struct {
	Role    string `json:"role"`
	Model   string `json:"model"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

type councilStage2Result struct {
	Model    string               `json:"model"`
	Rankings []councilRankingItem `json:"rankings"`
}

type councilRankingItem struct {
	Role  string  `json:"role"`
	Rank  int     `json:"rank"`
	Score float64 `json:"score"`
}

type councilStage3Result struct {
	Synthesis string `json:"synthesis"`
	Model     string `json:"model"`
}

type councilResultMetadata struct {
	Question  string   `json:"question"`
	Roles     []string `json:"roles"`
	StartedAt string   `json:"started_at"`
	EndedAt   string   `json:"ended_at"`
}

type councilCost struct {
	Total float64 `json:"total"`
}

type councilHistoryItem struct {
	ID        string       `json:"id"`
	Question  string       `json:"question"`
	Roles     []string     `json:"roles"`
	Status    string       `json:"status"`
	StartedAt string       `json:"started_at"`
	Cost      *councilCost `json:"cost,omitempty"`
}

type councilStatusResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Stage  string `json:"stage,omitempty"`
}

// =============================================================================
// HTTP helpers
// =============================================================================

var councilHTTPClient = &http.Client{Timeout: 10 * time.Second}

func councilPOST(path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := augustaBaseURL() + path
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return councilHTTPClient.Do(req)
}

func councilGET(path string) (*http.Response, error) {
	url := augustaBaseURL() + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	return councilHTTPClient.Do(req)
}

func decodeJSON(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

// =============================================================================
// lw council start — implementation
// =============================================================================

func runCouncilStart(question string, roles []string) error {
	baseURL := augustaBaseURL()

	// Build request
	req := councilStartRequest{
		Question: question,
		Roles:    roles,
		Caller:   "cli",
	}
	if councilStartModels != "" {
		req.Models = strings.Split(councilStartModels, ",")
		for i := range req.Models {
			req.Models[i] = strings.TrimSpace(req.Models[i])
		}
	}
	if councilStartChairman != "" {
		req.Chairman = councilStartChairman
	}

	// POST /council/start
	resp, err := councilPOST("/council/start", req)
	if err != nil {
		return fmt.Errorf("connect to Augusta at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("Augusta returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var startResp councilStartResponse
	if err := decodeJSON(resp.Body, &startResp); err != nil {
		return fmt.Errorf("parse start response: %w", err)
	}
	if startResp.Error != "" {
		return fmt.Errorf("Augusta error: %s", startResp.Error)
	}

	id := startResp.ID
	fmt.Printf("Council started: %s\n", color.CyanString(id))
	fmt.Printf("Question: %s\n", color.YellowString(question))
	fmt.Printf("Roles: %s\n\n", strings.Join(roles, ", "))

	// Open SSE stream
	if err := streamCouncilProgress(id); err != nil {
		return err
	}

	// Fetch and display result
	return fetchAndDisplayResult(id)
}

// streamCouncilProgress connects to the SSE endpoint and renders progress.
func streamCouncilProgress(id string) error {
	url := augustaBaseURL() + "/council/" + id + "/status"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	// Use a long timeout for SSE streaming
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connect to SSE stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("SSE stream returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(resp.Body)
	// SSE lines can be long (up to 1MB for large events)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentStage string
	var stageLabel string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[keepalive]" || data == "" {
			continue
		}

		var event councilSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			// Non-JSON event — print raw
			fmt.Println(color.HiBlackString("  %s", data))
			continue
		}

		switch event.Event {
		case "stage1_start":
			currentStage = "stage1"
			stageLabel = "Stage 1"
			fmt.Printf("[%s] Collecting responses...\n", color.CyanString(stageLabel))

		case "stage1_progress":
			var prog councilStageProgress
			json.Unmarshal(event.Data, &prog)
			statusIcon := "✓"
			statusColor := color.GreenString
			if prog.Status == "failed" {
				statusIcon = "✗"
				statusColor = color.RedString
			} else if prog.Status == "running" {
				statusIcon = "⏳"
				statusColor = color.YellowString
			}
			fmt.Printf("    [%d/%d] %s via %s %s\n",
				prog.Index, prog.Total,
				color.CyanString(prog.Role),
				color.HiBlackString(prog.Model),
				statusColor(statusIcon))
			if prog.Error != "" {
				fmt.Printf("           %s\n", color.RedString("Error: "+prog.Error))
			}

		case "stage1_complete":
			var summary struct {
				Succeeded int `json:"succeeded"`
				Failed    int `json:"failed"`
			}
			json.Unmarshal(event.Data, &summary)
			fmt.Printf("[%s] Complete: %s succeeded, %s failed\n",
				color.CyanString(stageLabel),
				color.GreenString("%d", summary.Succeeded),
				color.RedString("%d", summary.Failed))

		case "stage2_start":
			currentStage = "stage2"
			stageLabel = "Stage 2"
			fmt.Printf("[%s] Cross-referencing with models...\n", color.CyanString(stageLabel))

		case "stage2_progress":
			var prog struct {
				Index  int    `json:"index"`
				Total  int    `json:"total"`
				Model  string `json:"model"`
				Status string `json:"status"`
			}
			json.Unmarshal(event.Data, &prog)
			statusIcon := "✓"
			if prog.Status == "running" {
				statusIcon = "⏳"
			} else if prog.Status == "failed" {
				statusIcon = "✗"
			}
			fmt.Printf("    [%d/%d] %s ranked %s\n",
				prog.Index, prog.Total,
				color.HiBlackString(prog.Model),
				statusIcon)

		case "stage2_complete":
			fmt.Printf("[%s] Cross-referencing complete\n", color.CyanString(stageLabel))

		case "stage3_start":
			currentStage = "stage3"
			stageLabel = "Stage 3"
			fmt.Printf("[%s] Chairman synthesizing...\n", color.CyanString(stageLabel))

		case "stage3_complete":
			fmt.Printf("[%s] Synthesis complete\n", color.CyanString(stageLabel))

		case "complete":
			fmt.Println()

		case "error":
			var errPayload struct {
				Message string `json:"message"`
			}
			json.Unmarshal(event.Data, &errPayload)
			return fmt.Errorf("council error: %s", errPayload.Message)

		default:
			// Unknown event — print stage context if known
			if currentStage != "" {
				fmt.Printf("[%s] %s\n", color.CyanString(stageLabel), event.Event)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("SSE stream error: %w", err)
	}
	return nil
}

// fetchAndDisplayResult fetches the final result and prints it.
func fetchAndDisplayResult(id string) error {
	resp, err := councilGET("/council/" + id + "/result")
	if err != nil {
		return fmt.Errorf("fetch result: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		fmt.Println(color.YellowString("Deliberation still running. Use 'lw council status %s' to check.", id))
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("fetch result: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result councilResult
	if err := decodeJSON(resp.Body, &result); err != nil {
		return fmt.Errorf("parse result: %w", err)
	}

	if councilStartJSON {
		return emitJSON(result)
	}

	return printCouncilResult(&result)
}

func printCouncilResult(r *councilResult) error {
	// Cost line
	if r.Cost != nil {
		costStr := fmt.Sprintf("$%.4f", r.Cost.Total)
		fmt.Printf("✅ Council complete (cost: %s)\n\n", color.GreenString(costStr))
	} else {
		fmt.Println(color.GreenString("✅ Council complete\n"))
	}

	// Stage 1 — responses
	if len(r.Stage1) > 0 {
		fmt.Println(color.CyanString("── Stage 1: Individual Responses ──"))
		for _, s := range r.Stage1 {
			fmt.Printf("  %s via %s\n", color.CyanString(s.Role), color.HiBlackString(s.Model))
			if s.Error != "" {
				fmt.Printf("    %s: %s\n", color.RedString("Error"), s.Error)
			} else {
				// Truncate long responses for display
				content := s.Content
				if len(content) > 500 {
					content = content[:500] + "..."
				}
				for _, line := range strings.Split(content, "\n") {
					if line != "" {
						fmt.Printf("    %s\n", line)
					}
				}
			}
			fmt.Println()
		}
	}

	// Stage 2 — aggregate rankings
	if len(r.Stage2) > 0 {
		fmt.Println(color.CyanString("── Stage 2: Aggregate Rankings ──"))
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Role", "Model", "Rank", "Score"})
		table.SetBorder(false)
		table.SetColumnSeparator("  ")
		table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
		table.SetAlignment(tablewriter.ALIGN_LEFT)

		for _, s2 := range r.Stage2 {
			for _, rank := range s2.Rankings {
				table.Append([]string{
					rank.Role,
					s2.Model,
					fmt.Sprintf("%d", rank.Rank),
					fmt.Sprintf("%.2f", rank.Score),
				})
			}
		}
		table.Render()
		fmt.Println()
	}

	// Stage 3 — chairman synthesis
	if r.Stage3 != nil {
		fmt.Println(color.CyanString("── Stage 3: Chairman Synthesis ──"))
		if r.Stage3.Model != "" {
			fmt.Printf("  Chairman: %s\n", color.HiBlackString(r.Stage3.Model))
		}
		fmt.Println()
		for _, line := range strings.Split(r.Stage3.Synthesis, "\n") {
			fmt.Printf("  %s\n", line)
		}
		fmt.Println()
	}

	return nil
}

// =============================================================================
// lw council status <id>
// =============================================================================

var councilStatusJSON bool

var councilStatusCmd = &cobra.Command{
	Use:   "status <id>",
	Short: "Check the status of a deliberation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		resp, err := councilGET("/council/" + id + "/status?format=json")
		if err != nil {
			return fmt.Errorf("fetch status: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		// The status endpoint normally returns SSE; with ?format=json we get JSON
		// If it still returns SSE, read the body as raw JSON
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		if err != nil {
			return fmt.Errorf("read status response: %w", err)
		}

		// Pretty-print whatever JSON we got
		if councilStatusJSON {
			var raw any
			if err := json.Unmarshal(body, &raw); err != nil {
				// Not JSON — print raw
				fmt.Println(string(body))
				return nil
			}
			return emitJSON(raw)
		}

		// Try to parse as structured status
		var status councilStatusResponse
		if err := json.Unmarshal(body, &status); err == nil && status.ID != "" {
			fmt.Printf("ID:     %s\n", color.CyanString(status.ID))
			fmt.Printf("Status: %s\n", councilStatusColor(status.Status))
			if status.Stage != "" {
				fmt.Printf("Stage:  %s\n", status.Stage)
			}
		} else {
			// Raw print
			fmt.Println(string(body))
		}
		return nil
	},
}

func councilStatusColor(status string) string {
	switch status {
	case "running":
		return color.YellowString(status)
	case "complete":
		return color.GreenString(status)
	case "failed", "cancelled":
		return color.RedString(status)
	default:
		return status
	}
}

// =============================================================================
// lw council result <id>
// =============================================================================

var councilResultJSON bool

var councilResultCmd = &cobra.Command{
	Use:   "result <id>",
	Short: "Fetch the full result of a deliberation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		resp, err := councilGET("/council/" + id + "/result")
		if err != nil {
			return fmt.Errorf("fetch result: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusAccepted {
			fmt.Println(color.YellowString("Deliberation still running. Use 'lw council status %s' to check.", id))
			return nil
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		if err != nil {
			return fmt.Errorf("read result: %w", err)
		}

		if councilResultJSON {
			// Pretty-print the raw JSON
			var raw any
			json.Unmarshal(body, &raw)
			return emitJSON(raw)
		}

		var result councilResult
		if err := json.Unmarshal(body, &result); err != nil {
			return fmt.Errorf("parse result: %w", err)
		}
		return printCouncilResult(&result)
	},
}

// =============================================================================
// lw council history
// =============================================================================

var (
	councilHistoryLimit int
	councilHistoryRole  string
	councilHistoryJSON  bool
)

var councilHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "List past council deliberations",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := fmt.Sprintf("/council/history?limit=%d", councilHistoryLimit)
		if councilHistoryRole != "" {
			path += "&role=" + councilHistoryRole
		}

		resp, err := councilGET(path)
		if err != nil {
			return fmt.Errorf("fetch history: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var history []councilHistoryItem
		if err := decodeJSON(resp.Body, &history); err != nil {
			return fmt.Errorf("parse history: %w", err)
		}

		if councilHistoryJSON {
			return emitJSON(history)
		}

		if len(history) == 0 {
			fmt.Println(color.YellowString("No past deliberations found"))
			return nil
		}

		fmt.Printf("Past deliberations (%d):\n\n", len(history))
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Question", "Roles", "Status", "Cost"})
		table.SetBorder(false)
		table.SetColumnSeparator("  ")
		table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
		table.SetAlignment(tablewriter.ALIGN_LEFT)

		for _, h := range history {
			cost := "-"
			if h.Cost != nil {
				cost = fmt.Sprintf("$%.4f", h.Cost.Total)
			}
			question := h.Question
			if len(question) > 60 {
				question = question[:57] + "..."
			}
			table.Append([]string{
				h.ID,
				question,
				strings.Join(h.Roles, ", "),
				councilStatusColor(h.Status),
				cost,
			})
		}
		table.Render()
		fmt.Println()
		return nil
	},
}

// =============================================================================
// lw council cancel <id>
// =============================================================================

var councilCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel a running deliberation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		resp, err := councilPOST("/council/"+id+"/cancel", nil)
		if err != nil {
			return fmt.Errorf("cancel: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		fmt.Printf("Council %s %s\n", color.CyanString(id), color.YellowString("cancelled"))
		return nil
	},
}

// =============================================================================
// lw council config
// =============================================================================

var councilConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Print the council configuration",
	Long: `Read and display the council configuration from ~/.brain/cortex/engineering/council.yaml.

Requires LW_PERSONA=v_core, council, or orchestrator.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %w", err)
		}

		configPath := filepath.Join(home, ".brain", "cortex", "engineering", "council.yaml")
		data, err := os.ReadFile(configPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("council config not found at %s", configPath)
			}
			return fmt.Errorf("read council config: %w", err)
		}

		fmt.Printf("Council config: %s\n\n", color.CyanString(configPath))

		// Pretty-print YAML by parsing and re-marshalling
		var raw any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			// If it doesn't parse as YAML, just dump it
			fmt.Println(string(data))
			return nil
		}

		out, err := yaml.Marshal(raw)
		if err != nil {
			fmt.Println(string(data))
			return nil
		}
		fmt.Println(string(out))
		return nil
	},
}

// =============================================================================
// Registration
// =============================================================================

func init() {
	// council start flags
	councilStartCmd.Flags().BoolVar(&councilStartCTO, "cto", false, "Include CTO role")
	councilStartCmd.Flags().BoolVar(&councilStartCEO, "ceo", false, "Include CEO role")
	councilStartCmd.Flags().BoolVar(&councilStartCFO, "cfo", false, "Include CFO role")
	councilStartCmd.Flags().BoolVar(&councilStartAllRoles, "all", false, "All three roles")
	councilStartCmd.Flags().StringVar(&councilStartModels, "models", "", "Comma-separated model overrides")
	councilStartCmd.Flags().StringVar(&councilStartChairman, "chairman", "", "Chairman model override")
	councilStartCmd.Flags().BoolVar(&councilStartJSON, "json", false, "Output result as JSON")
	_ = councilStartCmd.Flags().String("question", "", "The deliberation question")

	// council status flags
	councilStatusCmd.Flags().BoolVar(&councilStatusJSON, "json", false, "Output as JSON")

	// council result flags
	councilResultCmd.Flags().BoolVar(&councilResultJSON, "json", false, "Output as JSON")

	// council history flags
	councilHistoryCmd.Flags().IntVar(&councilHistoryLimit, "limit", 20, "Maximum items to return")
	councilHistoryCmd.Flags().StringVar(&councilHistoryRole, "role", "", "Filter by role")
	councilHistoryCmd.Flags().BoolVar(&councilHistoryJSON, "json", false, "Output as JSON")

	// Assemble tree
	councilCmd.AddCommand(councilStartCmd)
	councilCmd.AddCommand(councilStatusCmd)
	councilCmd.AddCommand(councilResultCmd)
	councilCmd.AddCommand(councilHistoryCmd)
	councilCmd.AddCommand(councilCancelCmd)
	councilCmd.AddCommand(councilConfigCmd)
}
