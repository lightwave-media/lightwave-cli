package meta

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
	appSecret  string
	apiVersion string
	httpClient *http.Client
}

// NewClient creates a new Meta Graph API client.
// If appSecret is non-empty, appsecret_proof is added to every request.
func NewClient(token string, appSecret ...string) *Client {
	secret := ""
	if len(appSecret) > 0 {
		secret = appSecret[0]
	}
	return &Client{
		token:      token,
		appSecret:  secret,
		apiVersion: defaultVersion,
		httpClient: &http.Client{Timeout: httpTimeout},
	}
}

// NewAppClient creates a client using an app access token ({app_id}|{app_secret}).
// Required for app-level endpoints like webhook subscriptions.
func NewAppClient(appID, appSecret string) *Client {
	return &Client{
		token:      appID + "|" + appSecret,
		apiVersion: defaultVersion,
		httpClient: &http.Client{Timeout: httpTimeout},
	}
}

// appsecretProof computes HMAC-SHA256(token, appSecret) as hex.
func (c *Client) appsecretProof() string {
	mac := hmac.New(sha256.New, []byte(c.appSecret))
	mac.Write([]byte(c.token))
	return hex.EncodeToString(mac.Sum(nil))
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

	if c.appSecret != "" {
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		endpoint += sep + "appsecret_proof=" + c.appsecretProof()
	}

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

// --- Marketing API Response Types ---

// AdAccount represents a Meta ad account.
type AdAccount struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	AccountStatus int    `json:"account_status"`
	Currency      string `json:"currency"`
	Timezone      string `json:"timezone_name"`
	AmountSpent   string `json:"amount_spent"`
	Balance       string `json:"balance"`
}

// Campaign represents a Meta ad campaign.
type Campaign struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	Objective      string `json:"objective"`
	DailyBudget    string `json:"daily_budget"`
	LifetimeBudget string `json:"lifetime_budget"`
	CreatedTime    string `json:"created_time"`
}

// AdSet represents a Meta ad set.
type AdSet struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Status      string          `json:"status"`
	CampaignID  string          `json:"campaign_id"`
	Targeting   json.RawMessage `json:"targeting"`
	DailyBudget string          `json:"daily_budget"`
	BidAmount   string          `json:"bid_amount"`
}

// Ad represents a Meta ad.
type Ad struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Status   string     `json:"status"`
	AdSetID  string     `json:"adset_id"`
	Creative AdCreative `json:"creative"`
}

// AdCreative is a summary of the creative attached to an ad.
type AdCreative struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// InsightRow represents a single row of ad insights data.
type InsightRow struct {
	DateStart   string          `json:"date_start"`
	DateStop    string          `json:"date_stop"`
	Impressions string          `json:"impressions"`
	Clicks      string          `json:"clicks"`
	Spend       string          `json:"spend"`
	CTR         string          `json:"ctr"`
	CPC         string          `json:"cpc"`
	CPM         string          `json:"cpm"`
	Reach       string          `json:"reach"`
	Actions     json.RawMessage `json:"actions,omitempty"`
}

type dataResponse[T any] struct {
	Data []T `json:"data"`
}

// --- Marketing API Read Operations ---

const (
	adAccountFields = "id,name,account_status,currency,timezone_name,amount_spent,balance"
	campaignFields  = "id,name,status,objective,daily_budget,lifetime_budget,created_time"
	adSetFields     = "id,name,status,campaign_id,targeting,daily_budget,bid_amount"
	adFields        = "id,name,status,adset_id,creative{id,name}"
	insightFields   = "date_start,date_stop,impressions,clicks,spend,ctr,cpc,cpm,reach,actions"
)

// ListAdAccounts returns ad accounts accessible by the current user.
func (c *Client) ListAdAccounts(ctx context.Context) ([]AdAccount, error) {
	path := "me/adaccounts?fields=" + url.QueryEscape(adAccountFields) + "&limit=100"
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp dataResponse[AdAccount]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode ad accounts: %w", err)
	}
	return resp.Data, nil
}

// ListCampaigns returns campaigns for the given ad account.
func (c *Client) ListCampaigns(ctx context.Context, accountID string) ([]Campaign, error) {
	path := accountID + "/campaigns?fields=" + url.QueryEscape(campaignFields) + "&limit=100"
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp dataResponse[Campaign]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode campaigns: %w", err)
	}
	return resp.Data, nil
}

// ListAdSets returns ad sets for the given ad account, optionally filtered by campaign.
func (c *Client) ListAdSets(ctx context.Context, accountID string, campaignID string) ([]AdSet, error) {
	path := accountID + "/adsets?fields=" + url.QueryEscape(adSetFields) + "&limit=100"
	if campaignID != "" {
		path += "&filtering=" + url.QueryEscape(`[{"field":"campaign.id","operator":"EQUAL","value":"`+campaignID+`"}]`)
	}
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp dataResponse[AdSet]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode ad sets: %w", err)
	}
	return resp.Data, nil
}

// ListAds returns ads for the given ad account, optionally filtered by ad set.
func (c *Client) ListAds(ctx context.Context, accountID string, adSetID string) ([]Ad, error) {
	path := accountID + "/ads?fields=" + url.QueryEscape(adFields) + "&limit=100"
	if adSetID != "" {
		path += "&filtering=" + url.QueryEscape(`[{"field":"adset.id","operator":"EQUAL","value":"`+adSetID+`"}]`)
	}
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp dataResponse[Ad]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode ads: %w", err)
	}
	return resp.Data, nil
}

// GetInsights returns insights for any object (campaign, ad set, ad, or account).
func (c *Client) GetInsights(ctx context.Context, objectID string, datePreset string) ([]InsightRow, error) {
	path := objectID + "/insights?fields=" + url.QueryEscape(insightFields) + "&limit=100"
	if datePreset != "" {
		path += "&date_preset=" + url.QueryEscape(datePreset)
	}
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp dataResponse[InsightRow]
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode insights: %w", err)
	}
	return resp.Data, nil
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
