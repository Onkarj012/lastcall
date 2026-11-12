package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lastcall/internal/cpa"
	"lastcall/internal/history"
	"lastcall/internal/quota"
	"lastcall/internal/state"
	"lastcall/web"
)

// TestTodayHistory excludes yesterday's observations using the IST calendar day.
func TestTodayHistory(t *testing.T) {
	s, _, _ := testServer(t)
	s.creds = nil
	now := time.Now().In(time.FixedZone("IST", 19800))
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, at := range []time.Time{midnight.Add(-time.Minute), midnight} {
		if err := s.History.Append(history.Record{Account: "today", Provider: "claude", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	s.getHistory(w, httptest.NewRequest("GET", "/api/history?range=today", nil))
	var got struct{ Records []history.Record }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(got.Records) != 1 || !got.Records[0].At.Equal(midnight) {
		t.Fatal(w.Code, w.Body)
	}
}

// testServer supplies provider-shaped responses without touching a real account.
func testServer(t *testing.T) (*Server, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	var calls atomic.Int32
	var fail atomic.Bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req cpa.APICallRequest
		json.NewDecoder(r.Body).Decode(&req)
		body := `{"five_hour":{"utilization":40,"resets_at":"2026-11-11T00:00:00Z"},"seven_day":{"utilization":20,"resets_at":"2026-11-16T00:00:00Z"}}`
		if strings.Contains(req.URL, "profile") {
			body = `{"account":{"has_claude_pro":true},"organization":{"uuid":"test-org"}}`
		}
		status := 200
		if fail.Load() {
			status = 429
			body = "private upstream detail"
		}
		json.NewEncoder(w).Encode(cpa.APICallResponse{StatusCode: status, Body: body})
	}))
	t.Cleanup(fake.Close)
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	os.WriteFile(key, []byte("fixture-only"), 0600)
	st, err := state.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(fake.URL, cpa.New(fake.URL, key), st, 5*time.Minute)
	s.History = history.New(filepath.Join(dir, "history"))
	s.creds = []cpa.Credential{{Name: "fixture.json", AuthIndex: "1", Provider: "claude", Email: "fixture@example.com"}}
	return s, &calls, &fail
}

// TestCombinedHistory reads each unique account once without fetching provider data.
func TestCombinedHistory(t *testing.T) {
	s, calls, _ := testServer(t)
	now := time.Now().Add(-time.Minute)
	s.creds = nil
	for i, provider := range []string{"claude", "codex"} {
		for _, at := range []time.Time{now.Add(-5 * time.Minute), now} {
			if err := s.History.Append(history.Record{Account: provider + "-account", Provider: provider, At: at, Plan: "Pro", Windows: []quota.Window{{Kind: "5h", Left: float64(i) / 2}}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		selection         string
		records, accounts int
	}{{"", 4, 2}, {"all", 4, 2}, {"claude", 2, 1}, {"codex", 2, 1}, {"codex-account", 2, 1}} {
		w := httptest.NewRecorder()
		s.getHistory(w, httptest.NewRequest("GET", "/api/history?account="+tc.selection, nil))
		var got struct {
			Account  string
			Records  []history.Record
			Selected []historyAccount
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Records) != tc.records || len(got.Selected) != tc.accounts {
			t.Fatalf("selection %q: records=%d accounts=%d", tc.selection, len(got.Records), len(got.Selected))
		}
		if tc.selection == "" && got.Account != "all" {
			t.Fatal("combined history must be the default")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("viewing combined history fetched providers")
	}
}

// TestRecorderUsesFreshFetch ensures failed refreshes do not save cached successes.
func TestRecorderUsesFreshFetch(t *testing.T) {
	s, calls, fail := testServer(t)
	s.refreshQuota(context.Background())
	if calls.Load() != 2 {
		t.Fatal("unexpected provider calls", calls.Load())
	}
	rows, err := s.History.Read(time.Now())
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	s.refreshQuota(context.Background())
	s.refreshQuota(context.Background())
	fail.Store(true)
	s.refreshQuota(context.Background())
	rows, err = s.History.Read(time.Now())
	if err != nil || len(rows) != 4 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if rows[0].Windows[0].Left != .6 || rows[1].Windows[0].Left != .6 {
		t.Fatal("unchanged samples missing")
	}
	if rows[3].Failure != "fetch_failed" || len(rows[3].Windows) != 0 {
		t.Fatal("saved cached success", rows[3])
	}
	if rows[3].Account != rows[0].Account {
		t.Fatal("failure detached from confirmed account")
	}
	if len(s.quota[s.creds[0].Name].Windows) == 0 {
		t.Fatal("live cache changed")
	}
	if calls.Load() != 8 {
		t.Fatal("recorder added provider calls", calls.Load())
	}
	before := len(rows)
	s.creds[0].Disabled = true
	s.refreshQuota(context.Background())
	rows, _ = s.History.Read(time.Now())
	if len(rows) != before || calls.Load() != 8 {
		t.Fatal("disabled credential polled")
	}
}

// TestHistoryEndpoints verifies history reads and ignores the removed recording preference.
func TestHistoryEndpoints(t *testing.T) {
	s, calls, _ := testServer(t)
	// Older builds saved false by default. It must not suppress automatic recording.
	legacyPath := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(legacyPath, []byte(`{"history_recording":false,"history_epoch":"old","pins":["claude"],"pauses":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := state.Open(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	s.st = legacy
	s.refreshQuota(context.Background())
	rows, err := s.History.Read(time.Now())
	if err != nil || len(rows) != 1 {
		t.Fatalf("automatic recording failed: rows=%d err=%v", len(rows), err)
	}
	h := s.Handler(web.FS)
	request := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	before := calls.Load()
	if w := request("GET", "/api/history?range=7d"); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if calls.Load() != before {
		t.Fatal("history read fetched provider")
	}
	if w := request("GET", "/api/history?range=forever"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/history"); w.Code != 200 || !strings.Contains(w.Body.String(), "Quota history") || strings.Contains(w.Body.String(), "history_recording") {
		t.Fatal(w.Code, w.Body)
	}
	if w := request("GET", "/pool.mjs"); w.Code != 200 || !strings.Contains(w.Body.String(), "export function buildPool") {
		t.Fatal("pool module is missing from the embedded UI", w.Code)
	}
	if w := request("POST", "/api/history/settings"); w.Code == 200 {
		t.Fatal("obsolete recording control still available")
	}
}

// TestManualResetFetchIsRecorded covers the refresh path outside quota sweeps.
func TestManualResetFetchIsRecorded(t *testing.T) {
	s, calls, _ := testServer(t)
	s.useReset(context.Background(), s.creds[0].Name)
	rows, err := s.History.Read(time.Now())
	if err != nil || len(rows) != 1 || calls.Load() != 2 {
		t.Fatalf("rows=%d calls=%d err=%v", len(rows), calls.Load(), err)
	}
}
