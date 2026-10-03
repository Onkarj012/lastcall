package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"time"

	"lastcall/internal/cpa"
	"lastcall/internal/quota"
	"lastcall/internal/state"
)

type accountView struct {
	Name           string        `json:"name"`
	AuthIndex      string        `json:"auth_index"`
	Provider       string        `json:"provider"`
	Label          string        `json:"label"`
	Plan           string        `json:"plan,omitempty"`
	Status         string        `json:"status"`
	StatusMessage  string        `json:"status_message,omitempty"`
	Disabled       bool          `json:"disabled"`
	Unavailable    bool          `json:"unavailable"`
	NextRetryAfter string        `json:"next_retry_after,omitempty"`
	Priority       int           `json:"priority"`
	Success        int64         `json:"success"`
	Failed         int64         `json:"failed"`
	Recent         []cpa.Bucket  `json:"recent"`
	Supported      bool          `json:"supported"`
	Quota          *quota.Result `json:"quota,omitempty"`
	Pause          *state.Pause  `json:"pause,omitempty"`
}

type snapshot struct {
	Now        time.Time     `json:"now"`
	CPAURL     string        `json:"cpa_url"`
	CredsAt    time.Time     `json:"creds_at"`
	QuotaAt    time.Time     `json:"quota_at"`
	QuotaEvery int           `json:"quota_every_s"`
	Refreshing bool          `json:"refreshing"`
	Error      string        `json:"error,omitempty"`
	Accounts   []accountView `json:"accounts"`
	Pins       []string      `json:"pins"`
}

func (s *Server) snapshot() snapshot {
	d := s.st.Snapshot()

	s.mu.Lock()
	defer s.mu.Unlock()
	errMsg := s.credsErr
	if p := s.cpa.AuthProblem(); p != "" {
		errMsg = p
	}
	out := snapshot{
		Accounts: []accountView{},
		CPAURL:   s.cpaURL,
		Now:      time.Now(), CredsAt: s.credsAt, QuotaAt: s.quotaAt, QuotaEvery: int(s.quotaEvery.Seconds()),
		Refreshing: s.refreshing, Error: errMsg, Pins: d.Pins,
	}
	for _, c := range s.creds {
		v := accountView{
			Name: c.Name, AuthIndex: c.AuthIndex, Provider: quota.Provider(firstNonEmpty(c.Provider, c.Type)),
			Label:  firstNonEmpty(c.Email, c.Label, strings.TrimSuffix(c.Name, ".json")),
			Status: c.Status, StatusMessage: c.StatusMessage, Disabled: c.Disabled, Unavailable: c.Unavailable,
			NextRetryAfter: c.NextRetryAfter, Priority: c.Priority, Success: c.Success, Failed: c.Failed,
			Recent: c.RecentRequests, Supported: quota.Supported(c.Provider),
		}
		if r, ok := s.quota[c.Name]; ok {
			r := r
			v.Quota = &r
			v.Plan = r.Plan
		}
		if v.Plan == "" && c.IDToken != nil && c.IDToken.PlanType != "" {
			v.Plan = strings.ToUpper(c.IDToken.PlanType[:1]) + c.IDToken.PlanType[1:]
		}
		if p, ok := d.Pauses[c.Name]; ok {
			p := p
			v.Pause = &p
		}
		out.Accounts = append(out.Accounts, v)
	}
	return out
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// Handler serves the API and the embedded UI.
func (s *Server) Handler(ui fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/snapshot", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, s.snapshot()) })

	mux.HandleFunc("POST /api/refresh", s.act(func(ctx context.Context, r *http.Request) (any, error) {
		return nil, s.forceRefresh(ctx)
	}))

	mux.HandleFunc("POST /api/accounts/{name}/{action}", s.act(func(ctx context.Context, r *http.Request) (any, error) {
		name, action := r.PathValue("name"), r.PathValue("action")
		switch action {
		case "disable":
			return nil, s.setDisabled(ctx, name, true)
		case "enable", "resume":
			return nil, s.setDisabled(ctx, name, false)
		case "reset":
			msg, err := s.useReset(ctx, name)
			return map[string]string{"message": msg}, err
		case "pause":
			var body struct {
				Minutes int    `json:"minutes"`
				Until   string `json:"until"` // "5h" | "week": that window's reset
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return nil, badRequest("pause needs {minutes} or {until}")
			}
			until, err := s.pauseUntil(name, body.Minutes, body.Until)
			if err != nil {
				return nil, err
			}
			return map[string]time.Time{"resume_at": until}, s.pause(ctx, name, until)
		}
		return nil, badRequest("unknown action " + action)
	}))

	mux.HandleFunc("POST /api/pins", s.act(func(ctx context.Context, r *http.Request) (any, error) {
		var body struct {
			Provider string `json:"provider"`
			Pinned   bool   `json:"pinned"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Provider == "" {
			return nil, badRequest("pins needs {provider, pinned}")
		}
		return nil, s.st.Update(func(d *state.Data) {
			d.Pins = slices.DeleteFunc(d.Pins, func(p string) bool { return p == body.Provider })
			if body.Pinned {
				d.Pins = append(d.Pins, body.Provider)
			}
		})
	}))

	mux.HandleFunc("POST /api/oauth/start", s.act(func(ctx context.Context, r *http.Request) (any, error) {
		var body struct {
			Provider string `json:"provider"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Provider == "" {
			return nil, badRequest("oauth needs {provider}")
		}
		return s.cpa.StartOAuth(ctx, body.Provider)
	}))
	mux.HandleFunc("GET /api/oauth/status", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.cpa.OAuthStatus(r.Context(), r.URL.Query().Get("state"))
		if err != nil {
			writeErr(w, err)
			return
		}
		if st.Status == "ok" {
			s.refreshCreds(r.Context())
		}
		writeJSON(w, http.StatusOK, st)
	})

	mux.Handle("GET /", http.FileServerFS(ui))
	return mux
}

func (s *Server) pauseUntil(name string, minutes int, until string) (time.Time, error) {
	if minutes > 0 {
		return time.Now().Add(time.Duration(minutes) * time.Minute), nil
	}
	kind := map[string]string{"5h": "5h", "week": "week"}[until]
	if kind == "" {
		return time.Time{}, badRequest("pause needs minutes > 0 or until 5h|week")
	}
	s.mu.Lock()
	r, ok := s.quota[name]
	s.mu.Unlock()
	if ok {
		for _, w := range r.Windows {
			if w.Kind == kind && w.ResetAt != nil && w.ResetAt.After(time.Now()) {
				return *w.ResetAt, nil
			}
		}
	}
	return time.Time{}, badRequest("no known " + until + " reset time for this account")
}

type badRequest string

func (b badRequest) Error() string { return string(b) }

// act wraps state-changing endpoints. The custom header can't be sent cross-site
// without a CORS preflight we never answer, so other websites can't drive these.
func (s *Server) act(fn func(context.Context, *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Lastcall") != "1" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing X-Lastcall header"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()
		out, err := fn(ctx, r)
		if err != nil {
			writeErr(w, err)
			return
		}
		if out == nil {
			out = map[string]string{"status": "ok"}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway
	var br badRequest
	var api *cpa.APIError
	switch {
	case errors.As(err, &br):
		code = http.StatusBadRequest
	case errors.Is(err, cpa.ErrAuth):
		code = http.StatusUnauthorized
	case errors.As(err, &api) && api.Status < 500:
		code = api.Status
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
