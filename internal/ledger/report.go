package ledger

import (
	"sort"
	"strings"
	"time"
)

type Totals struct {
	Requests   int     `json:"requests"`
	In         int64   `json:"in"`
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
	Out        int64   `json:"out"`
	Reasoning  int64   `json:"reasoning"`
	Cost       float64 `json:"cost"`
	Unpriced   int     `json:"unpriced"` // responses whose model has no known price
}

func (t *Totals) add(e Event) {
	t.Requests++
	t.In += e.In
	t.CacheRead += e.CacheRead
	t.CacheWrite += e.CacheWrite
	t.Out += e.Out
	t.Reasoning += e.Reasoning
	t.Cost += e.Cost
	if !e.Priced {
		t.Unpriced++
	}
}

type Row struct {
	Key      string `json:"key"`
	Source   string `json:"source,omitempty"`
	Sessions int    `json:"sessions"`
	Totals
	sess map[string]bool
}

type Bucket struct {
	Start  time.Time          `json:"start"`
	Total  float64            `json:"total"`
	Tokens int64              `json:"tokens"`
	By     map[string]float64 `json:"by"` // cost per source
}

type Session struct {
	ID      string    `json:"id"`
	Source  string    `json:"source"`
	Project string    `json:"project"`
	Model   string    `json:"model"` // most expensive model in the session
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Totals
	byModel map[string]float64
}

type Filter struct {
	Source, Model, Project string
}

type Report struct {
	Range      string      `json:"range"`
	Since      time.Time   `json:"since"`
	ScannedAt  time.Time   `json:"scanned_at"`
	Note       string      `json:"note,omitempty"`
	Totals     Totals      `json:"totals"`
	Sessions   int         `json:"sessions"`
	ActiveDays int         `json:"active_days"`
	Buckets    []Bucket    `json:"buckets"`
	BucketSize string      `json:"bucket_size"` // hour | day
	Models     []Row       `json:"models"`
	Sources    []Row       `json:"sources"`
	Projects   []Row       `json:"projects"`
	Efforts    []Row       `json:"efforts"`
	Clients    []Row       `json:"clients"`
	Hours      [24]float64 `json:"hours"`    // cost by local hour of day
	Weekdays   [7]float64  `json:"weekdays"` // cost by weekday, Sunday first
	Top        []Session   `json:"top_sessions"`
	Options    struct {
		Sources, Models, Projects []string
	} `json:"options"`
}

var Ranges = map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "60d": Horizon}

func (l *Ledger) Report(rangeKey string, f Filter) Report {
	span, ok := Ranges[rangeKey]
	if !ok {
		rangeKey, span = "7d", Ranges["7d"]
	}
	now := time.Now()
	since := now.Add(-span)
	all, scanned, note := l.Events(since)
	r := Report{Range: rangeKey, Since: since, ScannedAt: scanned, Note: note, BucketSize: "day"}

	// Filter options come from the unfiltered range so dropdowns don't empty themselves.
	opt := func(get func(Event) string) []string {
		m := map[string]bool{}
		for _, e := range all {
			if v := get(e); v != "" {
				m[v] = true
			}
		}
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	r.Options.Sources = opt(func(e Event) string { return e.Source })
	r.Options.Models = opt(func(e Event) string { return e.Model })
	r.Options.Projects = opt(func(e Event) string { return short(e.Project) })

	step := 24 * time.Hour
	start := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, time.Local)
	if span <= 24*time.Hour {
		step, r.BucketSize = time.Hour, "hour"
		start = since.Truncate(time.Hour)
	}
	for t := start; !t.After(now); t = t.Add(step) {
		r.Buckets = append(r.Buckets, Bucket{Start: t, By: map[string]float64{}})
	}

	group := func() (map[string]*Row, func(string, string, Event)) {
		m := map[string]*Row{}
		return m, func(key, src string, e Event) {
			row := m[key]
			if row == nil {
				row = &Row{Key: key, Source: src, sess: map[string]bool{}}
				m[key] = row
			}
			row.add(e)
			row.sess[e.Session] = true
		}
	}
	models, addModel := group()
	sources, addSource := group()
	projects, addProject := group()
	efforts, addEffort := group()
	clients, addClient := group()
	sessions := map[string]*Session{}
	days := map[string]bool{}

	for _, e := range all {
		if (f.Source != "" && e.Source != f.Source) || (f.Model != "" && e.Model != f.Model) || (f.Project != "" && short(e.Project) != f.Project) {
			continue
		}
		r.Totals.add(e)
		addModel(e.Model, e.Source, e)
		addSource(e.Source, "", e)
		addProject(short(e.Project), "", e)
		addEffort(firstNonEmpty(e.Effort, "default"), "", e)
		addClient(e.Source+" · "+firstNonEmpty(e.Client, "unknown"), e.Source, e)
		local := e.At.Local()
		days[local.Format("2006-01-02")] = true
		r.Hours[local.Hour()] += e.Cost
		r.Weekdays[local.Weekday()] += e.Cost
		if i := bucketIndex(r.Buckets, local, step); i >= 0 {
			b := &r.Buckets[i]
			b.Total += e.Cost
			b.Tokens += e.In + e.CacheRead + e.CacheWrite + e.Out
			b.By[e.Source] += e.Cost
		}
		sk := e.Source + "|" + e.Session
		s := sessions[sk]
		if s == nil {
			s = &Session{ID: e.Session, Source: e.Source, Project: short(e.Project), Start: e.At, byModel: map[string]float64{}}
			sessions[sk] = s
		}
		s.add(e)
		s.End = e.At
		s.byModel[e.Model] += e.Cost + 1e-12
	}

	r.Models, r.Sources, r.Projects, r.Efforts, r.Clients = rows(models), rows(sources), rows(projects), rows(efforts), rows(clients)
	r.Sessions, r.ActiveDays = len(sessions), len(days)
	for _, s := range sessions {
		best := 0.0
		for m, c := range s.byModel {
			if c > best {
				best, s.Model = c, m
			}
		}
		r.Top = append(r.Top, *s)
	}
	sort.Slice(r.Top, func(i, j int) bool { return r.Top[i].Cost > r.Top[j].Cost })
	if len(r.Top) > 25 {
		r.Top = r.Top[:25]
	}
	return r
}

func bucketIndex(bs []Bucket, t time.Time, step time.Duration) int {
	for i := len(bs) - 1; i >= 0; i-- {
		if !t.Before(bs[i].Start) {
			if t.Before(bs[i].Start.Add(step)) {
				return i
			}
			return -1
		}
	}
	return -1
}

func rows(m map[string]*Row) []Row {
	out := make([]Row, 0, len(m))
	for _, r := range m {
		r.Sessions = len(r.sess)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cost > out[j].Cost })
	return out
}

// short turns /Users/me/Projects/market/meridian into market/meridian.
func short(p string) string {
	if p == "" {
		return "(none)"
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) <= 2 {
		return "~"
	}
	parts = parts[2:] // drop Users/<name>
	if len(parts) > 0 && parts[0] == "Projects" {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return "~"
	}
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}
