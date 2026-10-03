package quota

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"lastcall/internal/cpa"
)

// Resets is the banked usage-limit resets an account can spend on demand.
// Claude calls these grants (program "cedar_ember"); Codex calls them reset credits.
type Resets struct {
	Left   int         `json:"left"`
	Clears string      `json:"clears,omitempty"`
	Usable bool        `json:"usable"`
	Note   string      `json:"note,omitempty"` // why it can't be used right now
	Items  []ResetItem `json:"items"`          // soonest expiry first

	grant string // Claude: grant id to claim
	org   string // Claude: organization uuid the claim goes to
}

// ResetItem is one grant or credit. Claude grants can hold more than one reset.
type ResetItem struct {
	Title     string     `json:"title"`
	Count     int        `json:"count"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func sortItems(items []ResetItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].ExpiresAt, items[j].ExpiresAt
		return a != nil && (b == nil || a.Before(*b))
	})
}

// ---- Claude: cedar_ember block of /api/oauth/usage?cedar_ember=1 ----

type claudeResetStatus struct {
	Eligible      bool            `json:"eligible"`
	IneligibleWhy *string         `json:"ineligible_reason"`
	AtLimit       bool            `json:"at_limit"`
	NextGrantID   *string         `json:"next_grant_id"`
	CooldownUntil json.RawMessage `json:"cooldown_until"`
	Grants        []struct {
		ID               string          `json:"id"`
		Label            string          `json:"label"`
		ResetsLeft       int             `json:"resets_left"`
		EndsAt           json.RawMessage `json:"ends_at"`
		Clears           []string        `json:"clears"`
		Paused           bool            `json:"paused"`
		UsableNow        bool            `json:"usable_now"`
		UseRequiresLimit bool            `json:"use_requires_limit"`
	} `json:"grants"`
}

func claudeResets(raw json.RawMessage, org string) *Resets {
	var st claudeResetStatus
	if len(raw) == 0 || json.Unmarshal(raw, &st) != nil {
		return nil
	}
	r := &Resets{org: org}
	for _, g := range st.Grants {
		if g.ResetsLeft > 0 {
			r.Left += g.ResetsLeft
			r.Items = append(r.Items, ResetItem{Title: g.Label, Count: g.ResetsLeft, ExpiresAt: instant(g.EndsAt)})
		}
	}
	sortItems(r.Items)
	if !st.Eligible {
		if r.Left == 0 {
			return nil
		}
		r.Note = "not eligible"
		if st.IneligibleWhy != nil {
			r.Note += ": " + *st.IneligibleWhy
		}
		return r
	}
	for _, g := range st.Grants {
		if st.NextGrantID == nil || g.ID != *st.NextGrantID {
			continue
		}
		r.grant, r.Clears = g.ID, clearsText(g.Clears)
		switch {
		case g.Paused:
			r.Note = "grant paused"
		case g.UseRequiresLimit && !st.AtLimit:
			r.Note = "only usable once you hit a limit"
		case !g.UsableNow:
			r.Note = "not usable right now"
		default:
			r.Usable = true
		}
	}
	if t := instant(st.CooldownUntil); t != nil && t.After(time.Now()) {
		r.Usable, r.Note = false, "cooling down until "+t.Local().Format("Jan 2 15:04")
	}
	if r.Left == 0 {
		return nil
	}
	if r.grant == "" && r.Note == "" {
		r.Note = "no grant offered right now"
	}
	return r
}

func clearsText(keys []string) string {
	var out []string
	for _, k := range keys {
		switch k {
		case "five_hour":
			out = append(out, "5-hour")
		case "seven_day":
			out = append(out, "weekly")
		}
	}
	return strings.Join(out, " + ")
}

// ---- Codex: /wham/rate-limit-reset-credits ----

func codexHeaders(cred cpa.Credential) map[string]string {
	h := map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
		"User-Agent":    "codex-tui/0.160.0 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.160.0)",
	}
	if cred.IDToken != nil && cred.IDToken.ChatGPTAccountID != "" {
		h["Chatgpt-Account-Id"] = cred.IDToken.ChatGPTAccountID
	}
	return h
}

// codexResets lists the account's reset credits; the usage endpoint only carries the count.
func codexResets(ctx context.Context, c Caller, cred cpa.Credential) (*Resets, error) {
	var out struct {
		AvailableCount int `json:"available_count"`
		Credits        []struct {
			Status    string          `json:"status"`
			Supported *bool           `json:"is_supported_by_plan"`
			ExpiresAt json.RawMessage `json:"expires_at"`
			Title     string          `json:"title"`
		} `json:"credits"`
	}
	if err := call(ctx, c, cred.AuthIndex, "GET", "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits", codexHeaders(cred), "", &out); err != nil {
		return nil, err
	}
	if out.AvailableCount == 0 {
		return nil, nil
	}
	r := &Resets{Left: out.AvailableCount, Usable: true, Clears: "5-hour + weekly"}
	for _, cr := range out.Credits {
		if cr.Status != "available" || (cr.Supported != nil && !*cr.Supported) {
			continue
		}
		r.Items = append(r.Items, ResetItem{Title: cr.Title, Count: 1, ExpiresAt: instant(cr.ExpiresAt)})
	}
	sortItems(r.Items)
	return r, nil
}

// ---- spending one ----

// UseReset spends one banked reset on the account. It returns a short summary on
// success; a refusal (nothing to reset, cooldown, …) comes back as an error and,
// per both providers, does not use up the reset.
func UseReset(ctx context.Context, c Caller, cred cpa.Credential, r *Resets) (string, error) {
	if r == nil || r.Left == 0 {
		return "", errors.New("this account has no resets left")
	}
	id, err := requestID()
	if err != nil {
		return "", err
	}
	switch Provider(firstNonEmpty(cred.Provider, cred.Type)) {
	case "codex":
		var out struct {
			Code         string `json:"code"`
			WindowsReset int    `json:"windows_reset"`
		}
		body, _ := json.Marshal(map[string]string{"redeem_request_id": id})
		if err := call(ctx, c, cred.AuthIndex, "POST", "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume", codexHeaders(cred), string(body), &out); err != nil {
			return "", err
		}
		if out.Code != "reset" {
			return "", fmt.Errorf("codex: %s", strings.ReplaceAll(out.Code, "_", " "))
		}
		return fmt.Sprintf("Codex limits reset (%d windows)", out.WindowsReset), nil
	case "claude":
		if r.grant == "" || r.org == "" {
			return "", errors.New("claude: no reset grant available to claim")
		}
		var out struct {
			Result     string  `json:"result"`
			Reason     *string `json:"reason"`
			ResetsLeft *int    `json:"resets_left"`
		}
		body, _ := json.Marshal(map[string]string{"program": "cedar_ember", "grant_id": r.grant, "request_id": id})
		url := "https://api.anthropic.com/api/organizations/" + r.org + "/reset_rate_limits"
		if err := call(ctx, c, cred.AuthIndex, "POST", url, claudeHeaders, string(body), &out); err != nil {
			return "", err
		}
		if out.Result != "reset" {
			msg := "claude: " + strings.ReplaceAll(out.Result, "_", " ")
			if out.Reason != nil {
				msg += " (" + strings.ReplaceAll(*out.Reason, "_", " ") + ")"
			}
			return "", errors.New(msg)
		}
		return "Claude limits reset", nil
	}
	return "", errors.New("resets aren't supported for this provider")
}

// requestID makes a v4 UUID; both providers use it to make a retried claim idempotent.
func requestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
