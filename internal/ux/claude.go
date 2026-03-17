package ux

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultProxyURL = "http://localhost:3456"
	proxyEnvVar     = "CLAUDE_CLI_BASE_URL"
)

const analysisSystemPrompt = `You are analyzing a UX feedback recording for LightWave Media.
The user recorded their screen while navigating LightWave products and narrated their experience aloud.

You will receive:
1. Timestamped transcript segments (what the user said)
2. Screen captures at key moments (what was on screen)

Extract improvement items. For each item, provide:
- severity: "critical" | "major" | "minor"
- category: "bug" | "usability" | "design" | "performance" | "content" | "feature_request"
- description: What the issue or request is
- user_quote: The relevant quote from the narration (if applicable)
- affected_component: Which page, section, or component is affected
- timestamp: Approximate time in the recording (MM:SS)
- source: "narration" (user explicitly said it) | "visual" (observed from screen) | "both"

Output ONLY a JSON array of items. No markdown fencing, no explanation — just the JSON array.
Focus on actionable improvements. Ignore filler speech and off-topic comments.
If the user gives an explicit instruction ("change this to X", "make this bigger"), treat it as a feature_request with the user's exact words.`

// AnalysisResult holds the Claude analysis output.
type AnalysisResult struct {
	Items   []ImprovementItem
	RawText string
}

// proxyBaseURL returns the Claude Code HTTP proxy URL.
func proxyBaseURL() string {
	if url := os.Getenv(proxyEnvVar); url != "" {
		return url
	}
	return defaultProxyURL
}

// OpenAI-compatible request/response types for the Claude Code proxy.

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []contentPart
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Error   *chatError   `json:"error,omitempty"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type chatError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// AnalyzeWithClaude sends transcript + keyframes to Claude via the local proxy.
func AnalyzeWithClaude(ctx context.Context, sessionID string, transcript string, framePaths []string) (*AnalysisResult, error) {
	// Build multimodal content parts
	var parts []contentPart

	// Transcript as text
	parts = append(parts, contentPart{
		Type: "text",
		Text: fmt.Sprintf(
			"## Transcript\n\n%s\n\n## Screen Captures\n\nThe following images are keyframes extracted from the recording at regular intervals. Analyze them alongside the transcript above.",
			transcript,
		),
	})

	// Each keyframe as a base64 data URI image
	for _, framePath := range framePaths {
		data, err := os.ReadFile(framePath)
		if err != nil {
			continue
		}

		ext := strings.ToLower(filepath.Ext(framePath))
		mediaType := "image/jpeg"
		if ext == ".png" {
			mediaType = "image/png"
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		parts = append(parts, contentPart{
			Type: "image_url",
			ImageURL: &imageURL{
				URL: fmt.Sprintf("data:%s;base64,%s", mediaType, encoded),
			},
		})
	}

	reqBody := chatRequest{
		Model:     "claude-sonnet-4",
		MaxTokens: 8192,
		Messages: []chatMessage{
			{Role: "system", Content: analysisSystemPrompt},
			{Role: "user", Content: parts},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := proxyBaseURL() + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("proxy request to %s: %w", proxyBaseURL(), err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("proxy returned %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("claude error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("empty response from claude")
	}

	// Extract response text
	responseText, ok := chatResp.Choices[0].Message.Content.(string)
	if !ok {
		return nil, fmt.Errorf("unexpected response content type")
	}

	// Parse improvement items from JSON response
	var items []ImprovementItem
	cleaned := strings.TrimSpace(responseText)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if err := json.Unmarshal([]byte(cleaned), &items); err != nil {
		return &AnalysisResult{
			RawText: responseText,
		}, fmt.Errorf("parse items (raw text saved): %w", err)
	}

	for i := range items {
		items[i].ID = i + 1
	}

	return &AnalysisResult{
		Items:   items,
		RawText: responseText,
	}, nil
}
