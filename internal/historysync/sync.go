// Package historysync exchanges quota history over an authenticated, separate peer listener.
package historysync

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"lastcall/internal/history"
)

// The JSON envelope adds fewer than 128 bytes to a canonical JSONL day.
const maxDayBytes = history.MaxDayBytes + 128

type Config struct {
	Listen    string `json:"listen"`
	Peer      string `json:"peer"`
	TokenFile string `json:"token_file"`
}

type Status struct {
	Enabled     bool      `json:"enabled"`
	LastSuccess time.Time `json:"last_success,omitzero"`
	Error       string    `json:"error,omitempty"`
}

type Sync struct {
	store  *history.Store
	config Config
	token  string
	client *http.Client
	mu     sync.Mutex
	status Status
}

type exchange struct {
	Day     string           `json:"day"`
	Records []history.Record `json:"records"`
}

// Open reads explicit sync settings and a shared token without changing service configuration.
func Open(path string, store *history.Store) (*Sync, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot read sync config")
	}
	defer f.Close()
	var cfg Config
	if err := decode(io.LimitReader(f, 8192), &cfg); err != nil {
		return nil, errors.New("invalid sync config; expected listen, peer, and token_file")
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil || port == "" || !privateHost(host, false) {
		return nil, errors.New("sync listen must use a loopback or Tailscale IP and port")
	}
	u, err := url.Parse(cfg.Peer)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !privateHost(u.Hostname(), true) {
		return nil, errors.New("sync peer must be http://<Tailscale IP or DNS name>:<port>")
	}
	if cfg.TokenFile == "" {
		return nil, errors.New("sync token_file is required")
	}
	raw, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		return nil, errors.New("cannot read sync token file")
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < 32 || len(token) > 256 || strings.ContainsAny(token, " \t\r\n") {
		return nil, errors.New("sync token must contain 32 to 256 characters without whitespace")
	}
	if err := store.EnableSync(); err != nil {
		return nil, errors.New("cannot initialize history recorder identity")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Peer tokens must never pass through an environment HTTP proxy.
	return &Sync{store: store, config: cfg, token: token, client: &http.Client{
		Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("sync redirects are disabled") },
	}, status: Status{Enabled: true}}, nil
}

// privateHost limits unencrypted HTTP to loopback or the encrypted Tailscale network.
func privateHost(host string, dns bool) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.IsLoopback() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(ip)
	}
	return dns && strings.HasSuffix(strings.ToLower(host), ".ts.net")
}

// Status reports sync health without exposing the peer token or network errors.
func (s *Sync) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Handler serves only history exchange, keeping the account-control API off the peer listener.
func (s *Sync) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/history/manifest", func(w http.ResponseWriter, r *http.Request) {
		manifest, err := s.store.Manifest(time.Now())
		if err != nil {
			http.Error(w, "history could not be read", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(manifest)
	})
	mux.HandleFunc("POST /v1/history/day", func(w http.ResponseWriter, r *http.Request) {
		var input exchange
		if err := decode(http.MaxBytesReader(w, r.Body, maxDayBytes), &input); err != nil {
			http.Error(w, "invalid day exchange", http.StatusBadRequest)
			return
		}
		rows, err := s.store.MergeDay(input.Day, input.Records, time.Now())
		if err != nil {
			http.Error(w, "history day could not be merged", http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(exchange{Day: input.Day, Records: rows})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// Run binds the peer listener and retries catch-up every minute until shutdown.
func (s *Sync) Run(ctx context.Context) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer s.client.CloseIdleConnections()
	defer func() {
		if err != nil {
			s.mu.Lock()
			s.status.Error = "Sync listener stopped. Check the configured Tailscale IP and port."
			s.mu.Unlock()
		}
	}()
	var listener net.Listener
	for {
		listener, err = net.Listen("tcp", s.config.Listen)
		if err == nil {
			break
		}
		s.mu.Lock()
		s.status.Error = "Sync is waiting for its Tailscale IP and port. Retrying every minute."
		s.mu.Unlock()
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	hs := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 8192}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.RunOnce(ctx)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = hs.Shutdown(shut)
				return
			case <-ticker.C:
				s.RunOnce(ctx)
			}
		}
	}()
	err = hs.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return errors.New("sync listener stopped")
	}
	<-done
	return nil
}

// RunOnce catches up both stores and keeps transient peer failures separate from recording health.
func (s *Sync) RunOnce(ctx context.Context) {
	err := s.exchange(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.status.Error = "Peer sync failed. Recording continues locally; retrying every minute."
	} else {
		s.status.Error = ""
		s.status.LastSuccess = time.Now()
	}
}

// exchange sends only differing days and accepts the peer's union without deleting local observations.
func (s *Sync) exchange(ctx context.Context) error {
	var remote map[string]string
	if err := s.request(ctx, "GET", "/v1/history/manifest", nil, &remote, 8192); err != nil {
		return err
	}
	now := time.Now()
	local, err := s.store.Manifest(now)
	if err != nil {
		return err
	}
	for day := range remote {
		if !history.RetainedDay(day, now) {
			return errors.New("invalid peer manifest")
		}
	}
	days := map[string]bool{}
	for day, hash := range local {
		if hash != remote[day] {
			days[day] = true
		}
	}
	for day, hash := range remote {
		if hash != local[day] {
			days[day] = true
		}
	}
	ordered := make([]string, 0, len(days))
	for day := range days {
		ordered = append(ordered, day)
	}
	sort.Strings(ordered)
	for _, day := range ordered {
		rows, err := s.store.Day(day, time.Now())
		if err != nil {
			return err
		}
		var reply exchange
		if err := s.request(ctx, "POST", "/v1/history/day", exchange{Day: day, Records: rows}, &reply, maxDayBytes); err != nil {
			return err
		}
		if reply.Day != day {
			return errors.New("unexpected history day")
		}
		if _, err := s.store.MergeDay(day, reply.Records, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

// request bounds peer responses and never includes response bodies or tokens in errors.
func (s *Sync) request(ctx context.Context, method, path string, input, output any, limit int64) error {
	var body bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			return err
		}
		if body.Len() > maxDayBytes {
			return errors.New("history exchange exceeds size limit")
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, s.config.Peer+path, &body)
	if err != nil {
		return errors.New("invalid sync request")
	}
	r.Header.Set("Authorization", "Bearer "+s.token)
	r.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(r)
	if err != nil {
		return errors.New("peer is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("peer returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return errors.New("peer response exceeds size limit or could not be read")
	}
	return decode(bytes.NewReader(raw), output)
}

// decode rejects unknown fields and trailing JSON before any imported data is written.
func decode(reader io.Reader, output any) error {
	d := json.NewDecoder(reader)
	d.DisallowUnknownFields()
	if err := d.Decode(output); err != nil {
		return err
	}
	if err := d.Decode(new(json.RawMessage)); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
