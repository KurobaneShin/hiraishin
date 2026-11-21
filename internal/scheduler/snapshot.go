package scheduler

import (
	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

func Snapshot(cfg *config.Config, rows []ledger.ProviderRow, windows []ledger.WindowRow) []Provider {
	byID := make(map[string]ledger.ProviderRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	observed := map[string]map[string]ledger.WindowRow{}
	for _, w := range windows {
		pool := cfg.Providers[w.ProviderID].SharedQuotaPool
		key := w.ProviderID
		if pool != "" {
			key = "pool:" + pool
		}
		if observed[key] == nil {
			observed[key] = map[string]ledger.WindowRow{}
		}
		if cur, ok := observed[key][w.Name]; !ok || newer(w, cur) {
			observed[key][w.Name] = w
		}
	}
	out := make([]Provider, 0, len(cfg.Providers))
	for _, id := range cfg.ProviderIDs() {
		pc := cfg.Providers[id]
		row, ok := byID[id]
		if !ok {
			row = ledger.ProviderRow{ID: id, State: ledger.StateAvailable}
		}
		key := id
		if pc.SharedQuotaPool != "" {
			key = "pool:" + pc.SharedQuotaPool
		}
		out = append(out, Provider{ID: id, Config: pc, State: row.State, ActiveJobs: row.ActiveJobs, CooldownUntil: row.CooldownUntil, Windows: mergeWindows(pc.Windows, observed[key])})
	}
	return out
}

func newer(a, b ledger.WindowRow) bool { return a.SampledAt.After(b.SampledAt) }

func mergeWindows(configured []config.Window, seen map[string]ledger.WindowRow) []Window {
	out := make([]Window, 0, len(configured)+len(seen))
	used := map[string]bool{}
	for _, cw := range configured {
		w := Window{Name: cw.Name, Kind: cw.Kind, DurationHours: cw.DurationHours, Confidence: ""}
		if o, ok := seen[cw.Name]; ok {
			w.UsedPct, w.ResetsAt, w.Confidence = o.UsedPct, o.ResetsAt, o.Confidence
		}
		used[cw.Name] = true
		out = append(out, w)
	}
	for name, o := range seen {
		if !used[name] {
			out = append(out, Window{Name: name, Kind: o.Kind, UsedPct: o.UsedPct, ResetsAt: o.ResetsAt, Confidence: o.Confidence})
		}
	}
	return out
}
