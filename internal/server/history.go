package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"lastcall/internal/cpa"
	"lastcall/internal/history"
	"lastcall/internal/quota"
	"lastcall/internal/state"
)

type historyAccount struct {
	Key      string `json:"key"`
	Provider string `json:"provider"`
	Label    string `json:"label"`
}

// fetchQuota captures the recording period before making the existing provider calls.
func (s *Server) fetchQuota(ctx context.Context, c cpa.Credential) quota.Result {
	s.historyMu.Lock()
	epoch := s.st.Snapshot().HistoryEpoch
	s.historyMu.Unlock()
	r := quota.Fetch(ctx, s.cpa, c)
	s.recordQuota(c, r, epoch)
	return r
}

// recordQuota saves the fresh result before live-card cache preservation runs.
func (s *Server) recordQuota(c cpa.Credential, r quota.Result, epoch string) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	p := quota.Provider(c.Provider)
	if s.History == nil || (p != "claude" && p != "codex") {
		return
	}
	alias := history.AccountKey(c, quota.Result{})
	d := s.st.Snapshot()
	if !d.HistoryRecording || d.HistoryEpoch != epoch {
		return
	}
	row := history.Record{Account: s.History.Key(c, r), Alias: alias, Provider: p, At: r.At, Epoch: d.HistoryEpoch, Every: int(s.quotaEvery.Seconds()), Plan: r.Plan}
	if r.Error != "" {
		row.Failure = "fetch_failed"
	} else {
		for _, w := range r.Windows {
			if w.Kind == "5h" || w.Kind == "week" {
				row.Windows = append(row.Windows, w)
			}
		}
	}
	_ = s.History.Append(row) // The History page reports persistence errors; quota cards stay usable.
}

// historyHandlers serves saved readings and the explicit recording preference.
func (s *Server) historyHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/history/settings", s.act(func(ctx context.Context, r *http.Request) (any, error) {
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1024)).Decode(&body); err != nil || body.Enabled == nil {
			return nil, badRequest("history needs {enabled: true|false}")
		}
		s.historyMu.Lock()
		defer s.historyMu.Unlock()
		if s.History == nil {
			return nil, badRequest("history storage is unavailable")
		}
		return nil, s.st.Update(func(d *state.Data) {
			if d.HistoryRecording != *body.Enabled {
				d.HistoryEpoch = time.Now().UTC().Format(time.RFC3339Nano)
			}
			d.HistoryRecording = *body.Enabled
		})
	}))
	mux.HandleFunc("GET /api/history", s.getHistory)
}

// getHistory returns one bounded time range and deduplicated account choices.
func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	if s.History == nil {
		writeErr(w, badRequest("history storage is unavailable"))
		return
	}
	hours := map[string]int{"": 24, "24h": 24, "7d": 168, "30d": 720}[r.URL.Query().Get("range")]
	if hours == 0 {
		writeErr(w, badRequest("range must be 24h, 7d, or 30d"))
		return
	}
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	now := time.Now()
	rows, err := s.History.Read(now)
	readError := ""
	if err != nil {
		readError = "History could not be read. Check the history files and permissions."
	}
	choices := []historyAccount{}
	seen := map[string]bool{}
	s.mu.Lock()
	for _, c := range s.creds {
		p := quota.Provider(c.Provider)
		if p != "claude" && p != "codex" {
			continue
		}
		result := s.quota[c.Name]
		key := s.History.Key(c, result)
		if !seen[key] {
			choices = append(choices, historyAccount{Key: key, Provider: p, Label: firstNonEmpty(c.Email, c.Label, strings.TrimSuffix(c.Name, ".json"))})
			seen[key] = true
		}
	}
	s.mu.Unlock()
	for _, row := range rows {
		if !seen[row.Account] {
			choices = append(choices, historyAccount{Key: row.Account, Provider: row.Provider, Label: "Saved account " + row.Account[:min(8, len(row.Account))]})
			seen[row.Account] = true
		}
	}
	key := r.URL.Query().Get("account")
	if key == "" && len(choices) > 0 {
		key = choices[0].Key
	}
	selected := []history.Record{}
	cutoff := now.Add(-time.Duration(hours) * time.Hour)
	for _, row := range rows {
		if row.Account == key && !row.At.Before(cutoff) {
			selected = append(selected, row)
		}
	}
	d := s.st.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{"enabled": d.HistoryRecording, "status": s.History.Status(), "read_error": readError, "accounts": choices, "account": key, "records": selected, "now": now, "retention_days": history.RetentionDays})
}
