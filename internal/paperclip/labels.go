package paperclip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Label is a paperclip issue label scoped to a company.
type Label struct {
	ID        string    `json:"id"`
	CompanyID string    `json:"companyId"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Project is a paperclip project (groups issues, tied to one or more goals).
type Project struct {
	ID          string     `json:"id"`
	CompanyID   string     `json:"companyId"`
	GoalID      string     `json:"goalId,omitempty"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status,omitempty"`
	URLKey      string     `json:"urlKey,omitempty"`
	Color       string     `json:"color,omitempty"`
	ArchivedAt  *time.Time `json:"archivedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// Goal is a paperclip goal (high-level outcome).
type Goal struct {
	ID          string    `json:"id"`
	CompanyID   string    `json:"companyId"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Level       string    `json:"level,omitempty"`
	Status      string    `json:"status,omitempty"`
	ParentID    string    `json:"parentId,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// issueWithLabels is the subset of an issue we need for label add/remove.
type issueWithLabels struct {
	ID       string   `json:"id"`
	LabelIDs []string `json:"labelIds"`
}

// ListLabels returns all labels defined on a company.
func (c *Client) ListLabels(ctx context.Context, companyID string) ([]Label, error) {
	path := fmt.Sprintf("/api/companies/%s/labels", url.PathEscape(companyID))
	var labels []Label
	if err := c.get(ctx, path, &labels); err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	return labels, nil
}

// FindLabelByName resolves a label name (case-insensitive) to its ID.
func (c *Client) FindLabelByName(ctx context.Context, companyID, name string) (*Label, error) {
	labels, err := c.ListLabels(ctx, companyID)
	if err != nil {
		return nil, err
	}
	target := strings.ToLower(strings.TrimSpace(name))
	for i := range labels {
		if strings.ToLower(labels[i].Name) == target {
			return &labels[i], nil
		}
	}
	return nil, fmt.Errorf("label %q not found in company %s", name, companyID)
}

// CreateLabel creates a new label on a company.
func (c *Client) CreateLabel(ctx context.Context, companyID, name, color string) (*Label, error) {
	path := fmt.Sprintf("/api/companies/%s/labels", url.PathEscape(companyID))
	body := map[string]string{"name": name, "color": color}
	var created Label
	if err := c.post(ctx, path, body, &created); err != nil {
		return nil, fmt.Errorf("create label: %w", err)
	}
	return &created, nil
}

// AddIssueLabel adds a label to an issue (idempotent set semantics).
// issueIDOrIdentifier accepts either a UUID or an identifier like "LIGA-408".
func (c *Client) AddIssueLabel(ctx context.Context, issueIDOrIdentifier, labelID string) ([]string, error) {
	current, err := c.fetchIssueLabelIDs(ctx, issueIDOrIdentifier)
	if err != nil {
		return nil, err
	}
	if slices.Contains(current, labelID) {
		return current, nil
	}
	next := append(current, labelID)
	return next, c.patchIssueLabels(ctx, issueIDOrIdentifier, next)
}

// RemoveIssueLabel removes a label from an issue. No-op if not present.
func (c *Client) RemoveIssueLabel(ctx context.Context, issueIDOrIdentifier, labelID string) ([]string, error) {
	current, err := c.fetchIssueLabelIDs(ctx, issueIDOrIdentifier)
	if err != nil {
		return nil, err
	}
	next := make([]string, 0, len(current))
	for _, id := range current {
		if id != labelID {
			next = append(next, id)
		}
	}
	if len(next) == len(current) {
		return current, nil
	}
	return next, c.patchIssueLabels(ctx, issueIDOrIdentifier, next)
}

func (c *Client) fetchIssueLabelIDs(ctx context.Context, issueID string) ([]string, error) {
	path := fmt.Sprintf("/api/issues/%s", url.PathEscape(issueID))
	var issue issueWithLabels
	if err := c.get(ctx, path, &issue); err != nil {
		return nil, fmt.Errorf("get issue %s: %w", issueID, err)
	}
	return issue.LabelIDs, nil
}

func (c *Client) patchIssueLabels(ctx context.Context, issueID string, labelIDs []string) error {
	path := fmt.Sprintf("/api/issues/%s", url.PathEscape(issueID))
	body := map[string]any{"labelIds": labelIDs}
	return c.patch(ctx, path, body, nil)
}

// ListProjects returns all projects for a company.
func (c *Client) ListProjects(ctx context.Context, companyID string) ([]Project, error) {
	path := fmt.Sprintf("/api/companies/%s/projects", url.PathEscape(companyID))
	var projects []Project
	if err := c.get(ctx, path, &projects); err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return projects, nil
}

// CreateProject creates a new project on a company.
func (c *Client) CreateProject(ctx context.Context, companyID, name, description string) (*Project, error) {
	path := fmt.Sprintf("/api/companies/%s/projects", url.PathEscape(companyID))
	body := map[string]any{"name": name}
	if description != "" {
		body["description"] = description
	}
	var created Project
	if err := c.post(ctx, path, body, &created); err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	return &created, nil
}

// ListGoals returns all goals for a company.
func (c *Client) ListGoals(ctx context.Context, companyID string) ([]Goal, error) {
	path := fmt.Sprintf("/api/companies/%s/goals", url.PathEscape(companyID))
	var goals []Goal
	if err := c.get(ctx, path, &goals); err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	return goals, nil
}

// CreateGoal creates a new goal on a company.
func (c *Client) CreateGoal(ctx context.Context, companyID, title, description string) (*Goal, error) {
	path := fmt.Sprintf("/api/companies/%s/goals", url.PathEscape(companyID))
	body := map[string]any{"title": title}
	if description != "" {
		body["description"] = description
	}
	var created Goal
	if err := c.post(ctx, path, body, &created); err != nil {
		return nil, fmt.Errorf("create goal: %w", err)
	}
	return &created, nil
}

// patch performs a PATCH request with a JSON body. dest may be nil to discard the response.
func (c *Client) patch(ctx context.Context, path string, body any, dest any) error {
	endpoint, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return fmt.Errorf("build URL: %w", err)
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(bodyBytes))
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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
