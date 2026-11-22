package scheduler

import (
	"cmp"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/forecast"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

func Snapshot(cfg *config.Config, rows []ledger.ProviderRow, windows []ledger.WindowRow) []Provider {
	byID := make(map[string]ledger.ProviderRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	observed := map[string]map[string]ledger.WindowRow{}
	for _, w := range windows {
		key := quotaKey(cfg, w.ProviderID)
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
		out = append(out, Provider{ID: id, Config: pc, State: row.State, ActiveJobs: row.ActiveJobs, CooldownUntil: row.CooldownUntil, Windows: mergeWindows(pc.Windows, observed[quotaKey(cfg, id)])})
	}
	return out
}

func quotaKey(cfg *config.Config, providerID string) string {
	if pool := cfg.Providers[providerID].SharedQuotaPool; pool != "" {
		return "pool:" + pool
	}
	return providerID
}

func AttachForecasts(cfg *config.Config, ps []Provider, samples []ledger.SampleRow, now time.Time) []Provider {
	traces := map[string]map[string][]forecast.Sample{}
	for _, s := range samples {
		key := quotaKey(cfg, s.ProviderID)
		if traces[key] == nil {
			traces[key] = map[string][]forecast.Sample{}
		}
		traces[key][s.WindowName] = append(traces[key][s.WindowName], forecast.Sample{At: s.At, UsedPct: s.UsedPct, ResetsAt: s.ResetsAt})
	}
	for i := range ps {
		trace := traces[quotaKey(cfg, ps[i].ID)]
		for j := range ps[i].Windows {
			w := &ps[i].Windows[j]
			if len(trace[w.Name]) == 0 {
				continue
			}
			if f, ok := forecast.Project(trace[w.Name], cmp.Or(w.ResetsAt, configuredReset(ps[i].Config, w.Name, now)), now); ok {
				w.Forecast = &f
			}
		}
	}
	return ps
}

func configuredReset(pc config.Provider, window string, now time.Time) time.Time {
	for _, cw := range pc.Windows {
		if cw.Name == window && cw.FixedResetCron != "" {
			if at, err := forecast.NextReset(cw.FixedResetCron, cw.FixedResetTZ, now); err == nil {
				return at
			}
		}
	}
	return time.Time{}
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
	if len(configured) > 0 {
		return out
	}
	for name, o := range seen {
		if !used[name] {
			out = append(out, Window{Name: name, Kind: o.Kind, UsedPct: o.UsedPct, ResetsAt: o.ResetsAt, Confidence: o.Confidence})
		}
	}
	return out
}
