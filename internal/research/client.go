// Package research is a thin client over the Perplexity API, exposing a
// single research primitive used by `lw research`.
//
// MVP scope: one synchronous "ask → cited answer" call. The longer-term
// north-star is Search-as-Code (agents generating code to orchestrate
// low-level search primitives) — see docs/research-as-code.md. The package
// is deliberately small so it can grow toward that without a rewrite.
package research

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.perplexity.ai"

// Model identifiers accepted by the Perplexity API.
const (
	// ModelSonarPro is the fast, synchronous default.
	ModelSonarPro = "sonar-pro"
	// ModelDeepResearch is the slower, multi-step research model with the
	// richest citations — selected by `lw research --deep`.
	ModelDeepResearch = "sonar-deep-research"
)

const (
	// defaultTimeout is generous because sonar-deep-research can run for minutes.
	defaultTimeout = 5 * time.Minute
	// initialMessageCap pre-sizes the system+user message slice.
	initialMessageCap = 2
	// maxResponseBytes caps how much of the response body we read.
	maxResponseBytes = 16 * 1024 * 1024
)

// Client talks to the Perplexity chat-completions endpoint.
type Client struct {
	http    *http.Client
	apiKey  string
	baseURL string
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL (used in tests).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient overrides the HTTP client (used in tests / for custom timeouts).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// NewClient returns a Client. The default timeout (5m) is generous because
// sonar-deep-research can run for minutes.
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: defaultTimeout},
	}
	for _, o := range opts {
		o(c)
	}

	return c
}

// Request is a single research query.
type Request struct {
	Query   string   // required
	Model   string   // optional; defaults to ModelSonarPro
	System  string   // optional system prompt to steer the research
	Recency string   // optional: hour|day|week|month
	Domains []string // optional search_domain_filter (max 10; prefix "-" to exclude)
}

// Result is the parsed answer plus its sources and token usage.
type Result struct {
	Answer    string   `json:"answer"`
	Model     string   `json:"model"`
	Citations []string `json:"citations"`
	Usage     Usage    `json:"usage"`
}

// Usage mirrors the API's token accounting.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- wire types (Perplexity is OpenAI-compatible) ---

type chatRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	SearchRecencyFilter string        `json:"search_recency_filter,omitempty"`
	SearchDomainFilter  []string      `json:"search_domain_filter,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Model     string   `json:"model"`
	Citations []string `json:"citations"`
	Choices   []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

// Research runs one query and returns the cited answer.
func (c *Client) Research(ctx context.Context, req *Request) (*Result, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, errors.New("research: empty query")
	}

	if c.apiKey == "" {
		return nil, errors.New("research: missing API key")
	}

	model := req.Model
	if model == "" {
		model = ModelSonarPro
	}

	msgs := make([]chatMessage, 0, initialMessageCap)
	if req.System != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: req.System})
	}

	msgs = append(msgs, chatMessage{Role: "user", Content: req.Query})

	payload, err := json.Marshal(chatRequest{
		Model:               model,
		Messages:            msgs,
		SearchRecencyFilter: req.Recency,
		SearchDomainFilter:  req.Domains,
	})
	if err != nil {
		return nil, fmt.Errorf("research: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("research: POST %s: %w", c.baseURL, err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("research: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("research: Perplexity returned HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("research: decode response: %w", err)
	}

	if len(cr.Choices) == 0 {
		return nil, errors.New("research: Perplexity returned no choices")
	}

	return &Result{
		Answer:    cr.Choices[0].Message.Content,
		Citations: cr.Citations,
		Model:     cr.Model,
		Usage:     cr.Usage,
	}, nil
}
