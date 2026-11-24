package scheduler

import (
	"cmp"
	"math"
	"slices"
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
			if f, ok := forecast.Project(trace[w.Name], cmp.Or(w.ResetsAt, configuredReset(ps[i].Config, w.Name, now)), now); ok {
				w.Forecast = &f
			} else {
				w.BurnReadyIn = forecast.Warmup(trace[w.Name])
			}
		}
		capWaste(ps[i].Windows, now)
	}
	return ps
}

func capWaste(ws []Window, now time.Time) {
	order := make([]int, len(ws))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		ra, _ := resetIn(ws[a], now)
		rb, _ := resetIn(ws[b], now)
		return cmp.Compare(rb, ra)
	})
	for _, i := range order {
		w := ws[i]
		if w.Forecast == nil || w.UsedPct == nil || *w.UsedPct >= 100 {
			continue
		}
		room, _ := roomBeyondPace(w, ws, now)
		w.Forecast.PerishableWasteRisk = min(w.Forecast.PerishableWasteRisk, room/(100-*w.UsedPct))
	}
}

func roomBeyondPace(w Window, ws []Window, now time.Time) (float64, string) {
	room, by := math.Inf(1), ""
	for _, o := range ws {
		if o.Name == w.Name || resetsBefore(o, w, now) {
			continue
		}
		r := math.Inf(1)
		switch {
		case o.UsedPct != nil && *o.UsedPct >= 100, o.Forecast != nil && o.Forecast.EmptiesBeforeReset:
			r = 0
		case o.UsedPct != nil && o.Forecast != nil && w.Forecast != nil && o.Forecast.PctPerHour > 0 && w.Forecast.PctPerHour > 0:
			r = o.Forecast.PerishableWasteRisk * (100 - *o.UsedPct) * w.Forecast.PctPerHour / o.Forecast.PctPerHour
		}
		if r < room {
			room, by = r, o.Name
		}
	}
	return room, by
}

type Outlook map[string]map[string]forecast.Forecast

func OutlookOf(ps []Provider) Outlook {
	out := Outlook{}
	for _, p := range ps {
		for _, w := range p.Windows {
			if w.Forecast == nil {
				continue
			}
			if out[p.ID] == nil {
				out[p.ID] = map[string]forecast.Forecast{}
			}
			out[p.ID][w.Name] = *w.Forecast
		}
	}
	return out
}

func (o Outlook) Apply(ps []Provider) {
	for i := range ps {
		for j := range ps[i].Windows {
			if f, ok := o[ps[i].ID][ps[i].Windows[j].Name]; ok {
				ps[i].Windows[j].Forecast = &f
			}
		}
	}
}

func resetsBefore(a, b Window, now time.Time) bool {
	ra, okA := resetIn(a, now)
	rb, okB := resetIn(b, now)
	return okA && okB && ra < rb
}

func resetIn(w Window, now time.Time) (time.Duration, bool) {
	if w.Forecast != nil && w.Forecast.TimeToResetSeconds != nil {
		return time.Duration(*w.Forecast.TimeToResetSeconds) * time.Second, true
	}
	if !w.ResetsAt.IsZero() {
		return w.ResetsAt.Sub(now), true
	}
	return 0, false
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
