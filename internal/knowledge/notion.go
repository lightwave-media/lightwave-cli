package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	notionVersion   = "2026-03-11"
	requestTimeout  = 45 * time.Second
	requestInterval = 350 * time.Millisecond
	responseLimit   = 32 << 20
	retryDelayLimit = 30
	queryPageSize   = 100
)

type Remote interface {
	List(context.Context, string) ([]RemotePage, error)
	Fetch(context.Context, string) (Page, error)
	Update(context.Context, string, Content, Content) (Page, error)
}

type RemotePage struct {
	Edited time.Time `json:"last_edited_time"`
	ID     string    `json:"id"`
}

type Client struct {
	last     time.Time
	http     *http.Client
	base     string
	token    string
	interval time.Duration
	mu       sync.Mutex
}

func NewClient(token string) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("notion runtime credential is missing")
	}

	return &Client{http: &http.Client{Timeout: requestTimeout}, base: "https://api.notion.com/v1", token: token, interval: requestInterval}, nil
}

func wait(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (client *Client) request(ctx context.Context, method, path string, value, out any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}

	for attempt := 0; attempt < 3; attempt++ {
		client.mu.Lock()
		err = wait(ctx, time.Until(client.last.Add(client.interval)))
		client.last = time.Now()
		client.mu.Unlock()

		if err != nil {
			return err
		}

		var reader io.Reader
		if value != nil {
			reader = bytes.NewReader(body)
		}

		req, err := http.NewRequestWithContext(ctx, method, client.base+path, reader)
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", "Bearer "+client.token)
		req.Header.Set("Notion-Version", notionVersion)
		req.Header.Set("Content-Type", "application/json")

		response, err := client.http.Do(req)
		if err != nil {
			return fmt.Errorf("notion request failed: %w", err)
		}

		payload, readErr := io.ReadAll(io.LimitReader(response.Body, responseLimit))
		_ = response.Body.Close()

		if readErr != nil {
			return readErr
		}

		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if response.StatusCode == http.StatusAccepted {
				return errors.New("unexpected asynchronous Notion response; delivery remains pending")
			}

			return json.Unmarshal(payload, out)
		}

		if response.StatusCode == http.StatusTooManyRequests && attempt < 2 {
			seconds, _ := strconv.Atoi(response.Header.Get("Retry-After"))
			if seconds < 1 {
				seconds = 1 << attempt
			}

			if seconds > retryDelayLimit {
				return errors.New("notion rate limit exceeds this run's retry budget")
			}

			if err := wait(ctx, time.Duration(seconds)*time.Second); err != nil {
				return err
			}

			continue
		}
		// Never echo response bodies or credentials into logs. Do not blindly
		// replay an uncertain write; the durable intent is reconciled next run.
		return fmt.Errorf("notion %s %s returned HTTP %d", method, path, response.StatusCode)
	}

	return errors.New("notion retry budget exhausted")
}

func (client *Client) List(ctx context.Context, source string) ([]RemotePage, error) {
	if _, err := uuid.Parse(source); err != nil {
		return nil, fmt.Errorf("data source ID: %w", err)
	}

	var ids []RemotePage

	cursor := ""
	seen := make(map[string]bool)

	for {
		body := map[string]any{"page_size": queryPageSize}
		if cursor != "" {
			body["start_cursor"] = cursor
		}

		var page struct {
			NextCursor string       `json:"next_cursor"`
			Results    []RemotePage `json:"results"`
			HasMore    bool         `json:"has_more"`
		}
		if err := client.request(ctx, http.MethodPost, "/data_sources/"+source+"/query", body, &page); err != nil {
			return nil, err
		}

		for _, result := range page.Results {
			if _, err := uuid.Parse(result.ID); err != nil {
				return nil, err
			}

			ids = append(ids, result)
		}

		if !page.HasMore {
			return ids, nil
		}

		if page.NextCursor == "" || seen[page.NextCursor] {
			return nil, errors.New("notion pagination did not advance")
		}

		cursor = page.NextCursor
		seen[cursor] = true
	}
}

func (client *Client) Fetch(ctx context.Context, id string) (Page, error) {
	var result Page

	identity, err := uuid.Parse(id)
	if err != nil {
		return result, err
	}

	var page struct {
		Created    time.Time                  `json:"created_time"`
		Edited     time.Time                  `json:"last_edited_time"`
		Parent     map[string]string          `json:"parent"`
		Properties map[string]json.RawMessage `json:"properties"`
		ID         string                     `json:"id"`
		URL        string                     `json:"url"`
		Archived   bool                       `json:"archived"`
		InTrash    bool                       `json:"in_trash"`
	}
	if err := client.request(ctx, http.MethodGet, "/pages/"+id, nil, &page); err != nil {
		return result, err
	}

	if page.ID != id {
		return result, errors.New("notion returned a different page identity")
	}

	if err := client.hydrateProperties(ctx, id, page.Properties); err != nil {
		return result, err
	}

	var markdown struct {
		Markdown  string   `json:"markdown"`
		Unknown   []string `json:"unknown_block_ids"`
		Truncated bool     `json:"truncated"`
	}
	if err := client.request(ctx, http.MethodGet, "/pages/"+id+"/markdown?include_transcript=true", nil, &markdown); err != nil {
		return result, err
	}

	props, err := json.Marshal(page.Properties)
	if err != nil {
		return result, err
	}

	unknown, err := json.Marshal(markdown.Unknown)
	if err != nil {
		return result, err
	}

	parentType := page.Parent["type"]

	result = Page{ID: identity, NotionId: id, Url: page.URL, ParentType: parentType,
		ParentId: ptr(page.Parent[parentType]), CreatedAt: page.Created, UpdatedAt: page.Edited, LastEditedAt: page.Edited,
		Archived: ptr(page.Archived || page.InTrash), Markdown: ptr(markdown.Markdown), PropertiesJson: ptr(string(props)),
		ContentComplete: ptr(!markdown.Truncated && len(markdown.Unknown) == 0 && !strings.Contains(markdown.Markdown, "<unknown")),
		UnknownBlockIds: unknown, SyncStatus: StatusSynced}
	if parentType == "data_source_id" {
		result.DataSourceId = ptr(page.Parent[parentType])
	}

	content, err := ContentOf(result)
	if err != nil {
		return result, err
	}

	err = applyContent(&result, content)

	return result, err
}

func (client *Client) hydrateProperties(ctx context.Context, id string, properties map[string]json.RawMessage) error {
	for name, raw := range properties {
		var property map[string]json.RawMessage
		if err := json.Unmarshal(raw, &property); err != nil {
			return err
		}

		var kind, propertyID string

		_ = json.Unmarshal(property["type"], &kind)
		_ = json.Unmarshal(property["id"], &propertyID)

		if kind != "relation" && kind != "title" && kind != "rich_text" && kind != "people" {
			continue
		}

		var items []json.RawMessage
		if err := json.Unmarshal(property[kind], &items); err != nil {
			return err
		}

		var more bool

		_ = json.Unmarshal(property["has_more"], &more)
		if !more && len(items) < 25 {
			continue
		}

		complete, err := client.propertyItems(ctx, id, propertyID, kind)
		if err != nil {
			return fmt.Errorf("complete property %s: %w", name, err)
		}

		property[kind], err = json.Marshal(complete)
		if err != nil {
			return err
		}

		delete(property, "has_more")

		properties[name], err = json.Marshal(property)
		if err != nil {
			return err
		}
	}

	return nil
}

func (client *Client) propertyItems(ctx context.Context, pageID, propertyID, kind string) ([]json.RawMessage, error) {
	var items []json.RawMessage

	decodedID, err := url.PathUnescape(propertyID)
	if err != nil {
		return nil, fmt.Errorf("invalid property identity: %w", err)
	}

	escapedID := url.PathEscape(decodedID)
	path := "/pages/" + pageID + "/properties/" + escapedID + "?page_size=100"
	seen := make(map[string]bool)

	for {
		var page struct {
			NextCursor string                       `json:"next_cursor"`
			Results    []map[string]json.RawMessage `json:"results"`
			HasMore    bool                         `json:"has_more"`
		}
		if err := client.request(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}

		for _, item := range page.Results {
			items = append(items, item[kind])
		}

		if !page.HasMore {
			return items, nil
		}

		if page.NextCursor == "" || seen[page.NextCursor] {
			return nil, errors.New("property pagination did not advance")
		}

		seen[page.NextCursor] = true
		path = "/pages/" + pageID + "/properties/" + escapedID + "?page_size=100&start_cursor=" + url.QueryEscape(page.NextCursor)
	}
}

func (client *Client) Update(ctx context.Context, id string, before, target Content) (Page, error) {
	// Re-fetch immediately before writing: timestamps are not conditional
	// write tokens. Divergence is a conflict, not permission to overwrite.
	fresh, err := client.Fetch(ctx, id)
	if err != nil {
		return Page{}, err
	}

	current, err := ContentOf(fresh)
	if err != nil {
		return Page{}, err
	}

	if !equalContent(current, before) {
		return Page{}, errors.New("notion changed before delivery; reconcile again")
	}

	changes, err := writableChanges(before.Properties, target.Properties)
	if err != nil {
		return Page{}, err
	}

	if before.Markdown != target.Markdown && (fresh.ContentComplete == nil || !*fresh.ContentComplete) {
		return Page{}, errors.New("refusing to replace incomplete Notion page content")
	}

	if len(changes) > 0 {
		var response json.RawMessage
		if err := client.request(ctx, http.MethodPatch, "/pages/"+id, map[string]any{"properties": changes}, &response); err != nil {
			return Page{}, err
		}
	}

	if before.Markdown != target.Markdown {
		var response json.RawMessage

		command := map[string]any{"type": "replace_content", "replace_content": map[string]any{"new_str": target.Markdown, "allow_deleting_content": false}}
		if before.Markdown != "" {
			command = map[string]any{"type": "update_content", "update_content": map[string]any{"content_updates": []map[string]string{{"old_str": before.Markdown, "new_str": target.Markdown}}, "allow_deleting_content": false}}
		}

		if err := client.request(ctx, http.MethodPatch, "/pages/"+id+"/markdown", command, &response); err != nil {
			return Page{}, err
		}
	}

	return client.Fetch(ctx, id)
}

func writableChanges(before, target map[string]json.RawMessage) (map[string]any, error) {
	result := make(map[string]any)

	for name, raw := range target {
		if equalJSON(raw, before[name]) {
			continue
		}

		var property map[string]json.RawMessage
		if err := json.Unmarshal(raw, &property); err != nil {
			return nil, err
		}

		var kind string

		_ = json.Unmarshal(property["type"], &kind)
		switch kind {
		case "title", "rich_text", "select", "multi_select", "status", "date", "number", "url", "relation", "checkbox", "email", "phone_number", "people":
			result[name] = map[string]json.RawMessage{kind: property[kind]}
		default:
			return nil, fmt.Errorf("property %s (%s) is not writable; retain and resolve locally", name, kind)
		}
	}

	for name := range before {
		if _, exists := target[name]; !exists {
			return nil, fmt.Errorf("removing property %s requires an explicit schema change", name)
		}
	}

	return result, nil
}
