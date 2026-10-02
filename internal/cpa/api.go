package cpa

import (
	"context"
	"net/http"
	"net/url"
)

// Credential is one entry of GET /credentials (CPA v8.0.10 auth_files.go:655-787).
// Only fields lastcall uses are decoded; `account` is skipped on purpose because
// for API-key credentials it holds the key itself.
type Credential struct {
	ID             string        `json:"id"`
	AuthIndex      string        `json:"auth_index"`
	Name           string        `json:"name"`
	Provider       string        `json:"provider"`
	Type           string        `json:"type"`
	Label          string        `json:"label"`
	Status         string        `json:"status"`
	StatusMessage  string        `json:"status_message"`
	Disabled       bool          `json:"disabled"`
	Unavailable    bool          `json:"unavailable"`
	RuntimeOnly    bool          `json:"runtime_only"`
	Success        int64         `json:"success"`
	Failed         int64         `json:"failed"`
	RecentRequests []Bucket      `json:"recent_requests"`
	Email          string        `json:"email"`
	ProjectID      string        `json:"project_id"`
	AccountType    string        `json:"account_type"`
	NextRetryAfter string        `json:"next_retry_after"`
	Priority       int           `json:"priority"`
	IDToken        *CodexIDToken `json:"id_token"`
}

// Bucket is one 10-minute slot of recent traffic; the list always holds 20.
type Bucket struct {
	Time    string `json:"time"`
	Success int64  `json:"success"`
	Failed  int64  `json:"failed"`
}

// CodexIDToken holds parsed claims, not the raw JWT.
type CodexIDToken struct {
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	PlanType         string `json:"plan_type"`
	ActiveUntil      string `json:"chatgpt_subscription_active_until"`
}

func (c *Client) Credentials(ctx context.Context) ([]Credential, error) {
	var out struct {
		Files []Credential `json:"files"`
	}
	if err := c.do(ctx, http.MethodGet, "/credentials", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Files, nil
}

// SetDisabled flips a credential on or off. CPA persists it to the auth file and
// stops scheduling new requests on it; in-flight requests are left alone.
func (c *Client) SetDisabled(ctx context.Context, name, authIndex string, disabled bool) error {
	body := map[string]any{"name": name, "auth_index": authIndex, "disabled": disabled}
	return c.do(ctx, http.MethodPatch, "/credentials/status", nil, body, nil)
}

// SetPriority writes the routing priority (higher wins; 0 removes it). Live, persisted.
func (c *Client) SetPriority(ctx context.Context, name string, priority int) error {
	body := map[string]any{"name": name, "priority": priority}
	return c.do(ctx, http.MethodPatch, "/credentials/fields", nil, body, nil)
}

// APICallRequest asks CPA to call a provider URL with the credential's own token
// substituted for $TOKEN$. This is how CPA's panel reads quota.
type APICallRequest struct {
	AuthIndex string            `json:"auth_index"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Header    map[string]string `json:"header"`
	Data      string            `json:"data,omitempty"`
}

type APICallResponse struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header"`
	Body       string              `json:"body"`
}

func (c *Client) APICall(ctx context.Context, req APICallRequest) (APICallResponse, error) {
	var out APICallResponse
	err := c.do(ctx, http.MethodPost, "/requests/api-call", nil, req, &out)
	return out, err
}

type OAuthStart struct {
	Status    string `json:"status"`
	URL       string `json:"url"`
	State     string `json:"state"`
	Flow      string `json:"flow,omitempty"`
	UserCode  string `json:"user_code,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"`
}

// StartOAuth begins a login. is_webui makes CPA run the localhost callback forwarder.
func (c *Client) StartOAuth(ctx context.Context, provider string) (OAuthStart, error) {
	var out OAuthStart
	q := url.Values{"provider": {provider}, "is_webui": {"true"}}
	err := c.do(ctx, http.MethodGet, "/oauth/auth-url", q, nil, &out)
	return out, err
}

type OAuthStatus struct {
	Status string `json:"status"` // wait | ok | error
	Error  string `json:"error,omitempty"`
}

func (c *Client) OAuthStatus(ctx context.Context, state string) (OAuthStatus, error) {
	var out OAuthStatus
	err := c.do(ctx, http.MethodGet, "/oauth/status", url.Values{"state": {state}}, nil, &out)
	return out, err
}
