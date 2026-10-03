package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"lastcall/internal/cpa"
)

// ---- Claude: api.anthropic.com/api/oauth/usage (+ /profile for plan) ----

var claudeHeaders = map[string]string{
	"User-Agent":     "claude-cli/2.1.280 (external, cli)",
	"Authorization":  "Bearer $TOKEN$",
	"Content-Type":   "application/json",
	"anthropic-beta": "oauth-2025-04-20",
}

// Display order and labels for Claude's usage keys; null entries are skipped.
var claudeWindows = []struct{ key, name, kind string }{
	{"five_hour", "5-hour", "5h"},
	{"seven_day", "7-day", "week"},
	{"seven_day_opus", "7-day Opus", "other"},
	{"seven_day_sonnet", "7-day Sonnet", "other"},
	{"seven_day_oauth_apps", "7-day OAuth apps", "other"},
	{"seven_day_cowork", "7-day Cowork", "other"},
	{"iguana_necktie", "7-day Fable", "other"},
}

type claudeWin struct {
	Utilization json.RawMessage `json:"utilization"`
	ResetsAt    json.RawMessage `json:"resets_at"`
}

func claude(ctx context.Context, c Caller, cred cpa.Credential) (Result, error) {
	var usage map[string]json.RawMessage
	var profile struct {
		Account struct {
			HasMax bool `json:"has_claude_max"`
			HasPro bool `json:"has_claude_pro"`
		} `json:"account"`
		Organization struct {
			UUID   string `json:"uuid"`
			Type   string `json:"organization_type"`
			Status string `json:"subscription_status"`
		} `json:"organization"`
	}
	var wg sync.WaitGroup
	var profErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		profErr = call(ctx, c, cred.AuthIndex, "GET", "https://api.anthropic.com/api/oauth/profile", claudeHeaders, "", &profile)
	}()
	// cedar_ember=1 adds the banked-reset block, the same read Claude Code makes.
	err := call(ctx, c, cred.AuthIndex, "GET", "https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1", claudeHeaders, "", &usage)
	wg.Wait()
	if err != nil {
		return Result{}, err
	}

	var r Result
	if profErr == nil {
		switch {
		case profile.Organization.Type == "claude_team" && profile.Organization.Status == "active":
			r.Plan = "Team"
		case profile.Account.HasMax:
			r.Plan = "Max"
		case profile.Account.HasPro:
			r.Plan = "Pro"
		default:
			r.Plan = "Free"
		}
	}

	// A weekly_scoped Fable limit in limits[] replaces the iguana_necktie window.
	var limits []struct {
		Kind     string          `json:"kind"`
		Percent  json.RawMessage `json:"percent"`
		IsActive bool            `json:"is_active"`
		ResetsAt json.RawMessage `json:"resets_at"`
		Scope    struct {
			Model struct {
				DisplayName string `json:"display_name"`
			} `json:"model"`
		} `json:"scope"`
	}
	_ = json.Unmarshal(usage["limits"], &limits)
	var fable *Window
	for _, l := range limits {
		name := strings.ToLower(strings.TrimSpace(l.Scope.Model.DisplayName))
		used, ok := num(l.Percent)
		if l.Kind != "weekly_scoped" || (name != "fable" && name != "fable 5") || !ok {
			continue
		}
		w := Window{Name: "7-day " + l.Scope.Model.DisplayName, Kind: "other", Left: usedToLeft(used), ResetAt: instant(l.ResetsAt)}
		if fable == nil || l.IsActive {
			fable = &w
		}
		if l.IsActive {
			break
		}
	}

	for _, d := range claudeWindows {
		if d.key == "iguana_necktie" && fable != nil {
			r.Windows = append(r.Windows, *fable)
			continue
		}
		var w claudeWin
		if json.Unmarshal(usage[d.key], &w) != nil {
			continue
		}
		used, ok := num(w.Utilization)
		if !ok {
			continue
		}
		r.Windows = append(r.Windows, Window{Name: d.name, Kind: d.kind, Left: usedToLeft(used), ResetAt: instant(w.ResetsAt)})
	}
	if fable != nil && !hasWindow(r.Windows, fable.Name) {
		r.Windows = append(r.Windows, *fable)
	}
	r.Resets = claudeResets(usage["cedar_ember"], profile.Organization.UUID)
	if len(r.Windows) == 0 {
		return r, fmt.Errorf("no usage windows in response")
	}
	return r, nil
}

func hasWindow(ws []Window, name string) bool {
	for _, w := range ws {
		if w.Name == name {
			return true
		}
	}
	return false
}

// ---- Codex: chatgpt.com/backend-api/wham/usage ----

type codexWindow struct {
	UsedPercent       json.RawMessage `json:"used_percent"`
	LimitWindowSecs   json.RawMessage `json:"limit_window_seconds"`
	ResetAfterSeconds json.RawMessage `json:"reset_after_seconds"`
	ResetAt           json.RawMessage `json:"reset_at"`
}

type codexLimit struct {
	Allowed         *bool        `json:"allowed"`
	LimitReached    bool         `json:"limit_reached"`
	PrimaryWindow   *codexWindow `json:"primary_window"`
	SecondaryWindow *codexWindow `json:"secondary_window"`
	LimitName       string       `json:"limit_name"`
	MeteredFeature  string       `json:"metered_feature"`
}

func codex(ctx context.Context, c Caller, cred cpa.Credential) (Result, error) {
	h := codexHeaders(cred)
	var u struct {
		PlanType string `json:"plan_type"`
		Credits  *struct {
			HasCredits bool            `json:"has_credits"`
			Unlimited  bool            `json:"unlimited"`
			Balance    json.RawMessage `json:"balance"`
		} `json:"credits"`
		RateLimit            *codexLimit  `json:"rate_limit"`
		AdditionalRateLimits []codexLimit `json:"additional_rate_limits"`
		ResetCredits         *struct {
			AvailableCount json.RawMessage `json:"available_count"`
		} `json:"rate_limit_reset_credits"`
	}
	if err := call(ctx, c, cred.AuthIndex, "GET", "https://chatgpt.com/backend-api/wham/usage", h, "", &u); err != nil {
		return Result{}, err
	}

	r := Result{Plan: titleCase(firstNonEmpty(u.PlanType, planFromToken(cred)))}
	add := func(l *codexLimit, label string) {
		if l == nil {
			return
		}
		for i, w := range []*codexWindow{l.PrimaryWindow, l.SecondaryWindow} {
			if w == nil {
				continue
			}
			kind := "other"
			if sec, ok := num(w.LimitWindowSecs); ok {
				kind = kindForSeconds(sec)
			} else if i == 0 {
				kind = "5h"
			} else {
				kind = "week"
			}
			used, ok := num(w.UsedPercent)
			if !ok {
				if !l.LimitReached && (l.Allowed == nil || *l.Allowed) {
					continue
				}
				used = 100
			}
			reset := instant(w.ResetAt)
			if reset == nil {
				if secs, ok := num(w.ResetAfterSeconds); ok {
					t := time.Now().Add(time.Duration(secs * float64(time.Second)))
					reset = &t
				}
			}
			name := map[string]string{"5h": "5-hour", "week": "Weekly", "month": "Monthly"}[kind]
			if name == "" {
				name = "Window"
			}
			if label != "" {
				name = label + " · " + name
				kind = "other"
			}
			r.Windows = append(r.Windows, Window{Name: name, Kind: kind, Left: usedToLeft(used), ResetAt: reset})
		}
	}
	add(u.RateLimit, "")
	for i := range u.AdditionalRateLimits {
		l := &u.AdditionalRateLimits[i]
		add(l, firstNonEmpty(l.LimitName, l.MeteredFeature, "Extra"))
	}

	if u.Credits != nil {
		switch {
		case u.Credits.Unlimited:
			r.Meta = append(r.Meta, [2]string{"credits", "unlimited"})
		default:
			if b, ok := num(u.Credits.Balance); ok {
				r.Meta = append(r.Meta, [2]string{"credits", trimFloat(b)})
			}
		}
	}
	if u.ResetCredits != nil {
		if n, ok := num(u.ResetCredits.AvailableCount); ok && n > 0 {
			rs, err := codexResets(ctx, c, cred)
			if err != nil || rs == nil { // the count alone still tells you they exist
				rs = &Resets{Left: int(n), Usable: true, Clears: "5-hour + weekly"}
			}
			r.Resets = rs
		}
	}
	if cred.IDToken != nil && cred.IDToken.ActiveUntil != "" {
		if t := instant(json.RawMessage(`"` + cred.IDToken.ActiveUntil + `"`)); t != nil {
			r.Meta = append(r.Meta, [2]string{"renews", t.Local().Format("01/02")})
		}
	}
	if len(r.Windows) == 0 {
		return r, fmt.Errorf("no rate-limit windows in response")
	}
	return r, nil
}

func planFromToken(cred cpa.Credential) string {
	if cred.IDToken != nil {
		return cred.IDToken.PlanType
	}
	return ""
}

// ---- Antigravity: cloudcode-pa retrieveUserQuotaSummary ----

var antigravityHosts = []string{
	"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
}

func antigravity(ctx context.Context, c Caller, cred cpa.Credential) (Result, error) {
	if cred.ProjectID == "" {
		return Result{}, fmt.Errorf("credential list has no project_id for this account")
	}
	h := map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
		"User-Agent":    "antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)",
	}
	body, _ := json.Marshal(map[string]string{"project": cred.ProjectID})
	var out struct {
		Groups []struct {
			DisplayName string `json:"displayName"`
			Buckets     []struct {
				DisplayName       string          `json:"displayName"`
				Window            string          `json:"window"`
				ResetTime         json.RawMessage `json:"resetTime"`
				RemainingFraction json.RawMessage `json:"remainingFraction"`
			} `json:"buckets"`
		} `json:"groups"`
	}
	var lastErr error
	for _, u := range antigravityHosts {
		out.Groups = nil
		if lastErr = call(ctx, c, cred.AuthIndex, "POST", u, h, string(body), &out); lastErr == nil && len(out.Groups) > 0 {
			break
		}
	}
	if len(out.Groups) == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("no quota groups returned")
		}
		return Result{}, lastErr
	}
	var r Result
	for _, g := range out.Groups {
		for _, b := range g.Buckets {
			left, ok := num(b.RemainingFraction)
			if !ok {
				continue
			}
			if left > 1 { // "42%" style strings arrive as 42
				left /= 100
			}
			kind := "other"
			switch strings.ToLower(b.Window) {
			case "5h", "five-hour", "five_hour":
				kind = "5h"
			case "weekly", "week":
				kind = "week"
			}
			label := map[string]string{"5h": "5-hour", "week": "Weekly"}[kind]
			name := strings.TrimSpace(firstNonEmpty(g.DisplayName, b.DisplayName))
			if label != "" {
				name = label + " · " + name
			}
			r.Windows = append(r.Windows, Window{Name: name, Kind: kind, Left: clamp01(left), ResetAt: instant(b.ResetTime)})
		}
	}
	return r, nil
}

// ---- xAI / Grok: cli-chat-proxy.grok.com/v1/billing ----

var xaiHeaders = map[string]string{
	"Authorization":         "Bearer $TOKEN$",
	"x-xai-token-auth":      "xai-grok-cli",
	"x-grok-client-version": "0.2.91",
	"accept":                "*/*",
	"user-agent":            "grok-pager/0.2.91 grok-shell/0.2.91 (macos; aarch64)",
}

type xaiBilling struct {
	CurrentPeriod *struct {
		Type  string          `json:"type"`
		Start json.RawMessage `json:"start"`
		End   json.RawMessage `json:"end"`
	} `json:"currentPeriod"`
	CreditUsagePercent json.RawMessage `json:"creditUsagePercent"`
	ProductUsage       []struct {
		Product      string          `json:"product"`
		UsagePercent json.RawMessage `json:"usagePercent"`
	} `json:"productUsage"`
	MonthlyLimit     json.RawMessage `json:"monthlyLimit"`
	Used             json.RawMessage `json:"used"`
	OnDemandCap      json.RawMessage `json:"onDemandCap"`
	OnDemandUsed     json.RawMessage `json:"onDemandUsed"`
	BillingPeriodEnd json.RawMessage `json:"billingPeriodEnd"`
}

// decodeXai accepts the billing object at the root or wrapped in "config".
func decodeXai(raw map[string]json.RawMessage) xaiBilling {
	var b xaiBilling
	src := raw
	if inner, ok := raw["config"]; ok {
		var m map[string]json.RawMessage
		if json.Unmarshal(inner, &m) == nil {
			src = m
		}
	}
	j, _ := json.Marshal(src)
	_ = json.Unmarshal(j, &b)
	return b
}

func xai(ctx context.Context, c Caller, cred cpa.Credential) (Result, error) {
	var weeklyRaw, monthlyRaw map[string]json.RawMessage
	var wg sync.WaitGroup
	var mErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		mErr = call(ctx, c, cred.AuthIndex, "GET", "https://cli-chat-proxy.grok.com/v1/billing", xaiHeaders, "", &monthlyRaw)
	}()
	wErr := call(ctx, c, cred.AuthIndex, "GET", "https://cli-chat-proxy.grok.com/v1/billing?format=credits", xaiHeaders, "", &weeklyRaw)
	wg.Wait()
	if wErr != nil && mErr != nil {
		return Result{}, wErr
	}

	var r Result
	if wErr == nil {
		b := decodeXai(weeklyRaw)
		if b.CurrentPeriod != nil && strings.Contains(strings.ToLower(b.CurrentPeriod.Type), "week") {
			// xAI omits zero-valued fields (protobuf JSON), so a weekly period with no
			// creditUsagePercent means nothing used yet. Seen live 2026-10-02.
			used, _ := num(b.CreditUsagePercent)
			r.Windows = append(r.Windows, Window{Name: "Weekly", Kind: "week", Left: usedToLeft(used), ResetAt: instant(b.CurrentPeriod.End)})
		}
		for _, p := range b.ProductUsage {
			if used, ok := num(p.UsagePercent); ok && p.Product != "" {
				r.Windows = append(r.Windows, Window{Name: p.Product, Kind: "other", Left: usedToLeft(used)})
			}
		}
	}
	if mErr == nil {
		b := decodeXai(monthlyRaw)
		limit, okL := num(b.MonthlyLimit)
		used, okU := num(b.Used)
		if okL && okU && limit > 0 {
			r.Windows = append(r.Windows, Window{Name: "Monthly", Kind: "month", Left: clamp01(1 - minF(used, limit)/limit), ResetAt: instant(b.BillingPeriodEnd)})
			switch {
			case limit >= 150000:
				r.Plan = "SuperGrok Heavy"
			case limit >= 15000:
				r.Plan = "SuperGrok"
			}
		}
		if capV, ok := num(b.OnDemandCap); ok && capV > 0 {
			od, ok := num(b.OnDemandUsed)
			if !ok && okL && okU {
				od = maxF(0, used-limit)
			}
			r.Meta = append(r.Meta, [2]string{"pay as you go", fmt.Sprintf("%d%% used", int(100*od/capV))})
		} else {
			r.Meta = append(r.Meta, [2]string{"pay as you go", "off"})
		}
	}
	if len(r.Windows) == 0 {
		return r, fmt.Errorf("billing returned no usable windows")
	}
	return r, nil
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
