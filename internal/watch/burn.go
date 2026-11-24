package watch

import (
	"context"
	"slices"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/forecast"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

func (w *Watcher) burnEvents(ctx context.Context, jobID string, att ledger.AttemptRow, end time.Time) []ledger.Event {
	e := w.engine
	members := e.Cfg.PoolMembers(att.ProviderID)
	r := e.Ledger.Reader()
	samples, err := r.QuotaSamples(ctx, att.StartedAt)
	if err != nil {
		w.log.Debug("job burn skipped", "job", jobID, "err", err)
		return nil
	}
	attempts, err := r.AttemptsSince(ctx, att.StartedAt)
	if err != nil {
		w.log.Debug("job burn skipped", "job", jobID, "err", err)
		return nil
	}
	var others []forecast.Span
	for _, a := range attempts {
		if a.ID != att.ID && slices.Contains(members, a.ProviderID) {
			others = append(others, forecast.Span{From: a.StartedAt, To: a.EndedAt})
		}
	}
	traces := map[string][]forecast.Sample{}
	var windows []string
	for _, s := range samples {
		if !slices.Contains(members, s.ProviderID) || s.At.After(end) {
			continue
		}
		if _, seen := traces[s.WindowName]; !seen {
			windows = append(windows, s.WindowName)
		}
		traces[s.WindowName] = append(traces[s.WindowName], forecast.Sample{At: s.At, UsedPct: s.UsedPct, ResetsAt: s.ResetsAt})
	}
	job := forecast.Span{From: att.StartedAt, To: end}
	minutes := end.Sub(att.StartedAt).Minutes()
	var evs []ledger.Event
	for _, name := range windows {
		rate, ok := forecast.JobBurn(traces[name], job, others)
		if !ok {
			continue
		}
		evs = append(evs, ledger.Event{JobID: jobID, ProviderID: att.ProviderID, Source: ledger.SourceWatcher, Payload: ledger.JobBurnMeasured{
			AttemptID: att.ID, WindowName: name, PctPerHour: rate, DurationMin: minutes,
		}})
	}
	return evs
}
