package ux

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
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

// AnalyzeWithClaude sends transcript + keyframes to Claude for UX analysis.
func AnalyzeWithClaude(ctx context.Context, sessionID string, transcript string, framePaths []string) (*AnalysisResult, error) {
	client := anthropic.NewClient()

	// Build content blocks: transcript text + interleaved keyframe images
	var blocks []anthropic.ContentBlockParamUnion

	// Add transcript as the first text block
	blocks = append(blocks, anthropic.NewTextBlock(fmt.Sprintf(
		"## Transcript\n\n%s\n\n## Screen Captures\n\nThe following images are keyframes extracted from the recording at regular intervals. Analyze them alongside the transcript above.",
		transcript,
	)))

	// Add each keyframe as a base64 image block
	for _, framePath := range framePaths {
		data, err := os.ReadFile(framePath)
		if err != nil {
			continue // skip unreadable frames
		}

		ext := strings.ToLower(filepath.Ext(framePath))
		mediaType := "image/jpeg"
		if ext == ".png" {
			mediaType = "image/png"
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		blocks = append(blocks, anthropic.NewImageBlockBase64(mediaType, encoded))
	}

	message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     "claude-sonnet-4-6",
		MaxTokens: 8192,
		System: []anthropic.TextBlockParam{
			{Text: analysisSystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(blocks...),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude api: %w", err)
	}

	// Extract response text
	var responseText string
	for _, block := range message.Content {
		if block.Type == "text" {
			responseText += block.Text
		}
	}

	// Parse improvement items from JSON response
	var items []ImprovementItem
	// Strip any markdown code fences if present
	cleaned := strings.TrimSpace(responseText)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	if err := json.Unmarshal([]byte(cleaned), &items); err != nil {
		// If parsing fails, still return the raw text
		return &AnalysisResult{
			RawText: responseText,
		}, fmt.Errorf("parse items (raw text saved): %w", err)
	}

	// Assign sequential IDs
	for i := range items {
		items[i].ID = i + 1
	}

	return &AnalysisResult{
		Items:   items,
		RawText: responseText,
	}, nil
}
