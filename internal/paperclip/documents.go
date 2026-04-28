package paperclip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// PutDocument uploads or revisions a text document attached to an issue.
// Documents are keyed (e.g. "prd", "plan", "spec") and revisioned by Paperclip.
// Endpoint per https://docs.paperclip.ing/api/issues#documents.
func (c *Client) PutDocument(ctx context.Context, issueIDOrIdentifier, key string, body []byte) (*Document, error) {
	path := fmt.Sprintf("/api/issues/%s/documents/%s",
		url.PathEscape(issueIDOrIdentifier),
		url.PathEscape(key),
	)

	endpoint, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return nil, fmt.Errorf("build URL: %w", err)
	}

	payload, err := json.Marshal(map[string]string{"body": string(body)})
	if err != nil {
		return nil, fmt.Errorf("encode document body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed (is Paperclip running at %s?): %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("put document %s/%s: Paperclip API returned %d: %s",
			issueIDOrIdentifier, key, resp.StatusCode, string(respBody))
	}

	var doc Document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		// Some endpoints may return empty bodies on success; tolerate.
		return &Document{Key: key}, nil
	}
	if doc.Key == "" {
		doc.Key = key
	}
	return &doc, nil
}

// PutDocumentFromFile is a convenience wrapper that reads a file from disk
// and uploads it as a document keyed by `key`.
func (c *Client) PutDocumentFromFile(ctx context.Context, issueIDOrIdentifier, key, filePath string) (*Document, error) {
	body, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filePath, err)
	}
	return c.PutDocument(ctx, issueIDOrIdentifier, key, body)
}

// UploadAttachment uploads a binary file as an attachment to an issue.
// Endpoint per https://docs.paperclip.ing/api/issues#attachments.
func (c *Client) UploadAttachment(ctx context.Context, issueIDOrIdentifier, filename string, body io.Reader) (*Attachment, error) {
	path := fmt.Sprintf("/api/issues/%s/attachments", url.PathEscape(issueIDOrIdentifier))
	endpoint, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return nil, fmt.Errorf("build URL: %w", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("multipart writer: %w", err)
	}
	if _, err := io.Copy(part, body); err != nil {
		return nil, fmt.Errorf("copy body: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed (is Paperclip running at %s?): %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload attachment %s: Paperclip API returned %d: %s",
			filename, resp.StatusCode, string(respBody))
	}

	var att Attachment
	if err := json.NewDecoder(resp.Body).Decode(&att); err != nil {
		return &Attachment{Filename: filename}, nil
	}
	if att.Filename == "" {
		att.Filename = filename
	}
	return &att, nil
}

// UploadAttachmentFromFile is a convenience wrapper for uploading a file from disk.
func (c *Client) UploadAttachmentFromFile(ctx context.Context, issueIDOrIdentifier, filePath string) (*Attachment, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()
	return c.UploadAttachment(ctx, issueIDOrIdentifier, filepath.Base(filePath), f)
}

// LinkBlock records a "blockedBy" relationship: `issueID` is blocked by `blockedByID`.
// Implemented via PATCH on the labelIds-style endpoint, merging into blockedByIds.
func (c *Client) LinkBlock(ctx context.Context, issueIDOrIdentifier, blockedByID string) error {
	// Fetch current blockedByIds to merge.
	type blockedShape struct {
		BlockedByIDs []string `json:"blockedByIds"`
	}
	getPath := fmt.Sprintf("/api/issues/%s", url.PathEscape(issueIDOrIdentifier))
	var current blockedShape
	if err := c.get(ctx, getPath, &current); err != nil {
		return fmt.Errorf("fetch blockedByIds: %w", err)
	}
	for _, id := range current.BlockedByIDs {
		if id == blockedByID {
			return nil // already linked
		}
	}
	next := append(current.BlockedByIDs, blockedByID)
	return c.patch(ctx, getPath, map[string]any{"blockedByIds": next}, nil)
}
