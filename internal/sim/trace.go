package sim

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/ledger"
	_ "modernc.org/sqlite"
)

type rawEvent struct {
	seq        int64
	ts         time.Time
	kind       ledger.Kind
	providerID string
	jobID      string
	payload    []byte
}

type sampleKey struct {
	provider string
	window   string
}

type quotaSample struct {
	ts       time.Time
	seq      int64
	used     *float64
	resetsAt time.Time
	kind     string
}

type burnRate struct {
	delta float64
	hours float64
}

type trace struct {
	events   []rawEvent
	start    time.Time
	end      time.Time
	arrivals []rawEvent
	resets   []rawEvent
	jobs     map[string]jobSpec
	samples  map[sampleKey]quotaSample
	rates    map[sampleKey]float64
}

type jobSpec struct {
	id          string
	role        string
	arrival     time.Time
	duration    time.Duration
	pinned      string
	neverSwitch bool
}

func loadTrace(ctx context.Context, path string) (*trace, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Set("_pragma", "busy_timeout(5000)")
	u := &url.URL{Scheme: "file", Path: absolute, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open ledger read-only: %w", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `SELECT seq, ts, kind, provider_id, job_id, payload_json FROM events ORDER BY ts, seq`)
	if err != nil {
		return nil, fmt.Errorf("read ledger events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	t := &trace{jobs: map[string]jobSpec{}, samples: map[sampleKey]quotaSample{}, rates: map[sampleKey]float64{}}
	for rows.Next() {
		var e rawEvent
		var ts, kind, payload string
		var provider, job sql.NullString
		if err := rows.Scan(&e.seq, &ts, &kind, &provider, &job, &payload); err != nil {
			return nil, fmt.Errorf("scan ledger event: %w", err)
		}
		e.ts, err = time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return nil, fmt.Errorf("event %d timestamp: %w", e.seq, err)
		}
		e.kind, e.providerID, e.jobID, e.payload = ledger.Kind(kind), provider.String, job.String, []byte(payload)
		t.events = append(t.events, e)
		if t.end.Before(e.ts) {
			t.end = e.ts
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := t.index(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *trace) index() error {
	dispatched := map[string]time.Time{}
	completed := map[string]rawEvent{}
	previous := map[sampleKey]quotaSample{}
	deltas := map[sampleKey]burnRate{}
	for _, e := range t.events {
		switch e.kind {
		case ledger.KindJobQueued:
			var p ledger.JobQueued
			if err := json.Unmarshal(e.payload, &p); err != nil {
				return fmt.Errorf("event %d job.queued: %w", e.seq, err)
			}
			if _, exists := t.jobs[e.jobID]; exists {
				return fmt.Errorf("job %q has multiple arrivals", e.jobID)
			}
			t.jobs[e.jobID] = jobSpec{id: e.jobID, role: p.Role, arrival: e.ts, pinned: p.PinnedProvider, neverSwitch: p.NeverSwitch}
			t.arrivals = append(t.arrivals, e)
			if t.start.IsZero() || e.ts.Before(t.start) {
				t.start = e.ts
			}
		case ledger.KindJobDispatched:
			if _, ok := dispatched[e.jobID]; !ok {
				dispatched[e.jobID] = e.ts
			}
		case ledger.KindJobCompleted:
			completed[e.jobID] = e
		case ledger.KindQuotaSampled:
			var p ledger.QuotaSampled
			if err := json.Unmarshal(e.payload, &p); err != nil {
				return fmt.Errorf("event %d sensor.quota_sampled: %w", e.seq, err)
			}
			key := sampleKey{provider: e.providerID, window: p.WindowName}
			s := quotaSample{ts: e.ts, seq: e.seq, used: p.UsedPct, resetsAt: p.ResetsAt, kind: p.WindowKind}
			if t.start.IsZero() || !e.ts.After(t.start) {
				t.samples[key] = s
			}
			if prior, ok := previous[key]; ok && prior.used != nil && p.UsedPct != nil && e.ts.After(prior.ts) && *p.UsedPct > *prior.used {
				d := deltas[key]
				d.delta += *p.UsedPct - *prior.used
				d.hours += e.ts.Sub(prior.ts).Hours()
				deltas[key] = d
			}
			previous[key] = s
		case ledger.KindWindowReset:
			if !e.ts.Before(t.start) {
				t.resets = append(t.resets, e)
			}
		}
	}
	for key, d := range deltas {
		if d.hours > 0 {
			t.rates[key] = d.delta / d.hours
		}
	}
	if len(t.jobs) == 0 {
		return fmt.Errorf("ledger has no job arrivals")
	}
	for id, job := range t.jobs {
		e, ok := completed[id]
		if !ok {
			return fmt.Errorf("job %q has no completion duration", id)
		}
		var p ledger.JobCompleted
		if err := json.Unmarshal(e.payload, &p); err != nil {
			return fmt.Errorf("event %d job.completed: %w", e.seq, err)
		}
		minutes := p.DurationMin
		if minutes <= 0 {
			minutes = e.ts.Sub(dispatched[id]).Minutes()
		}
		if minutes <= 0 {
			return fmt.Errorf("job %q has no positive completion duration", id)
		}
		job.duration = time.Duration(minutes * float64(time.Minute))
		t.jobs[id] = job
	}
	sort.Slice(t.arrivals, func(i, j int) bool {
		if t.arrivals[i].ts.Equal(t.arrivals[j].ts) {
			return t.arrivals[i].jobID < t.arrivals[j].jobID
		}
		return t.arrivals[i].ts.Before(t.arrivals[j].ts)
	})
	return nil
}
