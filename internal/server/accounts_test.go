package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lastcall/internal/cpa"
	"lastcall/internal/quota"
	"lastcall/internal/state"
)

type accountFixture struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	creds   []cpa.Credential
	status  string
	deleted []string
}

func accountTestServer(t *testing.T) (*Server, *accountFixture) {
	t.Helper()
	f := &accountFixture{status: "wait", creds: []cpa.Credential{{Name: "old.json", ID: "old", AuthIndex: "1", Provider: "test", Email: "old@example.com", UpdatedAt: "1"}}}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/requests/api-call") {
			var call cpa.APICallRequest
			json.NewDecoder(r.Body).Decode(&call)
			if strings.Contains(call.URL, "usage") && f.started != nil {
				f.once.Do(func() { close(f.started) })
				<-f.release
			}
			json.NewEncoder(w).Encode(cpa.APICallResponse{StatusCode: 200, Body: `{"five_hour":{"utilization":20}}`})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/credentials"):
			if r.Method == "DELETE" {
				name := r.URL.Query().Get("name")
				f.deleted = append(f.deleted, name)
				kept := []cpa.Credential{}
				for _, c := range f.creds {
					if c.Name != name {
						kept = append(kept, c)
					}
				}
				f.creds = kept
				json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"files": f.creds})
		case strings.HasSuffix(r.URL.Path, "/auth-url"):
			json.NewEncoder(w).Encode(cpa.OAuthStart{State: "session", URL: "https://example.com/login"})
		case strings.HasSuffix(r.URL.Path, "/status"):
			json.NewEncoder(w).Encode(cpa.OAuthStatus{Status: f.status})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.Close)
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	if err := os.WriteFile(key, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(fake.URL, cpa.New(fake.URL, key), st, time.Minute)
	s.refreshCreds(context.Background())
	// Wait for the initial unsupported-provider sweep to finish.
	s.quotaMu.Lock()
	s.quotaMu.Unlock()
	return s, f
}
func accountRequest(body string) *http.Request {
	return httptest.NewRequest("POST", "/", strings.NewReader(body))
}
func beginReplace(t *testing.T, s *Server) {
	t.Helper()
	if _, err := s.startAccountOAuth(context.Background(), accountRequest(fmt.Sprintf(`{"mode":"replace","name":"old.json","auth_index":"1","identity":%q}`, s.snapshot().Accounts[0].Identity))); err != nil {
		t.Fatal(err)
	}
}
func TestReplacementPreservesOldUntilBoundConfirmation(t *testing.T) {
	s, f := accountTestServer(t)
	beginReplace(t, s)
	f.mu.Lock()
	f.status = "error"
	f.mu.Unlock()
	if out, err := s.verifiedOAuth(context.Background(), "session"); err != nil || out.Status != "error" {
		t.Fatal(out, err)
	}
	f.mu.Lock()
	f.status = "ok"
	f.creds = append(f.creds, cpa.Credential{Name: "new.json", AuthIndex: "2", Provider: "test", Email: "new@example.com"})
	f.mu.Unlock()
	out, err := s.verifiedOAuth(context.Background(), "session")
	if err != nil || out.Status != "confirm" || out.Candidates[0].Label != "new@example.com" {
		t.Fatal(out, err)
	}
	if len(f.deleted) != 0 {
		t.Fatal("poll deleted old account")
	}
	if _, err := s.confirmAccountReplacement(context.Background(), accountRequest(`{"state":"session","name":"new.json","auth_index":"2"}`)); err == nil {
		t.Fatal("accepted wrong target")
	}
	s.mu.Lock()
	s.quota["old.json"] = quota.Result{Plan: "old"}
	s.mu.Unlock()
	s.st.Update(func(d *state.Data) { d.Pauses["old.json"] = state.Pause{Name: "old.json"} })
	f.mu.Lock()
	f.status = "error" // CPA can expire terminal status while the confirmation is open.
	f.mu.Unlock()
	if _, err := s.confirmAccountReplacement(context.Background(), accountRequest(`{"state":"session","name":"old.json","auth_index":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if len(f.deleted) != 1 || f.deleted[0] != "old.json" {
		t.Fatal(f.deleted)
	}
	s.mu.Lock()
	_, cached := s.quota["old.json"]
	s.mu.Unlock()
	if cached || len(s.st.Snapshot().Pauses) != 0 {
		t.Fatal("stale cache or pause")
	}
}
func TestReplacementRejectsUnverifiedCandidates(t *testing.T) {
	for _, kind := range []string{"missing", "ambiguous", "same", "changed-target", "removed-target", "runtime"} {
		t.Run(kind, func(t *testing.T) {
			s, f := accountTestServer(t)
			beginReplace(t, s)
			f.mu.Lock()
			f.status = "ok"
			candidate := cpa.Credential{Name: "new.json", AuthIndex: "2", Provider: "test", Email: "new@example.com"}
			switch kind {
			case "missing":
			case "same":
				candidate.Email = "old@example.com"
				f.creds = append(f.creds, candidate)
			case "changed-target":
				f.creds[0].Email = "changed@example.com"
				f.creds = append(f.creds, candidate)
			case "removed-target":
				f.creds = []cpa.Credential{candidate}
			case "runtime":
				candidate.RuntimeOnly = true
				f.creds = append(f.creds, candidate)
			case "ambiguous":
				f.creds = append(f.creds, candidate, cpa.Credential{Name: "other.json", Provider: "test", Email: "other@example.com"})
			}
			f.mu.Unlock()
			if _, err := s.verifiedOAuth(context.Background(), "session"); err == nil {
				t.Fatal("unsafe replacement accepted")
			}
			if len(f.deleted) != 0 {
				t.Fatal(f.deleted)
			}
		})
	}
}
func TestIdentityChangeInvalidatesInflightCacheAndPause(t *testing.T) {
	s, f := accountTestServer(t)
	old := s.creds[0]
	generation := s.generations[old.Name]
	s.mu.Lock()
	s.quota[old.Name] = quota.Result{Plan: "old"}
	s.mu.Unlock()
	s.st.Update(func(d *state.Data) { d.Pauses[old.Name] = state.Pause{Name: old.Name} })
	f.mu.Lock()
	f.creds[0].Email = "new@example.com"
	f.mu.Unlock()
	s.refreshCreds(context.Background())
	s.mu.Lock()
	valid := s.currentCredentialLocked(old, generation)
	_, cached := s.quota[old.Name]
	s.mu.Unlock()
	if valid || cached || len(s.st.Snapshot().Pauses) != 0 {
		t.Fatal("old identity survives")
	}
	req := accountRequest(fmt.Sprintf(`{"auth_index":"1","identity":%q}`, credentialVersion(old)))
	req.SetPathValue("name", old.Name)
	// The browser selection may have the same index, but the cached target must
	// still match the identity CPA lists immediately before deletion.
	s.mu.Lock()
	s.creds = []cpa.Credential{old}
	s.mu.Unlock()
	if _, err := s.removeAccount(context.Background(), req); err == nil {
		t.Fatal("removed changed target")
	}
	if len(f.deleted) != 0 {
		t.Fatal(f.deleted)
	}
}
func TestReconnectRejectsDifferentIdentity(t *testing.T) {
	s, f := accountTestServer(t)
	if _, err := s.startAccountOAuth(context.Background(), accountRequest(fmt.Sprintf(`{"mode":"reconnect","name":"old.json","auth_index":"1","identity":%q}`, s.snapshot().Accounts[0].Identity))); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.status = "ok"
	f.creds = append(f.creds, cpa.Credential{Name: "new.json", Provider: "test", Email: "new@example.com"})
	f.mu.Unlock()
	if _, err := s.verifiedOAuth(context.Background(), "session"); err == nil {
		t.Fatal("different identity accepted")
	}
}

func TestInflightFetchCannotRestoreRemovedCredential(t *testing.T) {
	s, f := accountTestServer(t)
	// Configure the fixture before starting any provider requests.
	s.quotaMu.Lock()
	f.started = make(chan struct{})
	f.release = make(chan struct{})
	f.mu.Lock()
	f.creds[0].Provider = "claude"
	old := f.creds[0]
	f.mu.Unlock()
	s.mu.Lock()
	s.creds = []cpa.Credential{old}
	s.mu.Unlock()
	s.quotaMu.Unlock()
	done := make(chan struct{})
	go func() { s.refreshQuota(context.Background()); close(done) }()
	select {
	case <-f.started:
	case <-time.After(3 * time.Second):
		t.Fatal("fetch did not start")
	}
	if err := s.deleteSelected(context.Background(), old, nil); err != nil {
		close(f.release)
		t.Fatal(err)
	}
	close(f.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("fetch did not finish")
	}
	s.mu.Lock()
	_, ok := s.quota[old.Name]
	s.mu.Unlock()
	if ok {
		t.Fatal("in-flight fetch restored removed credential")
	}
}

func TestRemovalRejectsIdentityChangedSinceDisplayedSnapshot(t *testing.T) {
	s, f := accountTestServer(t)
	selected := s.snapshot().Accounts[0]
	body, err := json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.creds[0].Email = "different@example.com"
	f.mu.Unlock()
	s.refreshCreds(context.Background())
	req := accountRequest(string(body))
	req.SetPathValue("name", selected.Name)
	if _, err := s.removeAccount(context.Background(), req); err == nil {
		t.Fatal("removed a different identity after the cache refreshed")
	}
	if len(f.deleted) != 0 {
		t.Fatal(f.deleted)
	}
}
