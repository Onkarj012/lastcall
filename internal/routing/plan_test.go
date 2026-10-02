package routing

import (
	"testing"
	"time"

	"lastcall/internal/quota"
)

func q(now time.Time, fiveLeft, weekLeft float64, weekIn time.Duration) *quota.Result {
	r := now.Add(weekIn)
	return &quota.Result{At: now, Windows: []quota.Window{
		{Name: "5-hour", Kind: "5h", Left: fiveLeft},
		{Name: "Weekly", Kind: "week", Left: weekLeft, ResetAt: &r},
	}}
}

func TestEarliestWeeklyResetFirst(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	pools := Plan([]Account{
		{Name: "b", Provider: "codex", Quota: q(now, .8, .8, 6*24*time.Hour)},
		{Name: "a", Provider: "codex", Quota: q(now, .7, .2, 14*time.Hour)},
		{Name: "c", Provider: "codex", Quota: q(now, 0, .9, time.Hour)},
		{Name: "d", Provider: "codex", Disabled: true},
	}, []string{"codex"}, now)
	got := pools[0]
	if got.Frozen != "" {
		t.Fatalf("unexpected freeze: %s", got.Frozen)
	}
	want := []struct {
		name string
		rank int
	}{{"a", 1}, {"b", 2}, {"c", 0}, {"d", 0}}
	for i, w := range want {
		if got.Slots[i].Name != w.name || got.Slots[i].Rank != w.rank {
			t.Fatalf("slot %d = %+v, want %s rank %d", i, got.Slots[i], w.name, w.rank)
		}
	}
}

func TestStaleQuotaFreezesPool(t *testing.T) {
	now := time.Now()
	old := q(now.Add(-time.Hour), 1, 1, 24*time.Hour)
	pools := Plan([]Account{{Name: "a", Provider: "claude", Quota: old}}, []string{"claude"}, now)
	if pools[0].Frozen == "" || len(pools[0].Slots) != 0 {
		t.Fatalf("want frozen pool, got %+v", pools[0])
	}
}

func TestMinuteTieKeepsNameOrder(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	pools := Plan([]Account{
		{Name: "z", Provider: "claude", Quota: q(now, 1, 1, 2*time.Hour+10*time.Second)},
		{Name: "y", Provider: "claude", Quota: q(now, 1, 1, 2*time.Hour+40*time.Second)},
	}, []string{"claude"}, now)
	if pools[0].Slots[0].Name != "y" {
		t.Fatalf("tie should fall back to name order, got %+v", pools[0].Slots)
	}
}
