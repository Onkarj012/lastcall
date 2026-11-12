package history

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lastcall/internal/cpa"
	"lastcall/internal/quota"
)

// sample makes a complete observation for storage tests.
func sample() Record {
	return Record{Account: "test-account", Provider: "claude", At: time.Now().UTC(), Epoch: "on", Every: 300, Windows: []quota.Window{{ID: "five_hour", Kind: "5h", Left: .6}}}
}

// TestAppendRecovery preserves complete readings and repairs only an incomplete tail.
func TestAppendRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	s := New(dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("opening store created files")
	}
	r := sample()
	if err := s.Append(r); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, r.At.Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"v":`)
	f.Close()
	s = New(dir)
	if err := s.Append(r); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Read(time.Now())
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString("broken\n")
	f.Close()
	if err := s.Append(r); err == nil {
		t.Fatal("silently accepted corrupt complete record")
	}
	if s.Status().Error == "" {
		t.Fatal("write error not reported")
	}
}

// TestConcurrentWrites keeps unchanged readings and expires old daily files.
func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	old := filepath.Join(dir, time.Now().UTC().AddDate(0, 0, -31).Format("2006-01-02")+".jsonl")
	os.WriteFile(old, []byte("old"), 0600)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := s.Append(sample()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	rows, err := New(dir).Read(time.Now())
	if err != nil || len(rows) != 20 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old file retained")
	}
}

// TestAccountIdentity handles aliases, account replacements, and missing profile scope.
func TestAccountIdentity(t *testing.T) {
	c := cpa.Credential{Provider: "claude", Name: "one.json", Email: " USER@EXAMPLE.COM "}
	r := quota.Result{AccountScope: "organization-one"}
	key := AccountKey(c, r)
	c.Name = "renamed.json"
	c.Email = "user@example.com"
	if AccountKey(c, r) != key {
		t.Fatal("rename or email case split confirmed account")
	}
	c.Name = "duplicate.json"
	if AccountKey(c, r) != key {
		t.Fatal("duplicate login split confirmed account")
	}
	if AccountKey(c, quota.Result{AccountScope: "other-org"}) == key {
		t.Fatal("organizations merged")
	}
	if AccountKey(c, quota.Result{}) == key {
		t.Fatal("unconfirmed identity merged")
	}
	s := New(t.TempDir())
	alias := AccountKey(c, quota.Result{})
	row := sample()
	row.Account = key
	row.Alias = alias
	if err := s.Append(row); err != nil {
		t.Fatal(err)
	}
	s = New(s.dir)
	if s.Key(c, quota.Result{}) != key {
		t.Fatal("restart lost confirmed alias")
	}
	c.Email = "replacement@example.com"
	if s.Key(c, quota.Result{}) == key {
		t.Fatal("replacement inherited history")
	}
	c = cpa.Credential{Provider: "codex", Name: "one", IDToken: &cpa.CodexIDToken{ChatGPTAccountID: "id-one"}}
	key = AccountKey(c, quota.Result{})
	c.Name = "two"
	if AccountKey(c, quota.Result{}) != key {
		t.Fatal("Codex alias split")
	}
	c.IDToken.ChatGPTAccountID = "id-two"
	if AccountKey(c, quota.Result{}) == key {
		t.Fatal("Codex accounts merged")
	}
}

// TestWriteFailure leaves an actionable status without exposing filesystem details.
func TestWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	os.WriteFile(path, []byte("not a directory"), 0600)
	s := New(path)
	if err := s.Append(sample()); err == nil {
		t.Fatal("expected write failure")
	}
	if !strings.Contains(s.Status().Error, "could not be saved") {
		t.Fatal(s.Status())
	}
}

// TestConfirmedAliasReconcilesEarlierReadings keeps a recovered profile from adding a second account.
func TestConfirmedAliasReconcilesEarlierReadings(t *testing.T) {
	s := New(t.TempDir())
	c := cpa.Credential{Provider: "claude", Name: "one", Email: "user@example.com"}
	alias := AccountKey(c, quota.Result{})
	confirmed := AccountKey(c, quota.Result{AccountScope: "org"})
	for _, key := range []string{alias, confirmed} {
		r := sample()
		r.Account, r.Alias = key, alias
		if err := s.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := New(s.dir).Read(time.Now())
	if err != nil || len(rows) != 2 || rows[0].Account != confirmed || rows[1].Account != confirmed {
		t.Fatalf("alias split the account: rows=%+v err=%v", rows, err)
	}
}
