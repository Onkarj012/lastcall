// Package routing computes reset-first priorities: within each managed provider,
// the usable account whose weekly allowance resets soonest gets the highest priority,
// so allowance that would expire gets spent first. Pure; no I/O.
package routing

import (
	"sort"
	"time"

	"lastcall/internal/quota"
)

// Freshness limit: older quota freezes the pool rather than guessing.
const FreshFor = 15 * time.Minute

// Top priority value; ranks count down from here. Unusable accounts get Floor.
const (
	Top   = 100
	Floor = 1
)

type Account struct {
	Name        string
	Provider    string
	Disabled    bool
	Unavailable bool
	Quota       *quota.Result
}

type Slot struct {
	Name     string `json:"name"`
	Rank     int    `json:"rank"` // 1-based among usable; 0 when skipped
	Priority int    `json:"priority"`
	Reason   string `json:"reason"`
}

type Pool struct {
	Provider string `json:"provider"`
	Frozen   string `json:"frozen,omitempty"` // non-empty: why no ordering is proposed
	Slots    []Slot `json:"slots"`
}

func Plan(accts []Account, managed []string, now time.Time) []Pool {
	var pools []Pool
	for _, prov := range managed {
		var in []Account
		for _, a := range accts {
			if a.Provider == prov {
				in = append(in, a)
			}
		}
		if len(in) == 0 {
			continue
		}
		pools = append(pools, planPool(prov, in, now))
	}
	return pools
}

func planPool(prov string, in []Account, now time.Time) Pool {
	p := Pool{Provider: prov}
	type cand struct {
		a     Account
		reset time.Time
	}
	var usable []cand
	for _, a := range in {
		if a.Disabled {
			p.Slots = append(p.Slots, Slot{Name: a.Name, Priority: Floor, Reason: "disabled"})
			continue
		}
		q := a.Quota
		if q == nil || q.Error != "" || now.Sub(q.At) > FreshFor {
			p.Frozen = "quota for " + a.Name + " is missing or stale"
			return p
		}
		if reason := exhausted(q); reason != "" {
			p.Slots = append(p.Slots, Slot{Name: a.Name, Priority: Floor, Reason: reason})
			continue
		}
		if a.Unavailable {
			p.Slots = append(p.Slots, Slot{Name: a.Name, Priority: Floor, Reason: "cooling down in CPA"})
			continue
		}
		reset, ok := weeklyReset(q)
		if !ok {
			p.Frozen = "no weekly reset time for " + a.Name
			return p
		}
		usable = append(usable, cand{a, reset.Truncate(time.Minute)})
	}
	sort.SliceStable(usable, func(i, j int) bool {
		if !usable[i].reset.Equal(usable[j].reset) {
			return usable[i].reset.Before(usable[j].reset)
		}
		return usable[i].a.Name < usable[j].a.Name
	})
	ranked := make([]Slot, 0, len(usable))
	for i, c := range usable {
		reason := "weekly resets soonest"
		if i > 0 {
			reason = "next after earlier resets"
		}
		ranked = append(ranked, Slot{Name: c.a.Name, Rank: i + 1, Priority: Top - i, Reason: reason})
	}
	p.Slots = append(ranked, p.Slots...)
	return p
}

// exhausted names the first spent 5h or weekly window, if any.
func exhausted(q *quota.Result) string {
	for _, w := range q.Windows {
		if (w.Kind == "5h" || w.Kind == "week") && w.Left <= 0.005 {
			return w.Name + " spent"
		}
	}
	return ""
}

func weeklyReset(q *quota.Result) (time.Time, bool) {
	for _, w := range q.Windows {
		if w.Kind == "week" && w.ResetAt != nil {
			return *w.ResetAt, true
		}
	}
	return time.Time{}, false
}
