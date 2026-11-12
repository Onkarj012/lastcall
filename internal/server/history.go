package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"lastcall/internal/cpa"
	"lastcall/internal/history"
	"lastcall/internal/quota"
)

type historyAccount struct {
	Key      string `json:"key"`
	Provider string `json:"provider"`
	Label    string `json:"label"`
}

// fetchQuota saves each fresh provider result before the live quota cache is updated.
func (s *Server) fetchQuota(ctx context.Context, c cpa.Credential) quota.Result {
	r := quota.Fetch(ctx, s.cpa, c)
	s.recordQuota(c, r)
	return r
}

// recordQuota saves the fresh result before live-card cache preservation runs.
func (s *Server) recordQuota(c cpa.Credential, r quota.Result) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	p := quota.Provider(c.Provider)
	if s.History == nil || (p != "claude" && p != "codex") {
		return
	}
	alias := history.AccountKey(c, quota.Result{})
	row := history.Record{Account: s.History.Key(c, r), Alias: alias, Provider: p, At: r.At, Epoch: "automatic", Every: int(s.quotaEvery.Seconds()), Plan: r.Plan}
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

// getHistory returns one bounded time range and deduplicated account choices.
func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	if s.History == nil {
		writeErr(w, badRequest("history storage is unavailable"))
		return
	}
	hours := map[string]int{"": 24, "today": 24, "24h": 24, "7d": 168, "30d": 720}[r.URL.Query().Get("range")]
	if hours == 0 {
		writeErr(w, badRequest("range must be today, 24h, 7d, or 30d"))
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
	if key == "" {
		key = "all"
	}
	selectedAccounts := []historyAccount{}
	selectedKeys := map[string]bool{}
	for _, choice := range choices {
		if key == "all" || key == choice.Key || key == choice.Provider {
			selectedAccounts = append(selectedAccounts, choice)
			selectedKeys[choice.Key] = true
		}
	}
	selected := []history.Record{}
	cutoff := now.Add(-time.Duration(hours) * time.Hour)
	if r.URL.Query().Get("range") == "today" {
		local := now.In(time.FixedZone("IST", 19800))
		cutoff = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	}
	for _, row := range rows {
		if selectedKeys[row.Account] && !row.At.Before(cutoff) {
			selected = append(selected, row)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"quota_every_s": int(s.quotaEvery.Seconds()), "status": s.History.Status(), "read_error": readError, "accounts": choices, "selected": selectedAccounts, "account": key, "records": selected, "now": now, "retention_days": history.RetentionDays})
}
