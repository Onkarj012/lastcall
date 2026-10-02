// Package quota reads per-account limits the same way CPA's own panel does:
// through POST /requests/api-call, which calls each provider's usage API with the
// account's token. Field names and units follow CPAMC v1.25.2 (see docs/api-contracts.md).
package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"lastcall/internal/cpa"
)

// Window is one limit. Left is the remaining fraction, 0..1, whatever the provider reports.
type Window struct {
	Name    string     `json:"name"`
	Kind    string     `json:"kind"` // 5h | week | month | other
	Left    float64    `json:"left"`
	ResetAt *time.Time `json:"reset_at,omitempty"`
	// Dollar-metered windows (OpenCode Go) also carry the model and amounts.
	Model string  `json:"model,omitempty"`
	Cap   float64 `json:"cap,omitempty"`
	Spent float64 `json:"spent,omitempty"`
}

type Result struct {
	Plan    string      `json:"plan,omitempty"`
	Windows []Window    `json:"windows"`
	Meta    [][2]string `json:"meta,omitempty"`
	Error   string      `json:"error,omitempty"`
	At      time.Time   `json:"at"`
}

type Caller interface {
	APICall(ctx context.Context, req cpa.APICallRequest) (cpa.APICallResponse, error)
}

// Supported reports whether lastcall knows how to read this provider's quota.
func Supported(provider string) bool {
	switch Provider(provider) {
	case "claude", "codex", "antigravity", "xai":
		return true
	}
	return false
}

// Provider normalises CPA's provider names the way the panel does.
func Provider(p string) string {
	p = strings.ReplaceAll(strings.ToLower(p), "_", "-")
	switch p {
	case "x-ai", "grok":
		return "xai"
	case "anthropic":
		return "claude"
	}
	return p
}

func Fetch(ctx context.Context, c Caller, cred cpa.Credential) Result {
	p := Provider(firstNonEmpty(cred.Provider, cred.Type))
	var r Result
	var err error
	switch p {
	case "claude":
		r, err = claude(ctx, c, cred)
	case "codex":
		r, err = codex(ctx, c, cred)
	case "antigravity":
		r, err = antigravity(ctx, c, cred)
	case "xai":
		r, err = xai(ctx, c, cred)
	default:
		err = fmt.Errorf("quota not supported for %s", p)
	}
	if err != nil {
		r.Error = err.Error()
	}
	r.At = time.Now()
	return r
}

// call runs one proxied request and decodes the upstream JSON body.
func call(ctx context.Context, c Caller, authIndex, method, url string, header map[string]string, data string, out any) error {
	res, err := c.APICall(ctx, cpa.APICallRequest{AuthIndex: authIndex, Method: method, URL: url, Header: header, Data: data})
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body := res.Body
		if len(body) > 160 {
			body = body[:160] + "…"
		}
		return fmt.Errorf("upstream %d: %s", res.StatusCode, strings.TrimSpace(body))
	}
	if err := json.Unmarshal([]byte(res.Body), out); err != nil {
		return fmt.Errorf("decode upstream body: %w", err)
	}
	return nil
}

// usedToLeft turns a 0–100 "used" percent into a remaining fraction.
func usedToLeft(used float64) float64 { return clamp01(1 - used/100) }

func clamp01(x float64) float64 { return math.Max(0, math.Min(1, x)) }

// num accepts numbers, numeric strings, and {"val": n} wrappers (xAI money fields).
func num(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		s = strings.TrimSuffix(strings.TrimSpace(s), "%")
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v, true
		}
	}
	var w struct {
		Val json.RawMessage `json:"val"`
	}
	if json.Unmarshal(raw, &w) == nil && len(w.Val) > 0 {
		return num(w.Val)
	}
	return 0, false
}

// instant parses ISO-8601 or unix seconds/milliseconds (values < 1e11 are seconds).
func instant(raw json.RawMessage) *time.Time {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if t, err := time.Parse(layout, s); err == nil {
				return &t
			}
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return unix(f)
		}
		return nil
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil && f > 0 {
		return unix(f)
	}
	return nil
}

func unix(f float64) *time.Time {
	var t time.Time
	if f < 1e11 {
		t = time.Unix(int64(f), 0)
	} else {
		t = time.UnixMilli(int64(f))
	}
	return &t
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func kindForSeconds(sec float64) string {
	switch {
	case sec == 18000:
		return "5h"
	case sec == 604800:
		return "week"
	case sec >= 28*86400 && sec <= 31*86400:
		return "month"
	}
	return "other"
}

func titleCase(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func trimFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
