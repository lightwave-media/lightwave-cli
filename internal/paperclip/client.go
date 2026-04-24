package paperclip

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lightwave-media/lightwave-cli/internal/config"
)

// Client wraps the Paperclip REST API.
// Paperclip calls work items "issues" — the CLI exposes them as "tasks" for Joel's mental model.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Company represents a Paperclip company (e.g. lightwave-engineering, lightwave-operations).
type Company struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Agent represents a Paperclip agent within a company.
type Agent struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Title              string    `json:"title,omitempty"`
	Status             string    `json:"status"`
	CompanyID          string    `json:"companyId"`
	CompanyName        string    `json:"-"` // populated by client after join
	SpentMonthlyCents  int       `json:"spentMonthlyCents"`
	BudgetMonthlyCents int       `json:"budgetMonthlyCents"`
	LastHeartbeatAt    time.Time `json:"lastHeartbeatAt"`
}

// Issue represents a Paperclip issue (work item). Called "task" in the CLI.
type Issue struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Description     string    `json:"description,omitempty"`
	Status          string    `json:"status"`
	AssigneeAgentID string    `json:"assigneeAgentId,omitempty"`
	CompanyID       string    `json:"companyId"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// Activity represents a Paperclip audit trail entry.
type Activity struct {
	ID         string    `json:"id"`
	AgentID    string    `json:"agentId,omitempty"`
	AgentName  string    `json:"agentName,omitempty"`
	EntityType string    `json:"entityType,omitempty"`
	EntityID   string    `json:"entityId,omitempty"`
	Action     string    `json:"action"`
	Detail     string    `json:"detail,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ActivityFilter controls which activity entries are returned.
type ActivityFilter struct {
	AgentID    string
	EntityType string
	Limit      int
}

// PauseResumeResponse is the API response for agent pause/resume operations.
type PauseResumeResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// NewClient creates a Paperclip API client using the configured URL.
func NewClient() *Client {
	baseURL := "http://localhost:3100"
	cfg := config.Get()
	if cfg != nil {
		u := cfg.GetPaperclipURL()
		if u != "" {
			baseURL = strings.TrimRight(u, "/")
		}
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// ListCompanies returns all Paperclip companies.
func (c *Client) ListCompanies(ctx context.Context) ([]Company, error) {
	var companies []Company
	if err := c.get(ctx, "/api/companies", &companies); err != nil {
		return nil, fmt.Errorf("list companies: %w", err)
	}
	return companies, nil
}

// ListAgents returns agents for a company.
func (c *Client) ListAgents(ctx context.Context, companyID string) ([]Agent, error) {
	path := fmt.Sprintf("/api/companies/%s/agents", url.PathEscape(companyID))
	var agents []Agent
	if err := c.get(ctx, path, &agents); err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	return agents, nil
}

// ListAllAgents returns agents across all companies, with CompanyName populated.
func (c *Client) ListAllAgents(ctx context.Context) ([]Agent, error) {
	companies, err := c.ListCompanies(ctx)
	if err != nil {
		return nil, err
	}
	var all []Agent
	for _, co := range companies {
		agents, err := c.ListAgents(ctx, co.ID)
		if err != nil {
			return nil, fmt.Errorf("list agents for %s: %w", co.Name, err)
		}
		for i := range agents {
			agents[i].CompanyName = co.Name
		}
		all = append(all, agents...)
	}
	return all, nil
}

// ListIssues returns issues for a company. Paperclip "issues" = CLI "tasks".
func (c *Client) ListIssues(ctx context.Context, companyID string) ([]Issue, error) {
	path := fmt.Sprintf("/api/companies/%s/issues", url.PathEscape(companyID))
	var issues []Issue
	if err := c.get(ctx, path, &issues); err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	return issues, nil
}

// CreateIssue creates a new issue assigned to an agent. Paperclip "issues" = CLI "tasks".
func (c *Client) CreateIssue(ctx context.Context, companyID string, issue Issue) (*Issue, error) {
	path := fmt.Sprintf("/api/companies/%s/issues", url.PathEscape(companyID))
	var created Issue
	if err := c.post(ctx, path, issue, &created); err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}
	return &created, nil
}

// GetIssue returns a single issue by ID.
func (c *Client) GetIssue(ctx context.Context, companyID, issueID string) (*Issue, error) {
	path := fmt.Sprintf("/api/companies/%s/issues/%s", url.PathEscape(companyID), url.PathEscape(issueID))
	var issue Issue
	if err := c.get(ctx, path, &issue); err != nil {
		return nil, fmt.Errorf("get issue: %w", err)
	}
	return &issue, nil
}

// GetAgent returns a single agent by ID.
func (c *Client) GetAgent(ctx context.Context, agentID string) (*Agent, error) {
	path := fmt.Sprintf("/api/agents/%s", url.PathEscape(agentID))
	var agent Agent
	if err := c.get(ctx, path, &agent); err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	return &agent, nil
}

// PauseAgent pauses an agent's heartbeat.
func (c *Client) PauseAgent(ctx context.Context, agentID string) (*PauseResumeResponse, error) {
	path := fmt.Sprintf("/api/agents/%s/pause", url.PathEscape(agentID))
	var resp PauseResumeResponse
	if err := c.post(ctx, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("pause agent: %w", err)
	}
	return &resp, nil
}

// ResumeAgent resumes an agent's heartbeat.
func (c *Client) ResumeAgent(ctx context.Context, agentID string) (*PauseResumeResponse, error) {
	path := fmt.Sprintf("/api/agents/%s/resume", url.PathEscape(agentID))
	var resp PauseResumeResponse
	if err := c.post(ctx, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("resume agent: %w", err)
	}
	return &resp, nil
}

// InvokeHeartbeat manually triggers a heartbeat for an agent.
func (c *Client) InvokeHeartbeat(ctx context.Context, agentID string) (*PauseResumeResponse, error) {
	path := fmt.Sprintf("/api/agents/%s/heartbeat/invoke", url.PathEscape(agentID))
	var resp PauseResumeResponse
	if err := c.post(ctx, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("invoke heartbeat: %w", err)
	}
	return &resp, nil
}

// ListActivity returns activity entries for a company, filtered by the given criteria.
func (c *Client) ListActivity(ctx context.Context, companyID string, filter ActivityFilter) ([]Activity, error) {
	path := fmt.Sprintf("/api/companies/%s/activity", url.PathEscape(companyID))

	params := url.Values{}
	if filter.AgentID != "" {
		params.Set("agentId", filter.AgentID)
	}
	if filter.EntityType != "" {
		params.Set("entityType", filter.EntityType)
	}
	if filter.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", filter.Limit))
	}

	var activities []Activity
	if err := c.getWithQuery(ctx, path, params, &activities); err != nil {
		return nil, fmt.Errorf("list activity: %w", err)
	}
	return activities, nil
}

// ListAllActivity returns activity across all companies, merged chronologically.
func (c *Client) ListAllActivity(ctx context.Context, filter ActivityFilter) ([]Activity, error) {
	companies, err := c.ListCompanies(ctx)
	if err != nil {
		return nil, err
	}
	var all []Activity
	for _, co := range companies {
		activities, err := c.ListActivity(ctx, co.ID, filter)
		if err != nil {
			return nil, fmt.Errorf("activity for %s: %w", co.Name, err)
		}
		all = append(all, activities...)
	}
	return all, nil
}

// get performs a GET request and decodes the JSON response into dest.
func (c *Client) get(ctx context.Context, path string, dest any) error {
	endpoint, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return fmt.Errorf("build URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed (is Paperclip running at %s?): %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Paperclip API returned %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// getWithQuery performs a GET request with query parameters and decodes the JSON response into dest.
func (c *Client) getWithQuery(ctx context.Context, path string, params url.Values, dest any) error {
	endpoint, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return fmt.Errorf("build URL: %w", err)
	}
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed (is Paperclip running at %s?): %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Paperclip API returned %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// post performs a POST request with a JSON body and decodes the response into dest.
func (c *Client) post(ctx context.Context, path string, body any, dest any) error {
	endpoint, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return fmt.Errorf("build URL: %w", err)
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed (is Paperclip running at %s?): %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Paperclip API returned %d: %s", resp.StatusCode, string(respBody))
	}

	if dest != nil {
		if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
