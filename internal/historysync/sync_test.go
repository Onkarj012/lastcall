package historysync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lastcall/internal/history"
)

// testSync opens the production config path with isolated history and a fixture token.
func testSync(t *testing.T, peer string) *Sync {
	t.Helper()
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte(strings.Repeat("fixture-", 8)), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(Config{Listen: "127.0.0.1:0", Peer: peer, TokenFile: token})
	path := filepath.Join(dir, "sync.json")
	if err := os.WriteFile(path, cfg, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, history.New(filepath.Join(dir, "history")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.client.CloseIdleConnections)
	return s
}

// seed saves distinct observations without involving CPA or provider accounts.
func seed(t *testing.T, s *Sync, at time.Time, plan string) {
	t.Helper()
	if err := s.store.Append(history.Record{Account: strings.Repeat("a", 64), Provider: "claude", At: at, Every: 300, Epoch: "automatic", Plan: plan}); err != nil {
		t.Fatal(err)
	}
}

// TestCatchUpOfflineRetryAndUnchangedDays proves two-way catch-up and bounded retry transfers.
func TestCatchUpOfflineRetryAndUnchangedDays(t *testing.T) {
	remote := testSync(t, "http://127.0.0.1:1")
	var offline atomic.Bool
	var transfers atomic.Int32
	handler := remote.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		if r.Method == "POST" {
			transfers.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	local := testSync(t, server.URL)
	now := time.Now().UTC()
	seed(t, local, now, "mac")
	seed(t, remote, now, "hp")
	seed(t, remote, now.AddDate(0, 0, -2), "hp-old")
	local.RunOnce(context.Background())
	if local.Status().Error != "" || local.Status().LastSuccess.IsZero() {
		t.Fatal(local.Status())
	}
	a, _ := local.store.Manifest(now)
	b, _ := remote.store.Manifest(now)
	if !reflect.DeepEqual(a, b) || transfers.Load() != 2 {
		t.Fatalf("stores differ or wrong number of transfers: %v %v %d", a, b, transfers.Load())
	}
	local.RunOnce(context.Background())
	if transfers.Load() != 2 {
		t.Fatal("unchanged history transferred again")
	}
	offline.Store(true)
	seed(t, local, now.Add(-time.Second), "mac-offline")
	local.RunOnce(context.Background())
	if local.Status().Error == "" {
		t.Fatal("offline peer not reported")
	}
	seed(t, remote, now.Add(-2*time.Second), "hp-offline")
	offline.Store(false)
	local.RunOnce(context.Background())
	rows, err := local.store.Read(now)
	if err != nil || len(rows) != 5 || local.Status().Error != "" {
		t.Fatalf("reconnect: rows=%d err=%v status=%v", len(rows), err, local.Status())
	}
	a, _ = local.store.Manifest(now)
	b, _ = remote.store.Manifest(now)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("reconnected stores did not converge")
	}
	local.RunOnce(context.Background())
	rows, _ = local.store.Read(now)
	if len(rows) != 5 {
		t.Fatal("retry duplicated observations")
	}
}

// TestPeerAuthenticationAndIsolation keeps credentials and account controls off the sync listener.
func TestPeerAuthenticationAndIsolation(t *testing.T) {
	s := testSync(t, "http://127.0.0.1:1")
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	for _, tc := range []struct {
		path, token, origin string
		status              int
	}{
		{"/v1/history/manifest", "", "", 401},
		{"/v1/history/manifest", "wrong", "", 401},
		{"/v1/history/manifest", s.token, "https://example.com", 401},
		{"/api/accounts/test/disable", s.token, "", 404},
		{"/api/snapshot", s.token, "", 404},
		{"/v1/history/manifest", s.token, "", 200},
	} {
		r, _ := http.NewRequest("GET", server.URL+tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("Origin", tc.origin)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("%s: got %d want %d", tc.path, response.StatusCode, tc.status)
		}
	}
	for _, body := range []string{`{"day":"../../escape","records":[]}`, `{"day":"2026-11-16","records":[],"credentials":"unexpected"}`, `{} {}`} {
		r, _ := http.NewRequest("POST", server.URL+"/v1/history/day", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+s.token)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode < 400 {
			t.Fatal("accepted malformed exchange")
		}
	}
}

// TestRedirectAndPublicHosts rejects tokens leaving the intended private peer.
func TestRedirectAndPublicHosts(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	s := testSync(t, redirect.URL)
	s.RunOnce(context.Background())
	if leaked.Load() || s.Status().Error == "" {
		t.Fatal("followed redirect or failed to report it")
	}
	for _, host := range []string{"0.0.0.0", "192.168.1.1", "example.com", "ts.net", "onkar-hp.ts.net.evil.com"} {
		if privateHost(host, true) {
			t.Fatalf("accepted public or non-Tailscale host %q", host)
		}
	}
}
