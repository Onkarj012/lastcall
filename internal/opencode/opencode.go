// Package opencode estimates OpenCode Go quota from opencode's own local database.
// OpenCode has no usage API; its docs only point at the web console. opencode stores the
// cost of every response in ~/.local/share/opencode/opencode.db, and Go limits are
// per-model dollar caps over rolling 5-hour (20%), weekly (50%) and monthly (100%) windows.
// So: sum this Mac's spend per model per window and compare to the published caps.
// Usage from other machines is not visible here.
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"time"

	"lastcall/internal/quota"
)

// Monthly caps in USD per model, from opencode.ai/docs/go (verbatim, read 2026-10-03):
// "Each model has the following usage limits: 5-hour — 20% of the monthly limit;
// weekly — 50%; and monthly — 100%." Free models are unlimited and omitted.
var caps = map[string]map[string]float64{
	"go": {
		"glm-5.3-flash": 60, "glm-5.3": 15, "glm-5.2": 60, "kimi-k3": 15, "kimi-k2.7-code": 60, "kimi-k2.6": 60,
		"longcat-2.0": 60, "mimo-v2.6-flash": 60, "mimo-v2.6-pro": 15, "mimo-v2.5": 60, "mimo-v2.5-pro": 15,
		"minimax-m3": 60, "minimax-m2.7": 60, "muse-spark-1.3-contributor": 60, "muse-spark-1.2-contributor": 60,
		"qwen3.8-max": 15, "qwen3.8-flash": 30, "qwen3.7-plus": 60, "deepseek-v4.1-flash": 60, "deepseek-v4-pro": 15,
		"deepseek-v4-flash": 30, "deepseek-v4-flash-vision-exp": 15, "hy4-preview": 30, "hy3": 60,
		"grok-4.7": 15, "grok-4.6": 15, "gpt-6-luna": 15, "gpt-5.6-luna": 15,
	},
	"go-plus": {
		"glm-5.3-flash": 180, "glm-5.3": 120, "glm-5.2": 180, "kimi-k3": 60, "kimi-k2.7-code": 180, "kimi-k2.6": 240,
		"longcat-2.0": 240, "mimo-v2.6-flash": 120, "mimo-v2.6-pro": 60, "mimo-v2.5": 120, "mimo-v2.5-pro": 60,
		"minimax-m3": 180, "minimax-m2.7": 240, "muse-spark-1.3-contributor": 120, "muse-spark-1.2-contributor": 120,
		"qwen3.8-max": 60, "qwen3.8-flash": 90, "qwen3.7-plus": 180, "deepseek-v4.1-flash": 120, "deepseek-v4-pro": 60,
		"deepseek-v4-flash": 120, "deepseek-v4-flash-vision-exp": 60, "hy4-preview": 120, "hy3": 240,
		"grok-4.7": 60, "grok-4.6": 60, "gpt-6-luna": 60, "gpt-5.6-luna": 60,
	},
}

var windows = []struct {
	name, kind string
	span       time.Duration
	share      float64
}{
	{"5-hour", "5h", 5 * time.Hour, 0.2},
	{"Weekly", "week", 7 * 24 * time.Hour, 0.5},
	{"Monthly", "month", 30 * 24 * time.Hour, 1},
}

type Reader struct {
	DB   string
	Plan string // go | go-plus
}

func PlanLabel(plan string) string {
	if plan == "go-plus" {
		return "Go Plus"
	}
	return "Go"
}

type row struct {
	Model string  `json:"m"`
	Cost  float64 `json:"c"`
	At    int64   `json:"t"`
}

// Read uses the sqlite3 CLI that ships with macOS, opened read-only.
func (r Reader) Read(ctx context.Context) quota.Result {
	now := time.Now()
	res := quota.Result{Plan: PlanLabel(r.Plan), At: now}
	since := now.Add(-30 * 24 * time.Hour).UnixMilli()
	q := fmt.Sprintf(`select json_extract(data,'$.modelID') m, json_extract(data,'$.cost') c, time_created t
		from message where time_created > %d
		and json_extract(data,'$.providerID') = 'opencode-go'
		and json_extract(data,'$.role') = 'assistant'
		and json_extract(data,'$.cost') > 0`, since)
	out, err := exec.CommandContext(ctx, "sqlite3", "-readonly", "-json", "file:"+r.DB+"?mode=ro", q).Output()
	if err != nil {
		res.Error = fmt.Sprintf("read opencode db: %v", err)
		return res
	}
	var rows []row
	if len(out) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			res.Error = fmt.Sprintf("parse opencode db rows: %v", err)
			return res
		}
	}

	byModel := map[string][]row{}
	monthly := map[string]float64{}
	for _, x := range rows {
		byModel[x.Model] = append(byModel[x.Model], x)
		monthly[x.Model] += x.Cost
	}
	models := make([]string, 0, len(byModel))
	for m := range byModel {
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return monthly[models[i]] > monthly[models[j]] })

	planCaps := caps[r.Plan]
	if planCaps == nil {
		planCaps = caps["go"]
	}
	for _, m := range models {
		monthCap, ok := planCaps[m]
		if !ok {
			res.Meta = append(res.Meta, [2]string{m, fmt.Sprintf("$%.2f/30d, no known cap", monthly[m])})
			continue
		}
		for _, w := range windows {
			from := now.Add(-w.span).UnixMilli()
			spent, oldest := 0.0, int64(0)
			for _, x := range byModel[m] {
				if x.At > from {
					spent += x.Cost
					if oldest == 0 || x.At < oldest {
						oldest = x.At
					}
				}
			}
			capW := monthCap * w.share
			win := quota.Window{Name: w.name, Kind: w.kind, Model: m, Cap: capW, Spent: spent, Left: max(0, 1-spent/capW)}
			// Rolling window: allowance starts coming back when the oldest spend ages out.
			if oldest > 0 {
				t := time.UnixMilli(oldest).Add(w.span)
				win.ResetAt = &t
			}
			res.Windows = append(res.Windows, win)
		}
	}
	return res
}
