package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	graphBaseURL   = "https://graph.facebook.com"
	defaultVersion = "v21.0"
	httpTimeout    = 30 * time.Second
)

// Client is an HTTP client for the Meta Graph API.
type Client struct {
	token      string
	apiVersion string
	httpClient *http.Client
}

// NewClient creates a new Meta Graph API client.
func NewClient(token string) *Client {
	return &Client{
		token:      token,
		apiVersion: defaultVersion,
		httpClient: &http.Client{Timeout: httpTimeout},
	}
}

// APIError represents an error returned by the Meta Graph API.
type APIError struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Code      int    `json:"code"`
	FBTraceID string `json:"fbtrace_id"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("meta api: %s (type=%s code=%d trace=%s)", e.Message, e.Type, e.Code, e.FBTraceID)
}

type errorEnvelope struct {
	Error APIError `json:"error"`
}

// do executes an HTTP request against the Graph API and returns the raw response body.
func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/%s/%s", graphBaseURL, c.apiVersion, path)

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope errorEnvelope
		if err := json.Unmarshal(respBody, &envelope); err == nil && envelope.Error.Message != "" {
			return nil, &envelope.Error
		}
		return nil, fmt.Errorf("meta api: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// --- Response Types ---

// PhoneNumber represents a phone number registered on a WABA.
type PhoneNumber struct {
	ID                     string `json:"id"`
	DisplayPhoneNumber     string `json:"display_phone_number"`
	VerifiedName           string `json:"verified_name"`
	QualityRating          string `json:"quality_rating"`
	CodeVerificationStatus string `json:"code_verification_status"`
	AccountMode            string `json:"account_mode"`
}

type phoneNumbersResponse struct {
	Data []PhoneNumber `json:"data"`
}

// WebhookSub represents a webhook subscription on an app.
type WebhookSub struct {
	Object      string         `json:"object"`
	CallbackURL string         `json:"callback_url"`
	Active      bool           `json:"active"`
	Fields      []WebhookField `json:"fields"`
}

// WebhookField represents a single field in a webhook subscription.
type WebhookField struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type webhookSubsResponse struct {
	Data []WebhookSub `json:"data"`
}

// TokenDebugInfo contains debug information about a token.
type TokenDebugInfo struct {
	AppID          string          `json:"app_id"`
	Type           string          `json:"type"`
	Application    string          `json:"application"`
	IsValid        bool            `json:"is_valid"`
	ExpiresAt      int64           `json:"expires_at"`
	Scopes         []string        `json:"scopes"`
	UserID         string          `json:"user_id"`
	GranularScopes []GranularScope `json:"granular_scopes"`
}

// GranularScope represents a permission scope with target IDs.
type GranularScope struct {
	Scope     string   `json:"scope"`
	TargetIDs []string `json:"target_ids"`
}

type tokenDebugResponse struct {
	Data TokenDebugInfo `json:"data"`
}

// MessageResponse contains the result of sending a message.
type MessageResponse struct {
	MessagingProduct string `json:"messaging_product"`
	Contacts         []struct {
		Input string `json:"input"`
		WaID  string `json:"wa_id"`
	} `json:"contacts"`
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
}

// --- Read Operations ---

// ListPhoneNumbers returns all phone numbers registered on a WABA.
func (c *Client) ListPhoneNumbers(ctx context.Context, wabaID string) ([]PhoneNumber, error) {
	data, err := c.do(ctx, http.MethodGet, wabaID+"/phone_numbers", nil)
	if err != nil {
		return nil, err
	}

	var resp phoneNumbersResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode phone numbers: %w", err)
	}

	return resp.Data, nil
}

// GetWebhookSubscriptions returns all webhook subscriptions for an app.
func (c *Client) GetWebhookSubscriptions(ctx context.Context, appID string) ([]WebhookSub, error) {
	data, err := c.do(ctx, http.MethodGet, appID+"/subscriptions", nil)
	if err != nil {
		return nil, err
	}

	var resp webhookSubsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode webhook subscriptions: %w", err)
	}

	return resp.Data, nil
}

// DebugToken returns debug info for the given token.
func (c *Client) DebugToken(ctx context.Context, inputToken string) (*TokenDebugInfo, error) {
	path := "debug_token?input_token=" + url.QueryEscape(inputToken)
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp tokenDebugResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode token debug info: %w", err)
	}

	return &resp.Data, nil
}

// --- Write Operations ---

// AddPhoneNumberOpts contains options for adding a phone number to a WABA.
type AddPhoneNumberOpts struct {
	CountryCode string `json:"cc"`
	PhoneNumber string `json:"phone_number"`
}

// AddPhoneNumber adds a phone number to a WABA.
func (c *Client) AddPhoneNumber(ctx context.Context, wabaID string, opts AddPhoneNumberOpts) ([]byte, error) {
	return c.do(ctx, http.MethodPost, wabaID+"/phone_numbers", opts)
}

// RegisterPhone registers a phone number for WhatsApp messaging.
func (c *Client) RegisterPhone(ctx context.Context, phoneID, pin string) ([]byte, error) {
	body := map[string]string{
		"messaging_product": "whatsapp",
		"pin":               pin,
	}
	return c.do(ctx, http.MethodPost, phoneID+"/register", body)
}

// DeregisterPhone deregisters a phone number from WhatsApp messaging.
func (c *Client) DeregisterPhone(ctx context.Context, phoneID string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, phoneID+"/deregister", nil)
}

// SetWebhook configures a webhook subscription for an app.
func (c *Client) SetWebhook(ctx context.Context, appID, callbackURL, verifyToken string, fields []string) ([]byte, error) {
	body := map[string]any{
		"object":       "whatsapp_business_account",
		"callback_url": callbackURL,
		"verify_token": verifyToken,
		"fields":       fields,
	}
	return c.do(ctx, http.MethodPost, appID+"/subscriptions", body)
}

// SendTextMessage sends a text message via the WhatsApp Cloud API.
func (c *Client) SendTextMessage(ctx context.Context, phoneID, to, body string) (*MessageResponse, error) {
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "text",
		"text":              map[string]string{"body": body},
	}

	data, err := c.do(ctx, http.MethodPost, phoneID+"/messages", payload)
	if err != nil {
		return nil, err
	}

	var resp MessageResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode message response: %w", err)
	}

	return &resp, nil
}
