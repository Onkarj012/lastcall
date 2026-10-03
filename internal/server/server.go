// Package server is lastcall's backend: it polls CPA, keeps the latest snapshot,
// runs timed pauses, and serves the web UI.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"lastcall/internal/cpa"
	"lastcall/internal/quota"
	"lastcall/internal/state"
)

const (
	credsEvery   = 15 * time.Second
	pauseEvery   = 20 * time.Second
	minForcedGap = 30 * time.Second // Refresh button can't hammer provider usage APIs
	quotaWorkers = 4
)

type Server struct {
	cpaURL     string
	cpa        *cpa.Client
	st         *state.Store
	quotaEvery time.Duration

	mu          sync.Mutex
	creds       []cpa.Credential
	credsAt     time.Time
	credsErr    string
	quota       map[string]quota.Result // by credential name
	quotaAt     time.Time
	lastForced  time.Time
	refreshing  bool
	sweepQueued bool

	quotaMu sync.Mutex // one quota sweep at a time
}

func New(cpaURL string, c *cpa.Client, st *state.Store, quotaEvery time.Duration) *Server {
	return &Server{cpaURL: cpaURL, cpa: c, st: st, quotaEvery: quotaEvery, quota: map[string]quota.Result{}}
}

// Run starts the background loops and blocks until ctx ends.
func (s *Server) Run(ctx context.Context) {
	s.refreshCreds(ctx) // also starts the first quota sweep once accounts are listed
	credT := time.NewTicker(credsEvery)
	quotaT := time.NewTicker(s.quotaEvery)
	pauseT := time.NewTicker(pauseEvery)
	defer credT.Stop()
	defer quotaT.Stop()
	defer pauseT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-credT.C:
			s.refreshCreds(ctx)
		case <-quotaT.C:
			go s.refreshQuota(ctx)
		case <-pauseT.C:
			s.resumeDue(ctx)
		}
	}
}

func (s *Server) refreshCreds(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	creds, err := s.cpa.Credentials(cctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.credsErr = err.Error()
		return
	}
	sort.SliceStable(creds, func(i, j int) bool {
		pi, pj := quota.Provider(creds[i].Provider), quota.Provider(creds[j].Provider)
		if pi != pj {
			return pi < pj
		}
		return creds[i].Name < creds[j].Name
	})
	s.creds, s.credsAt, s.credsErr = creds, time.Now(), ""
	// First successful listing (e.g. the key file just appeared): don't wait for the timer.
	if s.quotaAt.IsZero() && !s.refreshing && len(creds) > 0 && !s.sweepQueued {
		s.sweepQueued = true
		go s.refreshQuota(context.WithoutCancel(ctx))
	}
}

// refreshQuota sweeps every supported, enabled account. Disabled accounts keep
// their last reading; there's no point spending a provider call on them.
func (s *Server) refreshQuota(ctx context.Context) {
	s.quotaMu.Lock()
	defer s.quotaMu.Unlock()
	s.mu.Lock()
	creds := append([]cpa.Credential(nil), s.creds...)
	s.refreshing = true
	s.sweepQueued = false
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.refreshing = false
		s.mu.Unlock()
	}()

	sem := make(chan struct{}, quotaWorkers)
	var wg sync.WaitGroup
	swept := 0
	for _, c := range creds {
		if c.Disabled || c.RuntimeOnly || !quota.Supported(c.Provider) {
			continue
		}
		swept++
		wg.Add(1)
		go func(c cpa.Credential) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			qctx, cancel := context.WithTimeout(ctx, 70*time.Second)
			defer cancel()
			r := quota.Fetch(qctx, s.cpa, c)
			s.mu.Lock()
			if r.Error != "" {
				// Keep the last good windows so a transient upstream error doesn't blank the card.
				if prev, ok := s.quota[c.Name]; ok && prev.Error == "" {
					prev.Error = r.Error
					r = prev
				}
			}
			s.quota[c.Name] = r
			s.mu.Unlock()
		}(c)
	}
	wg.Wait()
	if swept > 0 {
		s.mu.Lock()
		s.quotaAt = time.Now()
		s.mu.Unlock()
	}
}

func (s *Server) credByName(name string) (cpa.Credential, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.creds {
		if c.Name == name {
			return c, true
		}
	}
	return cpa.Credential{}, false
}

// ---- pauses ----

func (s *Server) pause(ctx context.Context, name string, until time.Time) error {
	c, ok := s.credByName(name)
	if !ok {
		return fmt.Errorf("unknown account %s", name)
	}
	if !until.After(time.Now()) {
		return errors.New("resume time must be in the future")
	}
	// Save the timer first: a disabled account with no saved timer would never come back.
	if err := s.st.Update(func(d *state.Data) {
		d.Pauses[name] = state.Pause{Name: name, PausedAt: time.Now(), ResumeAt: until}
	}); err != nil {
		return err
	}
	if err := s.cpa.SetDisabled(ctx, c.Name, c.AuthIndex, true); err != nil {
		_ = s.st.Update(func(d *state.Data) { delete(d.Pauses, name) })
		return err
	}
	s.refreshCreds(ctx)
	return nil
}

// resumeDue re-enables paused accounts whose time is up, but only if they're still
// disabled: if someone enabled or deleted the account elsewhere, the pause just ends.
func (s *Server) resumeDue(ctx context.Context) {
	now := time.Now()
	for name, p := range s.st.Snapshot().Pauses {
		if p.ResumeAt.After(now) {
			continue
		}
		s.refreshCreds(ctx)
		if c, ok := s.credByName(name); ok && c.Disabled {
			if err := s.cpa.SetDisabled(ctx, c.Name, c.AuthIndex, false); err != nil {
				log.Printf("resume %s: %v", name, err)
				continue // try again next tick
			}
		}
		_ = s.st.Update(func(d *state.Data) { delete(d.Pauses, name) })
		s.refreshCreds(ctx)
	}
}

func (s *Server) setDisabled(ctx context.Context, name string, disabled bool) error {
	c, ok := s.credByName(name)
	if !ok {
		return fmt.Errorf("unknown account %s", name)
	}
	if err := s.cpa.SetDisabled(ctx, c.Name, c.AuthIndex, disabled); err != nil {
		return err
	}
	// A manual on/off replaces any timed pause.
	if err := s.st.Update(func(d *state.Data) { delete(d.Pauses, name) }); err != nil {
		return err
	}
	s.refreshCreds(ctx)
	return nil
}

// ---- forced refresh ----

func (s *Server) forceRefresh(ctx context.Context) error {
	s.mu.Lock()
	if time.Since(s.lastForced) < minForcedGap {
		wait := minForcedGap - time.Since(s.lastForced)
		s.mu.Unlock()
		s.refreshCreds(ctx)
		return fmt.Errorf("quota was fetched moments ago; try again in %ds", int(wait.Seconds())+1)
	}
	s.lastForced = time.Now()
	s.mu.Unlock()
	s.refreshCreds(ctx)
	s.refreshQuota(ctx)
	return nil
}
