// Package state persists lastcall's own small state: pins and timed pauses.
// Nothing here holds credentials.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Pause records an account lastcall disabled on a timer.
// Resume only happens if the account is still disabled when the timer fires,
// so a manual change made elsewhere is never overridden.
type Pause struct {
	Name     string    `json:"name"`
	PausedAt time.Time `json:"paused_at"`
	ResumeAt time.Time `json:"resume_at"`
}

// Routing remembers what lastcall wrote so it can restore only its own changes.
// Original holds each credential's priority before lastcall first touched it.
type Routing struct {
	Auto     bool           `json:"auto"`
	Managed  []string       `json:"managed"`
	Original map[string]int `json:"original"`
	Written  map[string]int `json:"written"`
}

type Data struct {
	Pins    []string         `json:"pins"`
	Pauses  map[string]Pause `json:"pauses"`
	Routing Routing          `json:"routing"`
}

type Store struct {
	path string
	mu   sync.Mutex
	data Data
}

var defaultPins = []string{"claude", "codex"}

func fresh() Data {
	return Data{
		Pins:    defaultPins,
		Pauses:  map[string]Pause{},
		Routing: Routing{Managed: []string{"claude", "codex"}, Original: map[string]int{}, Written: map[string]int{}},
	}
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, data: fresh()}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, err
	}
	if s.data.Pauses == nil {
		s.data.Pauses = map[string]Pause{}
	}
	if s.data.Routing.Original == nil {
		s.data.Routing.Original = map[string]int{}
	}
	if s.data.Routing.Written == nil {
		s.data.Routing.Written = map[string]int{}
	}
	return s, nil
}

func (s *Store) Snapshot() Data {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.Marshal(s.data)
	var d Data
	_ = json.Unmarshal(raw, &d)
	return d
}

func (s *Store) Update(fn func(*Data)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.data)
	return s.save()
}

// save writes atomically: temp file in the same directory, then rename.
func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".state-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
