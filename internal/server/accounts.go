package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"lastcall/internal/cpa"
	"lastcall/internal/state"
)

type oauthSession struct {
	Mode      string
	Provider  string
	Target    cpa.Credential
	Before    []cpa.Credential
	Expires   time.Time
	Completed bool
	Candidate *cpa.Credential
}

type accountCandidate struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	AuthIndex string `json:"auth_index"`
}

type accountOAuthStatus struct {
	Status     string             `json:"status"`
	Error      string             `json:"error,omitempty"`
	Mode       string             `json:"mode"`
	Candidates []accountCandidate `json:"candidates"`
}

func credentialIdentity(c cpa.Credential) string {
	id := ""
	if c.IDToken != nil {
		id = c.IDToken.ChatGPTAccountID
	}
	return strings.Join([]string{c.Name, c.ID, c.AuthIndex, strings.ToLower(firstNonEmpty(c.Provider, c.Type)), strings.ToLower(c.Email), c.ProjectID, id}, "\x00")
}

func credentialVersion(c cpa.Credential) string {
	sum := sha256.Sum256([]byte(credentialIdentity(c)))
	return hex.EncodeToString(sum[:])
}

func sameAccount(a, b cpa.Credential) bool {
	if a.IDToken != nil && b.IDToken != nil && a.IDToken.ChatGPTAccountID != "" && b.IDToken.ChatGPTAccountID != "" {
		return a.IDToken.ChatGPTAccountID == b.IDToken.ChatGPTAccountID
	}
	return a.Email != "" && strings.EqualFold(a.Email, b.Email)
}

func (s *Server) currentCredentialLocked(c cpa.Credential, generation uint64) bool {
	if s.generations[c.Name] != generation {
		return false
	}
	for _, live := range s.creds {
		if credentialIdentity(live) == credentialIdentity(c) {
			return true
		}
	}
	return false
}

func findCredential(creds []cpa.Credential, name string) (cpa.Credential, error) {
	var found []cpa.Credential
	for _, c := range creds {
		if c.Name == name {
			found = append(found, c)
		}
	}
	if len(found) != 1 || found[0].RuntimeOnly {
		return cpa.Credential{}, badRequest("account is missing, ambiguous, or runtime-only")
	}
	return found[0], nil
}

func (s *Server) startAccountOAuth(ctx context.Context, r *http.Request) (any, error) {
	var body struct {
		Provider  string `json:"provider"`
		Mode      string `json:"mode"`
		Name      string `json:"name"`
		AuthIndex string `json:"auth_index"`
		Identity  string `json:"identity"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		return nil, badRequest("oauth needs {provider, mode, name, auth_index}")
	}
	if body.Mode == "" {
		body.Mode = "add"
	}
	if body.Mode != "add" && body.Mode != "reconnect" && body.Mode != "replace" {
		return nil, badRequest("invalid oauth mode")
	}
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	now := time.Now()
	for key, sess := range s.sessions {
		if now.After(sess.Expires) {
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= 32 {
		return nil, badRequest("too many pending logins")
	}
	creds, err := s.cpa.Credentials(ctx)
	if err != nil {
		return nil, err
	}
	sess := &oauthSession{Mode: body.Mode, Provider: body.Provider, Before: creds, Expires: now.Add(10 * time.Minute)}
	if body.Mode != "add" {
		target, err := findCredential(creds, body.Name)
		if err != nil {
			return nil, err
		}
		if body.AuthIndex == "" || target.AuthIndex != body.AuthIndex || body.Identity != credentialVersion(target) {
			return nil, badRequest("selected account changed; refresh first")
		}
		if body.Mode == "replace" && target.Email == "" && (target.IDToken == nil || target.IDToken.ChatGPTAccountID == "") {
			return nil, badRequest("selected account identity cannot be verified for replacement")
		}
		sess.Target = target
		sess.Provider = firstNonEmpty(target.Provider, target.Type)
	}
	if sess.Provider == "" {
		return nil, badRequest("provider is required")
	}
	start, err := s.cpa.StartOAuth(ctx, sess.Provider)
	if err != nil {
		return nil, err
	}
	if start.State == "" {
		return nil, badRequest("CPA did not return a login state")
	}
	if start.ExpiresIn > 0 && start.ExpiresIn < 600 {
		sess.Expires = now.Add(time.Duration(start.ExpiresIn) * time.Second)
	}
	if s.sessions == nil {
		s.sessions = map[string]*oauthSession{}
	}
	s.sessions[start.State] = sess
	return start, nil
}

func (s *Server) verifiedOAuth(ctx context.Context, key string) (accountOAuthStatus, error) {
	sess := s.sessions[key]
	if sess == nil || time.Now().After(sess.Expires) {
		delete(s.sessions, key)
		return accountOAuthStatus{}, badRequest("login session expired or unknown")
	}
	out := accountOAuthStatus{Mode: sess.Mode, Status: "wait", Candidates: []accountCandidate{}}
	if !sess.Completed {
		status, err := s.cpa.OAuthStatus(ctx, key)
		if err != nil {
			return out, err
		}
		out.Status, out.Error = status.Status, status.Error
		if status.Status != "ok" {
			return out, nil
		}
		sess.Completed = true
	}
	out.Status = "ok"
	creds, err := s.cpa.Credentials(ctx)
	if err != nil {
		return out, err
	}
	if sess.Mode != "add" {
		target, err := findCredential(creds, sess.Target.Name)
		if err != nil || credentialIdentity(target) != credentialIdentity(sess.Target) {
			return out, badRequest("selected account changed or was removed")
		}
	}
	candidates := []cpa.Credential{}
	for _, c := range creds {
		if c.RuntimeOnly || !strings.EqualFold(firstNonEmpty(c.Provider, c.Type), sess.Provider) {
			continue
		}
		changed := true
		for _, old := range sess.Before {
			if credentialIdentity(old) == credentialIdentity(c) && old.UpdatedAt == c.UpdatedAt && old.ModTime == c.ModTime {
				changed = false
				break
			}
		}
		if changed {
			candidates = append(candidates, c)
		}
	}
	sess.Candidate = nil
	for _, c := range candidates {
		out.Candidates = append(out.Candidates, accountCandidate{c.Name, firstNonEmpty(c.Email, c.Label, strings.TrimSuffix(c.Name, ".json")), c.AuthIndex})
	}
	if len(candidates) != 1 {
		return out, badRequest("login did not identify exactly one new or updated account")
	}
	c := candidates[0]
	if sess.Mode == "reconnect" && (credentialIdentity(c) != credentialIdentity(sess.Target) || !sameAccount(c, sess.Target)) {
		return out, badRequest("login used a different account; reconnect requires the selected identity")
	}
	if sess.Mode == "replace" {
		if c.Name == sess.Target.Name || sameAccount(c, sess.Target) {
			return out, badRequest("replacement must use a different account")
		}
		if c.Email == "" && (c.IDToken == nil || c.IDToken.ChatGPTAccountID == "") {
			return out, badRequest("replacement account identity could not be verified")
		}
		sess.Candidate = &c
		out.Status = "confirm"
	}
	return out, nil
}

func (s *Server) accountOAuthStatus(w http.ResponseWriter, r *http.Request) {
	s.oauthMu.Lock()
	out, err := s.verifiedOAuth(r.Context(), r.URL.Query().Get("state"))
	s.oauthMu.Unlock()
	if err != nil {
		writeErr(w, err)
		return
	}
	if out.Status == "ok" || out.Status == "confirm" {
		s.refreshCreds(r.Context())
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) confirmAccountReplacement(ctx context.Context, r *http.Request) (any, error) {
	var body struct {
		State     string `json:"state"`
		Name      string `json:"name"`
		AuthIndex string `json:"auth_index"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		return nil, badRequest("confirmation needs {state, name, auth_index}")
	}
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	previous := s.sessions[body.State]
	if previous == nil || previous.Candidate == nil {
		return nil, badRequest("poll and review the replacement before confirming")
	}
	reviewed := *previous.Candidate
	out, err := s.verifiedOAuth(ctx, body.State)
	if err != nil {
		return nil, err
	}
	sess := s.sessions[body.State]
	if out.Status != "confirm" || sess.Candidate == nil || credentialIdentity(*sess.Candidate) != credentialIdentity(reviewed) || sess.Target.Name != body.Name || sess.Target.AuthIndex != body.AuthIndex {
		return nil, badRequest("replacement confirmation does not match the selected account")
	}
	if err := s.deleteSelected(ctx, sess.Target, sess.Candidate); err != nil {
		return nil, err
	}
	delete(s.sessions, body.State)
	out.Status = "ok"
	s.refreshCreds(ctx)
	return out, nil
}

func (s *Server) deleteSelected(ctx context.Context, target cpa.Credential, candidate *cpa.Credential) error {
	s.accountsMu.Lock()
	defer s.accountsMu.Unlock()
	creds, err := s.cpa.Credentials(ctx)
	if err != nil {
		return err
	}
	live, err := findCredential(creds, target.Name)
	if err != nil {
		return err
	}
	if credentialIdentity(live) != credentialIdentity(target) {
		return badRequest("selected account changed; refresh first")
	}
	if candidate != nil {
		replacement, err := findCredential(creds, candidate.Name)
		if err != nil || credentialIdentity(replacement) != credentialIdentity(*candidate) {
			return badRequest("replacement account changed or was removed")
		}
	}
	if err := s.cpa.RemoveCredential(ctx, target.Name); err != nil {
		return err
	}
	s.mu.Lock()
	if s.generations == nil {
		s.generations = map[string]uint64{}
	}
	s.generations[target.Name]++
	delete(s.quota, target.Name)
	kept := s.creds[:0]
	for _, c := range s.creds {
		if c.Name != target.Name {
			kept = append(kept, c)
		}
	}
	s.creds = kept
	s.mu.Unlock()
	return s.st.Update(func(d *state.Data) { delete(d.Pauses, target.Name) })
}

func (s *Server) removeAccount(ctx context.Context, r *http.Request) (any, error) {
	var body struct {
		AuthIndex string `json:"auth_index"`
		Identity  string `json:"identity"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.AuthIndex == "" {
		return nil, badRequest("remove needs {auth_index}")
	}
	target, ok := s.credByName(r.PathValue("name"))
	if !ok || target.AuthIndex != body.AuthIndex || body.Identity != credentialVersion(target) {
		return nil, badRequest("selected account changed; refresh first")
	}
	if err := s.deleteSelected(ctx, target, nil); err != nil {
		return nil, err
	}
	s.refreshCreds(ctx)
	return nil, nil
}
