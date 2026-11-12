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
	s.getHistory(w, httptest.NewRequest("GET", "/api/history?range=today&account=today", nil))
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
		c := cpa.Credential{Name: provider + "-account", Provider: provider}
		s.creds = append(s.creds, c)
		for _, at := range []time.Time{now.Add(-5 * time.Minute), now} {
			if err := s.History.Append(history.Record{Account: history.AccountKey(c, quota.Result{}), Provider: provider, At: at, Plan: "Pro", Windows: []quota.Window{{Kind: "5h", Left: float64(i) / 2}}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		selection         string
		records, accounts int
	}{{"", 4, 2}, {"all", 4, 2}, {"claude", 2, 1}, {"codex", 2, 1}, {history.AccountKey(s.creds[1], quota.Result{}), 2, 1}} {
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

// TestShortDisableRecordsBreak preserves a pause that fits between quota polls.
func TestShortDisableRecordsBreak(t *testing.T) {
	for _, action := range []string{"disable", "pause", "external"} {
		t.Run(action, func(t *testing.T) {
			s, _, _ := testServer(t)
			c := s.creds[0]
			var disabled atomic.Bool
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPatch {
					var body struct{ Disabled bool }
					json.NewDecoder(r.Body).Decode(&body)
					disabled.Store(body.Disabled)
					w.Write([]byte(`{}`))
					return
				}
				current := c
				current.Disabled = disabled.Load()
				json.NewEncoder(w).Encode(map[string]any{"files": []cpa.Credential{current}})
			}))
			defer fake.Close()
			key := filepath.Join(t.TempDir(), "key")
			os.WriteFile(key, []byte("fixture-only"), 0600)
			s.cpa = cpa.New(fake.URL, key)
			s.quotaAt = time.Now() // Keep credential reads from scheduling a provider sweep.
			reset := time.Now().Add(time.Hour)
			reading := quota.Result{At: time.Now().Add(-time.Minute), Plan: "Pro", Windows: []quota.Window{{ID: "five_hour", Kind: "5h", Left: .8, ResetAt: &reset}}}
			s.recordQuota(c, reading)
			var err error
			switch action {
			case "disable":
				err = s.setDisabled(context.Background(), c.Name, true)
			case "pause":
				err = s.pause(context.Background(), c.Name, time.Now().Add(time.Minute))
			case "external":
				disabled.Store(true)
				s.refreshCreds(context.Background())
			}
			if err != nil {
				t.Fatal(err)
			}
			s.recordQuota(c, reading) // A fetch finishing while disabled must not fill the gap.
			if err := s.setDisabled(context.Background(), c.Name, false); err != nil {
				t.Fatal(err)
			}
			reading.At = time.Now()
			reading.Windows[0].Left = .6
			s.recordQuota(c, reading)
			rows, err := s.History.Read(time.Now())
			if err != nil || len(rows) != 3 || rows[1].Failure != "account_disabled" || rows[1].Account != rows[0].Account || len(rows[1].Windows) != 0 {
				t.Fatalf("short disable did not leave a gap: rows=%+v err=%v", rows, err)
			}
		})
	}
}

// TestRemovedAccountStaysOutsideCurrentPool keeps saved history without blocking live totals.
func TestRemovedAccountStaysOutsideCurrentPool(t *testing.T) {
	s, _, _ := testServer(t)
	s.recordQuota(s.creds[0], quota.Result{At: time.Now(), Plan: "Pro"})
	if err := s.History.Append(history.Record{Account: "removed", Provider: "claude", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"all", "claude", "removed"} {
		w := httptest.NewRecorder()
		s.getHistory(w, httptest.NewRequest("GET", "/api/history?account="+key, nil))
		var got struct {
			Accounts []historyAccount
			Selected []historyAccount
			Records  []history.Record
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Accounts) != 2 || len(got.Selected) != 1 || len(got.Records) != 1 || (got.Selected[0].Key == "removed") != (key == "removed") {
			t.Fatalf("selection %s includes removed account in pool: %+v", key, got)
		}
	}
}

type blockedHistoryWriter struct {
	*httptest.ResponseRecorder
	writing chan struct{}
	release chan struct{}
}

// Write waits for the test to simulate a history client that has stopped reading.
func (w *blockedHistoryWriter) Write(p []byte) (int, error) {
	close(w.writing)
	<-w.release
	return w.ResponseRecorder.Write(p)
}

// TestHistoryDownloadDoesNotBlockRecording separates network writes from the recorder lock.
func TestHistoryDownloadDoesNotBlockRecording(t *testing.T) {
	s, _, _ := testServer(t)
	w := &blockedHistoryWriter{httptest.NewRecorder(), make(chan struct{}), make(chan struct{})}
	done := make(chan struct{})
	go func() {
		s.getHistory(w, httptest.NewRequest("GET", "/api/history", nil))
		close(done)
	}()
	<-w.writing
	defer func() { close(w.release); <-done }()
	recorded := make(chan struct{})
	go func() {
		s.recordQuota(s.creds[0], quota.Result{At: time.Now()})
		close(recorded)
	}()
	select {
	case <-recorded:
	case <-time.After(time.Second):
		t.Fatal("slow download blocked quota recording")
	}
}

// TestDuplicateDisableKeepsSharedAccountFresh marks a gap only after every confirmed login is disabled.
func TestDuplicateDisableKeepsSharedAccountFresh(t *testing.T) {
	s, _, _ := testServer(t)
	first := s.creds[0]
	second := first
	second.Name = "duplicate.json"
	s.creds = append(s.creds, second)
	reading := quota.Result{At: time.Now(), AccountScope: "same-org", Plan: "Pro"}
	for _, c := range s.creds {
		s.quota[c.Name] = reading
		s.recordQuota(c, reading)
	}
	first.Disabled = true
	s.recordAvailability(first, nil)
	rows, err := s.History.Read(time.Now())
	if err != nil || len(rows) != 2 {
		t.Fatalf("one disabled login broke the shared account: rows=%+v err=%v", rows, err)
	}
	second.Disabled = true
	s.recordAvailability(second, nil)
	rows, err = s.History.Read(time.Now())
	if err != nil || len(rows) != 3 || rows[2].Failure != "account_disabled" || rows[2].Account != rows[0].Account {
		t.Fatalf("last disabled login did not leave a gap: rows=%+v err=%v", rows, err)
	}
}
