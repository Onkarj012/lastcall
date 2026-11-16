package history

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// EnableSync persists a recorder identity and scopes legacy local aliases before exchanging history.
func (s *Store) EnableSync() error {
	s.mu.Lock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		s.mu.Unlock()
		return err
	}
	path := filepath.Join(s.dir, ".recorder-id")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		id := make([]byte, 32)
		if _, err = rand.Read(id); err == nil {
			raw = []byte(hex.EncodeToString(id))
			var f *os.File
			f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err == nil {
				_, err = f.Write(raw)
				if err == nil {
					err = f.Sync()
				}
				if closeErr := f.Close(); err == nil {
					err = closeErr
				}
			}
		}
	}
	if err == nil && !validKey(string(raw)) {
		err = errors.New("invalid recorder identity")
	}
	if err == nil {
		s.origin = string(raw)
		s.keys = map[string]string{}
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	// Corrupt daily files remain a history error rather than blocking live quota cards.
	_, _ = s.Read(time.Now())
	return nil
}

// recorderAlias confines profile mappings to the recorder that observed them.
func recorderAlias(origin, alias string) string {
	return origin + "\x00" + alias
}

// unconfirmedKey separates unknown Claude organizations across recorders.
func unconfirmedKey(origin, alias string) string {
	if origin == "" {
		return alias
	}
	sum := sha256.Sum256([]byte(recorderAlias(origin, alias)))
	return hex.EncodeToString(sum[:])
}

// unconfirmed recognizes both legacy and recorder-scoped Claude fallback identities.
func unconfirmed(r Record) bool {
	return r.Provider == "claude" && r.Alias != "" && (r.Account == r.Alias || r.Account == unconfirmedKey(r.Origin, r.Alias))
}

// localOrigins attributes older source-less rows to this recorder without changing their account keys.
func (s *Store) localOrigins(rows []Record) []Record {
	for i := range rows {
		if rows[i].Origin == "" {
			rows[i].Origin = s.origin
		}
	}
	return rows
}

// Manifest fingerprints retained daily observations without reconciling their stored identities.
func (s *Store) Manifest(now time.Time) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths, err := filepath.Glob(filepath.Join(s.dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, path := range paths {
		day := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if !RetainedDay(day, now) {
			continue
		}
		rows, _, err := readFile(path)
		if err != nil {
			return nil, err
		}
		raw, err := encodeDay(s.localOrigins(rows))
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			continue
		}
		sum := sha256.Sum256(raw)
		out[day] = hex.EncodeToString(sum[:])
	}
	return out, nil
}

// RetainedDay accepts only exact UTC dates inside the existing retention window.
func RetainedDay(day string, now time.Time) bool {
	at, err := time.Parse("2006-01-02", day)
	return err == nil && at.Format("2006-01-02") == day && !at.Before(calendarCutoff(now)) && day <= now.UTC().Format("2006-01-02")
}

// Day returns stored observations unchanged so identity reconciliation cannot cause sync duplicates.
func (s *Store) Day(day string, now time.Time) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !RetainedDay(day, now) {
		return nil, errors.New("day is outside history retention")
	}
	rows, _, err := readFile(filepath.Join(s.dir, day+".jsonl"))
	return s.localOrigins(rows), err
}

// MergeDay atomically unions one retained day with peer observations while serializing local writes.
func (s *Store) MergeDay(day string, incoming []Record, now time.Time) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !RetainedDay(day, now) {
		return nil, errors.New("day is outside history retention")
	}
	for _, r := range incoming {
		if r.Version != 1 || s.origin != "" && r.Origin == "" || r.At.UTC().Format("2006-01-02") != day || r.At.After(now.Add(5*time.Minute)) || !validPeerRecord(r) {
			return nil, errors.New("invalid peer observation")
		}
	}
	path := filepath.Join(s.dir, day+".jsonl")
	rows, _, err := readFile(path)
	if err != nil {
		return nil, err
	}
	rows = s.localOrigins(rows)
	before, err := encodeDay(rows)
	if err != nil {
		return nil, err
	}
	raw, err := encodeDay(append(rows, incoming...))
	if err != nil {
		return nil, err
	}
	merged, err := decodeDay(raw)
	if err != nil {
		return nil, err
	}
	size, err := observationBytes(merged)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxFileSize || size > maxObservationBytes {
		return nil, errors.New("history file limit reached")
	}
	if !bytes.Equal(before, raw) {
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return nil, err
		}
		f, err := os.CreateTemp(s.dir, ".sync-*")
		if err != nil {
			return nil, err
		}
		defer os.Remove(f.Name())
		defer f.Close()
		// Keep an existing file's permissions when replacing its contents.
		if info, err := os.Stat(path); err == nil {
			if err := f.Chmod(info.Mode().Perm()); err != nil {
				return nil, err
			}
		}
		if _, err := f.Write(raw); err != nil {
			return nil, err
		}
		if err := f.Sync(); err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		if err := os.Rename(f.Name(), path); err != nil {
			return nil, err
		}
	}
	return merged, nil
}

// observationBytes counts quota data independently of the recorder IDs added to legacy rows.
func observationBytes(rows []Record) (int, error) {
	size := 0
	for _, r := range rows {
		r.Origin = ""
		raw, err := json.Marshal(r)
		if err != nil {
			return 0, err
		}
		size += len(raw) + 1
	}
	return size, nil
}

// validPeerRecord bounds peer metadata and admits only the fields used by quota history.
func validPeerRecord(r Record) bool {
	if !validKey(r.Account) || r.Origin != "" && !validKey(r.Origin) || r.Alias != "" && !validKey(r.Alias) || (r.Provider != "claude" && r.Provider != "codex") || r.At.IsZero() || r.Every < 0 || len(r.Epoch) > 128 || len(r.Plan) > 128 || len(r.Windows) > 8 {
		return false
	}
	if r.Failure != "" && r.Failure != "fetch_failed" && r.Failure != "account_disabled" {
		return false
	}
	for _, w := range r.Windows {
		if (w.Kind != "5h" && w.Kind != "week") || w.Left < 0 || w.Left > 1 || len(w.ID) > 128 || len(w.Name) > 128 {
			return false
		}
	}
	return true
}

// validKey accepts the account hashes produced by AccountKey.
func validKey(key string) bool {
	raw, err := hex.DecodeString(key)
	return err == nil && len(raw) == sha256.Size && key == hex.EncodeToString(raw)
}

// encodeDay sorts and deduplicates complete observations for stable fingerprints and retries.
func encodeDay(rows []Record) ([]byte, error) {
	type line struct {
		at  time.Time
		raw string
	}
	lines := make([]line, 0, len(rows))
	for _, r := range rows {
		r.At = r.At.UTC()
		r.Windows = append(r.Windows[:0:0], r.Windows...)
		for i := range r.Windows {
			if r.Windows[i].ResetAt != nil {
				at := r.Windows[i].ResetAt.UTC()
				r.Windows[i].ResetAt = &at
			}
		}
		raw, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		lines = append(lines, line{at: r.At, raw: string(raw)})
	}
	sort.Slice(lines, func(i, j int) bool {
		if !lines[i].at.Equal(lines[j].at) {
			return lines[i].at.Before(lines[j].at)
		}
		return lines[i].raw < lines[j].raw
	})
	var out bytes.Buffer
	for i, line := range lines {
		if i == 0 || line.raw != lines[i-1].raw {
			out.WriteString(line.raw)
			out.WriteByte('\n')
		}
	}
	return out.Bytes(), nil
}

// decodeDay restores observations in timestamp order after a canonical union.
func decodeDay(raw []byte) ([]Record, error) {
	rows := []Record{}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].At.Before(rows[j].At) })
	return rows, nil
}
