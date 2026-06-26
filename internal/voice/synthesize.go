package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const defaultNullhubBase = "http://nullhub.localhost:19800"

type SynthesizeRequest struct {
	Profile      map[string]any `json:"profile,omitempty"`
	Text         string         `json:"text"`
	Engine       string         `json:"engine"`
	VoiceID      string         `json:"voice_id"`
	Language     string         `json:"language"`
	OutputFormat string         `json:"output_format"`
	Goal         string         `json:"goal,omitempty"`
	SpeakingRate float64        `json:"speaking_rate"`
	Pitch        float64        `json:"pitch"`
}

type SynthesizeResponse struct {
	ArtifactPath string `json:"artifact_path"`
	Bytes        int    `json:"bytes"`
}

func NullhubBase() string {
	if v := os.Getenv("NULLHUB_BASE_URL"); v != "" {
		return v
	}

	return defaultNullhubBase
}

func Synthesize(ctx context.Context, profile *Profile, text, goal, outPath string) (string, error) {
	if profile == nil {
		return "", errors.New("voice synthesize: nil profile")
	}

	if text == "" {
		return "", errors.New("voice synthesize: empty text")
	}

	reqBody := SynthesizeRequest{
		Text:         text,
		Engine:       profile.Engine,
		VoiceID:      profile.VoiceID,
		Language:     profile.Language,
		SpeakingRate: profile.SpeakingRate,
		Pitch:        profile.Pitch,
		OutputFormat: profile.OutputFormat,
		Goal:         goal,
	}
	if reqBody.Language == "" {
		reqBody.Language = "en"
	}

	if reqBody.OutputFormat == "" {
		reqBody.OutputFormat = "wav"
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := NullhubBase() + "/api/instances/nullvoice/nullvoice-1/synthesize"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: synthesizeTimeout}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("nullvoice synthesize request: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= httpStatusErrorMin {
		return "", fmt.Errorf("nullvoice synthesize: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var synth SynthesizeResponse
	if err := json.Unmarshal(body, &synth); err == nil && synth.ArtifactPath != "" {
		return synth.ArtifactPath, nil
	}

	if outPath == "" {
		outPath = filepath.Join(os.TempDir(), fmt.Sprintf("nullvoice-%d.wav", time.Now().Unix()))
	}

	if err := os.WriteFile(outPath, body, filePerm); err != nil {
		return "", err
	}

	return outPath, nil
}
