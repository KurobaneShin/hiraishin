package scheduler

import (
	"fmt"
	"slices"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

const JobBurnWindow = 20

type JobBurn struct {
	PctPerHour  float64 `json:"pct_per_hour"`
	DurationMin float64 `json:"duration_min"`
	Samples     int     `json:"samples"`
}

func EstimateJobBurn(newestFirst []ledger.JobBurnRow) map[string]map[string]JobBurn {
	type key struct{ provider, window string }
	rates, minutes := map[key][]float64{}, map[key][]float64{}
	for _, r := range newestFirst {
		k := key{r.ProviderID, r.WindowName}
		if len(rates[k]) == JobBurnWindow {
			continue
		}
		rates[k] = append(rates[k], r.PctPerHour)
		minutes[k] = append(minutes[k], r.DurationMin)
	}
	out := map[string]map[string]JobBurn{}
	for k, rs := range rates {
		if out[k.provider] == nil {
			out[k.provider] = map[string]JobBurn{}
		}
		var burnt, total float64
		for i, r := range rs {
			burnt += r * minutes[k][i]
			total += minutes[k][i]
		}
		est := JobBurn{DurationMin: median(minutes[k]), Samples: len(rs)}
		if total > 0 {
			est.PctPerHour = burnt / total
		}
		out[k.provider][k.window] = est
	}
	return out
}

func median(vs []float64) float64 {
	s := slices.Clone(vs)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func quotaBrake(req Request, p Provider, byID map[string]Provider, limit float64) string {
	own := req.JobBurn[p.ID]
	for _, win := range p.Windows {
		b, ok := own[win.Name]
		if !ok || win.UsedPct == nil {
			continue
		}
		rate, running := b.PctPerHour, 0
		for _, m := range req.Config.PoolMembers(p.ID) {
			active := byID[m].ActiveJobs
			if active == 0 {
				continue
			}
			mb, ok := req.JobBurn[m][win.Name]
			if !ok {
				mb = b
			}
			rate += float64(active) * mb.PctPerHour
			running += active
		}
		horizon := time.Duration(b.DurationMin * float64(time.Minute))
		if !win.ResetsAt.IsZero() {
			horizon = min(horizon, win.ResetsAt.Sub(req.Now))
		}
		if horizon <= 0 {
			continue
		}
		if projected := *win.UsedPct + rate*horizon.Hours(); projected >= limit {
			return fmt.Sprintf("quota brake: window %s at %.1f%% with %d running + this job (%.2g%%/h together) reaches %.0f%% in %s, >= reserve %.0f%%",
				win.Name, *win.UsedPct, running, rate, projected, horizon.Round(time.Minute), limit)
		}
	}
	return ""
}
