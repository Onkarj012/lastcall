package state

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRecordingPreference checks the default, persistence, and rollback after a failed write.
func TestRecordingPreference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s, err := Open(path)
	if err != nil || s.Snapshot().HistoryRecording {
		t.Fatal("recording must start off", err)
	}
	if err := s.Update(func(d *Data) { d.HistoryRecording = true; d.HistoryEpoch = "first" }); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil || !s.Snapshot().HistoryRecording || s.Snapshot().HistoryEpoch != "first" {
		t.Fatal("preference did not survive restart", err)
	}
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(blocked, "state.json")
	if err := s.Update(func(d *Data) { d.HistoryRecording = false; d.HistoryEpoch = "failed" }); err == nil {
		t.Fatal("expected save error")
	}
	if !s.Snapshot().HistoryRecording || s.Snapshot().HistoryEpoch != "first" {
		t.Fatal("failed setting took effect")
	}
}
