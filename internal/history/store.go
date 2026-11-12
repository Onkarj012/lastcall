// Package history saves fresh quota observations without storing credentials.
package history

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lastcall/internal/cpa"
	"lastcall/internal/quota"
)

const RetentionDays = 30
const maxFileSize = 32 << 20

type Record struct {
	Version  int            `json:"v"`
	Alias    string         `json:"alias,omitempty"`
	Account  string         `json:"account"`
	Provider string         `json:"provider"`
	At       time.Time      `json:"at"`
	Epoch    string         `json:"epoch"`
	Every    int            `json:"every_s"`
	Plan     string         `json:"plan,omitempty"`
	Windows  []quota.Window `json:"windows,omitempty"`
	Failure  string         `json:"failure,omitempty"`
}

type Status struct {
	LastSaved time.Time `json:"last_saved"`
	Error     string    `json:"error,omitempty"`
}

type Store struct {
	mu     sync.Mutex
	dir    string
	status Status
	keys   map[string]string
}

// New keeps history beside the state file and creates nothing until the first write.
func New(dir string) *Store {
	s := &Store{dir: dir, keys: map[string]string{}}
	// Read failures are reported by the history endpoint and can clear after a file repair.
	_, _ = s.Read(time.Now())
	return s
}

// Key reuses a confirmed identity when a later profile call is unavailable.
func (s *Store) Key(c cpa.Credential, r quota.Result) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	alias := AccountKey(c, quota.Result{})
	key := AccountKey(c, r)
	if r.AccountScope == "" {
		if known := s.keys[alias]; known != "" {
			return known
		}
	}
	s.keys[alias] = key
	return key
}

// AccountKey groups confirmed accounts while keeping unknown identities separate.
func AccountKey(c cpa.Credential, r quota.Result) string {
	p := quota.Provider(c.Provider)
	identity := "credential\x00" + c.Name + "\x00" + c.ID + "\x00" + strings.ToLower(strings.TrimSpace(c.Email))
	if p == "codex" && c.IDToken != nil && c.IDToken.ChatGPTAccountID != "" {
		identity = "account\x00" + c.IDToken.ChatGPTAccountID
	} else if p == "claude" && strings.TrimSpace(c.Email) != "" {
		identity = "email\x00" + strings.ToLower(strings.TrimSpace(c.Email)) + "\x00" + c.ProjectID
		if r.AccountScope != "" {
			identity += "\x00" + r.AccountScope
		} else {
			// Without organization scope, do not merge separate credentials by email alone.
			identity += "\x00unconfirmed\x00" + c.Name
		}
	}
	sum := sha256.Sum256([]byte(p + "\x00" + identity))
	return hex.EncodeToString(sum[:])
}

// Status returns the latest persistence result without affecting live quota.
func (s *Store) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Expire enforces retention while the History page is closed.
func (s *Store) Expire(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prune(now); err != nil {
		s.status.Error = "Old history could not be removed. Check the history directory permissions."
	}
}

// Append serializes writes, repairs an interrupted tail, and flushes each observation.
func (s *Store) Append(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.append(r)
	if err != nil {
		s.status.Error = "History could not be saved. Check the history directory and disk space."
	} else {
		s.status.Error = ""
		s.status.LastSaved = r.At
	}
	return err
}

// append validates the existing file before appending one complete JSON line.
func (s *Store) append(r Record) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := s.prune(time.Now()); err != nil {
		return err
	}
	path := filepath.Join(s.dir, r.At.UTC().Format("2006-01-02")+".jsonl")
	_, valid, err := readFile(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(int64(valid)); err != nil {
		return err
	}
	if _, err := f.Seek(0, 2); err != nil {
		return err
	}
	r.Version = 1
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if valid+len(raw)+1 > maxFileSize {
		return errors.New("history file limit reached")
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// Read returns retained records in observation order, including failed polls.
func (s *Store) Read(now time.Time) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := []Record{}
	paths, err := filepath.Glob(filepath.Join(s.dir, "*.jsonl"))
	if err == nil {
		err = s.prune(now)
	}
	if err != nil {
		return nil, err
	}
	cutoff := now.AddDate(0, 0, -RetentionDays)
	for _, path := range paths {
		day, e := time.Parse("2006-01-02.jsonl", filepath.Base(path))
		if e != nil || day.Before(calendarCutoff(now)) {
			continue
		}
		records, _, e := readFile(path)
		if e != nil {
			return nil, e
		}
		for _, r := range records {
			if r.Alias != "" {
				s.keys[r.Alias] = r.Account
			}
			if r.At.After(s.status.LastSaved) {
				s.status.LastSaved = r.At
			}
			if !r.At.Before(cutoff) && !r.At.After(now) {
				rows = append(rows, r)
			}
		}
	}
	for i := range rows {
		if rows[i].Account == rows[i].Alias && s.keys[rows[i].Alias] != "" {
			rows[i].Account = s.keys[rows[i].Alias]
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].At.Before(rows[j].At) })
	return rows, nil
}

// readFile rejects corrupt complete lines and leaves an incomplete final line for recovery.
func readFile(path string) ([]Record, int, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if stat.Size() > maxFileSize {
		return nil, 0, errors.New("history file too large")
	}
	raw := make([]byte, stat.Size())
	if _, err := f.ReadAt(raw, 0); err != nil && len(raw) > 0 {
		return nil, 0, err
	}
	valid := bytes.LastIndexByte(raw, '\n') + 1
	rows := []Record{}
	for _, line := range bytes.Split(raw[:valid], []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, 0, fmt.Errorf("invalid history record: %w", err)
		}
		if r.Version != 1 || r.At.IsZero() || r.Account == "" {
			return nil, 0, errors.New("unsupported history record")
		}
		rows = append(rows, r)
	}
	return rows, valid, nil
}

// calendarCutoff retains at most thirty daily files, including today.
func calendarCutoff(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1-RetentionDays)
}

// prune expires only daily files owned by this recorder.
func (s *Store) prune(now time.Time) error {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		day, err := time.Parse("2006-01-02.jsonl", e.Name())
		if err == nil && !e.IsDir() && day.Before(calendarCutoff(now)) {
			if err := os.Remove(filepath.Join(s.dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
