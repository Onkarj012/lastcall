// Package ledger rebuilds a per-response usage history from the clients' own local logs
// (Claude Code transcripts, Codex rollouts, opencode's database) and prices each response
// at public API rates. CLIProxyAPI keeps no token history (its usage queue is off and holds
// 60s), so the clients are the only 60-day record. Traffic from other clients is not seen.
package ledger

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const Horizon = 60 * 24 * time.Hour

type Event struct {
	At         time.Time
	Source     string // claude-code | codex | opencode
	Client     string // entrypoint / originator / provider
	Model      string
	Effort     string
	Project    string
	Session    string
	In         int64 // uncached input
	CacheRead  int64
	CacheWrite int64
	Out        int64 // includes reasoning
	Reasoning  int64
	Cost       float64
	Priced     bool
	key        string // Claude message id + request id, for cross-file dedupe
}

type fileKey struct {
	size int64
	mod  time.Time
}

type Ledger struct {
	Home   string
	mu     sync.RWMutex
	files  map[string]fileKey
	byFile map[string][]Event
	oc     []Event
	ocErr  string
	at     time.Time
}

func New(home string) *Ledger {
	return &Ledger{Home: home, files: map[string]fileKey{}, byFile: map[string][]Event{}}
}

// Refresh re-parses only files that changed since the last pass.
func (l *Ledger) Refresh(ctx context.Context) {
	cut := time.Now().Add(-Horizon)
	seen := map[string]bool{}
	scan := func(root string, parse func(string) []Event) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || ctx.Err() != nil {
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.ModTime().Before(cut) {
				return nil
			}
			seen[p] = true
			k := fileKey{info.Size(), info.ModTime()}
			l.mu.RLock()
			old, ok := l.files[p]
			l.mu.RUnlock()
			if ok && old == k {
				return nil
			}
			evs := parse(p)
			l.mu.Lock()
			l.files[p], l.byFile[p] = k, evs
			l.mu.Unlock()
			return nil
		})
	}
	scan(filepath.Join(l.Home, ".claude/projects"), parseClaude)
	scan(filepath.Join(l.Home, ".codex/sessions"), parseCodex)
	oc, ocErr := readOpenCode(ctx, filepath.Join(l.Home, ".local/share/opencode/opencode.db"), cut)

	l.mu.Lock()
	for p := range l.files {
		if !seen[p] {
			delete(l.files, p)
			delete(l.byFile, p)
		}
	}
	l.oc, l.ocErr, l.at = oc, ocErr, time.Now()
	l.mu.Unlock()
}

// Events returns every event since `since`, deduplicated across files
// (Claude Code repeats a response in parent and subagent transcripts).
func (l *Ledger) Events(since time.Time) ([]Event, time.Time, string) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []Event
	seen := map[string]bool{}
	for _, evs := range l.byFile {
		for _, e := range evs {
			if e.At.Before(since) || (e.key != "" && seen[e.key]) {
				continue
			}
			if e.key != "" {
				seen[e.key] = true
			}
			out = append(out, e)
		}
	}
	for _, e := range l.oc {
		if !e.At.Before(since) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, l.at, l.ocErr
}

func price(e *Event) {
	if p, ok := lookup(e.Model); ok {
		e.Cost = p.cost(e.In, e.CacheRead, e.CacheWrite, 0, e.Out)
		e.Priced = true
	}
}

// eachLine streams a JSONL file, handing over only lines containing marker.
func eachLine(path string, marker []byte, fn func([]byte)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && bytes.Contains(line, marker) {
			fn(line)
		}
		if err == io.EOF || err != nil {
			return
		}
	}
}

// ---- Claude Code: ~/.claude/projects/**/*.jsonl ----

type claudeLine struct {
	Type       string    `json:"type"`
	Timestamp  time.Time `json:"timestamp"`
	SessionID  string    `json:"sessionId"`
	Cwd        string    `json:"cwd"`
	Effort     string    `json:"effort"`
	Entrypoint string    `json:"entrypoint"`
	RequestID  string    `json:"requestId"`
	Message    struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input         int64 `json:"input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			Output        int64 `json:"output_tokens"`
			Details       struct {
				Thinking int64 `json:"thinking_tokens"`
			} `json:"output_tokens_details"`
			Split *struct {
				H1 int64 `json:"ephemeral_1h_input_tokens"`
				M5 int64 `json:"ephemeral_5m_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

func parseClaude(path string) []Event {
	var out []Event
	seen := map[string]bool{}
	eachLine(path, []byte(`"usage"`), func(b []byte) {
		var d claudeLine
		if json.Unmarshal(b, &d) != nil || d.Type != "assistant" || d.Message.Usage == nil || d.Message.Model == "" || d.Message.Model == "<synthetic>" {
			return
		}
		// Streaming writes one line per content block with the same usage; count the response once.
		key := d.Message.ID + "|" + d.RequestID
		if seen[key] {
			return
		}
		seen[key] = true
		u := d.Message.Usage
		e := Event{
			At: d.Timestamp, Source: "claude-code", Client: d.Entrypoint, Model: d.Message.Model,
			Effort: d.Effort, Project: d.Cwd, Session: d.SessionID,
			In: u.Input, CacheRead: u.CacheRead, CacheWrite: u.CacheCreation, Out: u.Output, Reasoning: u.Details.Thinking,
			key: key,
		}
		if p, ok := lookup(e.Model); ok {
			w5, w1 := u.CacheCreation, int64(0)
			if u.Split != nil && u.Split.H1+u.Split.M5 > 0 {
				w5, w1 = u.Split.M5, u.Split.H1
			}
			e.Cost, e.Priced = p.cost(e.In, e.CacheRead, w5, w1, e.Out), true
		}
		out = append(out, e)
	})
	return out
}

// ---- Codex: ~/.codex/sessions/**/rollout-*.jsonl ----

type codexLine struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexUsage struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
	Total      int64 `json:"total_tokens"`
}

func parseCodex(path string) []Event {
	var out []Event
	var model, effort, cwd, session, client string
	var lastTotal int64 = -1
	eachLine(path, []byte(`"type"`), func(b []byte) {
		var d codexLine
		if json.Unmarshal(b, &d) != nil {
			return
		}
		switch d.Type {
		case "session_meta":
			var p struct {
				ID         string `json:"id"`
				Cwd        string `json:"cwd"`
				Originator string `json:"originator"`
			}
			if json.Unmarshal(d.Payload, &p) == nil {
				session, cwd, client = p.ID, p.Cwd, p.Originator
			}
		case "turn_context":
			var p struct {
				Model  string `json:"model"`
				Effort string `json:"effort"`
				Cwd    string `json:"cwd"`
			}
			if json.Unmarshal(d.Payload, &p) == nil {
				model, effort = firstNonEmpty(p.Model, model), firstNonEmpty(p.Effort, effort)
				cwd = firstNonEmpty(p.Cwd, cwd)
			}
		case "event_msg":
			var p struct {
				Type string `json:"type"`
				Info *struct {
					Total codexUsage `json:"total_token_usage"`
					Last  codexUsage `json:"last_token_usage"`
				} `json:"info"`
			}
			if json.Unmarshal(d.Payload, &p) != nil || p.Type != "token_count" || p.Info == nil {
				return
			}
			// token_count repeats when nothing new was billed; only count increases.
			if p.Info.Total.Total == lastTotal {
				return
			}
			lastTotal = p.Info.Total.Total
			u := p.Info.Last
			e := Event{
				At: d.Timestamp, Source: "codex", Client: client, Model: model, Effort: effort, Project: cwd,
				Session: firstNonEmpty(session, filepath.Base(path)),
				In:      max(0, u.Input-u.Cached), CacheRead: u.Cached, CacheWrite: u.CacheWrite, Out: u.Output, Reasoning: u.Reasoning,
			}
			price(&e)
			out = append(out, e)
		}
	})
	return out
}

// ---- opencode: ~/.local/share/opencode/opencode.db ----

func readOpenCode(ctx context.Context, db string, since time.Time) ([]Event, string) {
	if _, err := os.Stat(db); err != nil {
		return nil, ""
	}
	q := fmt.Sprintf(`select session_id s, time_created t,
		json_extract(data,'$.providerID') p, json_extract(data,'$.modelID') m, json_extract(data,'$.cost') c,
		json_extract(data,'$.path.cwd') d, json_extract(data,'$.variant') v,
		json_extract(data,'$.tokens.input') i, json_extract(data,'$.tokens.output') o,
		json_extract(data,'$.tokens.reasoning') r, json_extract(data,'$.tokens.cache.read') cr,
		json_extract(data,'$.tokens.cache.write') cw
		from message where time_created > %d and json_extract(data,'$.role') = 'assistant'`, since.UnixMilli())
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(cctx, "sqlite3", "-readonly", "-json", "file:"+db+"?mode=ro", q).Output()
	if err != nil {
		return nil, fmt.Sprintf("opencode db: %v", err)
	}
	var rows []struct {
		S          string `json:"s"`
		T          int64  `json:"t"`
		P, M, D, V string
		C          float64 `json:"c"`
		I, O, R    int64
		CR         int64 `json:"cr"`
		CW         int64 `json:"cw"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Sprintf("opencode rows: %v", err)
		}
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		if r.I+r.O+r.CR+r.CW == 0 {
			continue
		}
		e := Event{
			At: time.UnixMilli(r.T), Source: "opencode", Client: r.P, Model: r.M, Effort: r.V, Project: r.D, Session: r.S,
			In: r.I, CacheRead: r.CR, CacheWrite: r.CW, Out: r.O + r.R, Reasoning: r.R,
		}
		// opencode prices each response itself (Go/Zen bill at these API rates); trust it, else our table.
		if r.C > 0 {
			e.Cost, e.Priced = r.C, true
		} else {
			price(&e)
		}
		out = append(out, e)
	}
	return out, ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
