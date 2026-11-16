package history

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"lastcall/internal/cpa"
	"lastcall/internal/quota"
)

// peerSample creates a wire-compatible observation with a real account hash.
func peerSample(at time.Time) Record {
	r := sample()
	r.Version, r.Account, r.At = 1, strings.Repeat("a", 64), at
	return r
}

// TestEnableSyncPreservesHistoryErrors initializes identity without making corrupt history block startup.
func TestEnableSyncPreservesHistoryErrors(t *testing.T) {
	now := time.Now()
	s := New(t.TempDir())
	if err := os.WriteFile(filepath.Join(s.dir, now.UTC().Format("2006-01-02")+".jsonl"), []byte("corrupt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableSync(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(now); err == nil {
		t.Fatal("daily-file corruption was hidden")
	}
	if New(s.dir).origin != s.origin {
		t.Fatal("recorder identity was not retained")
	}
}

// TestRecorderAliasesSeparateOrganizations keeps legacy and new unknown readings with their own profile source.
func TestRecorderAliasesSeparateOrganizations(t *testing.T) {
	now := time.Now().UTC()
	c := cpa.Credential{Provider: "claude", Name: "same.json", Email: "same@example.com"}
	alias := AccountKey(c, quota.Result{})
	mac, hp := New(t.TempDir()), New(t.TempDir())
	for _, s := range []*Store{mac, hp} {
		r := peerSample(now)
		r.Account, r.Alias = alias, alias
		if err := s.Append(r); err != nil {
			t.Fatal(err)
		}
		if err := s.EnableSync(); err != nil {
			t.Fatal(err)
		}
	}
	macKey := mac.Key(c, quota.Result{AccountScope: "mac-org"})
	r := peerSample(now)
	r.Account, r.Alias = macKey, alias
	if err := mac.Append(r); err != nil {
		t.Fatal(err)
	}
	day := now.Format("2006-01-02")
	remote, _ := hp.Day(day, now)
	if _, err := mac.MergeDay(day, remote, now); err != nil {
		t.Fatal(err)
	}
	rows, err := mac.Read(now)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	unknownHP := hp.Key(c, quota.Result{})
	if unknownHP == macKey || unknownHP == alias {
		t.Fatal("HP fallback was not recorder-scoped")
	}
	for _, row := range rows {
		want := macKey
		if row.Origin == hp.origin {
			want = unknownHP
		}
		if row.Account != want {
			t.Fatalf("cross-machine profile reassignment: got %s want %s", row.Account, want)
		}
	}
	hpKey := hp.Key(c, quota.Result{AccountScope: "hp-org"})
	r.Account = hpKey
	if err := hp.Append(r); err != nil {
		t.Fatal(err)
	}
	remote, _ = hp.Day(day, now)
	if _, err := mac.MergeDay(day, remote, now); err != nil {
		t.Fatal(err)
	}
	restarted := New(mac.dir)
	rows, _ = restarted.Read(now)
	for _, row := range rows {
		want := macKey
		if row.Origin == hp.origin {
			want = hpKey
		}
		if row.Account != want {
			t.Fatal("restart mixed organizations")
		}
	}
	if restarted.Key(c, quota.Result{}) != macKey {
		t.Fatal("peer profile replaced the local fallback")
	}
	if hp.Key(c, quota.Result{AccountScope: "mac-org"}) != macKey {
		t.Fatal("confirmed account keys did not merge across recorders")
	}
}

// TestPeerIntervalsAcceptRecordedValues retains intervals already accepted by local quota recording.
func TestPeerIntervalsAcceptRecordedValues(t *testing.T) {
	now := time.Now().UTC()
	s := New(t.TempDir())
	for _, every := range []int{0, 90000, int(time.Duration(1<<63-1) / time.Second)} {
		r := peerSample(now)
		r.Every = every
		if _, err := s.MergeDay(now.Format("2006-01-02"), []Record{r}, now); err != nil {
			t.Fatalf("every=%d: %v", every, err)
		}
	}
	rows, _ := s.Read(now)
	if len(rows) != 3 {
		t.Fatal("accepted intervals lost readings")
	}
}

// TestMergeUnconfirmedAliasKeepsKnownAccount preserves a confirmed scope across peer gaps and restarts.
func TestMergeUnconfirmedAliasKeepsKnownAccount(t *testing.T) {
	now := time.Now().UTC()
	s := New(t.TempDir())
	c := cpa.Credential{Provider: "claude", Name: "shared.json", Email: "fixture@example.com"}
	alias := AccountKey(c, quota.Result{})
	confirmed := AccountKey(c, quota.Result{AccountScope: "confirmed-org"})
	r := peerSample(now)
	r.Account, r.Alias = confirmed, alias
	if err := s.Append(r); err != nil {
		t.Fatal(err)
	}
	r.Account, r.At = alias, now.Add(time.Second)
	day := r.At.Format("2006-01-02")
	if _, err := s.MergeDay(day, []Record{r}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.dir)
	rows, err := restarted.Read(now.Add(time.Second))
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	for _, row := range rows {
		if row.Account != confirmed {
			t.Fatal("peer observation split the confirmed account")
		}
	}
	if restarted.Key(c, quota.Result{}) != confirmed {
		t.Fatal("unconfirmed profile lost the confirmed account after restart")
	}
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
