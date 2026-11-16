package history

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// peerSample creates a wire-compatible observation with a real account hash.
func peerSample(at time.Time) Record {
	r := sample()
	r.Version, r.Account, r.At = 1, strings.Repeat("a", 64), at
	return r
}

// TestMergeDayRetryAndConcurrentRecording preserves local data and makes retries idempotent.
func TestMergeDayRetryAndConcurrentRecording(t *testing.T) {
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	s := New(t.TempDir())
	r := peerSample(now)
	if err := s.Append(r); err != nil {
		t.Fatal(err)
	}
	remote := r
	remote.Plan = "remote"
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			local := r
			local.At = now.Add(-time.Duration(i+1) * time.Second)
			if err := s.Append(local); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if _, err := s.MergeDay(day, []Record{remote, r}, now); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	rows, err := New(s.dir).Read(now)
	if err != nil || len(rows) != 12 {
		t.Fatalf("got %d rows, error %v", len(rows), err)
	}
	info, err := os.Stat(filepath.Join(s.dir, day+".jsonl"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("history permissions: %v, %v", info, err)
	}
}

// TestMergeValidationAndRawIdentity prevents malformed imports and identity changes from rewriting exports.
func TestMergeValidationAndRawIdentity(t *testing.T) {
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	s := New(t.TempDir())
	r := peerSample(now)
	r.Alias = strings.Repeat("b", 64)
	older := r
	older.Account, older.At = r.Alias, now.Add(-time.Second)
	if _, err := s.MergeDay(day, []Record{r, older}, now); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Manifest(now)
	if _, err := s.Read(now); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Manifest(now)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("viewing history changed its fingerprint")
	}
	raw, _ := s.Day(day, now)
	if raw[0].Account != r.Alias {
		t.Fatal("export reconciled the original identity")
	}
	for _, change := range []func(*Record){
		func(r *Record) { r.At = now.AddDate(0, 0, -31) },
		func(r *Record) { r.At = now.Add(10 * time.Minute) },
		func(r *Record) { r.Version = 2 },
		func(r *Record) { r.Failure = "upstream secret body" },
		func(r *Record) { r.Account = "not-a-hash" },
		func(r *Record) { r.Windows[0].Left = 2 },
	} {
		bad := peerSample(now)
		change(&bad)
		if _, err := s.MergeDay(day, []Record{r, bad}, now); err == nil {
			t.Fatal("accepted invalid record")
		}
	}
	if _, err := s.MergeDay("../../outside", nil, now); err == nil {
		t.Fatal("accepted invalid day")
	}
	if _, err := s.MergeDay(now.AddDate(0, 0, -30).Format("2006-01-02"), nil, now); err == nil {
		t.Fatal("accepted expired day")
	}
	final, _ := s.Manifest(now)
	if !reflect.DeepEqual(before, final) {
		t.Fatal("rejected imports changed history")
	}
}
