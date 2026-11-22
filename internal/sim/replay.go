package sim

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
)

type Policy struct {
	Name   string
	Config *config.Config
}

type Metrics struct {
	Policy                   string  `json:"policy"`
	PerishableQuotaUsedPct   float64 `json:"perishable_quota_used_pct"`
	ExpiredWastedQuotaPct    float64 `json:"expired_wasted_quota_pct"`
	TotalStallMinutes        float64 `json:"total_stall_minutes"`
	FailoverCount            int     `json:"failover_count"`
	ScarcePoolReserveLeftPct float64 `json:"scarce_pool_reserve_left_pct"`
	SkippedJobs              int     `json:"skipped_jobs,omitempty"`
}

type windowState struct {
	name      string
	kind      string
	duration  float64
	used      float64
	measured  bool
	resetsAt  time.Time
	lastReset time.Time
	capacity  float64
	consumed  float64
	expired   float64
}

type poolState struct {
	id        string
	providers []string
	windows   map[string]*windowState
}

type jobState struct {
	spec          jobSpec
	remaining     time.Duration
	waitingSince  time.Time
	stall         time.Duration
	provider      string
	lastProvider  string
	forceProvider string
	attempts      int
	running       bool
}

type replay struct {
	ctx          context.Context
	trace        *trace
	cfg          *config.Config
	now          time.Time
	pools        map[string]*poolState
	providerPool map[string]string
	active       map[string]int
	jobs         map[string]*jobState
	waiting      []*jobState
	running      []*jobState
	arrivalAt    int
	resetAt      int
	failovers    int
}

func Replay(ctx context.Context, ledgerPath string, policies []Policy) ([]Metrics, error) {
	t, err := loadTrace(ctx, ledgerPath)
	if err != nil {
		return nil, err
	}
	rows := make([]Metrics, 0, len(policies))
	for _, p := range policies {
		if p.Config == nil {
			return nil, fmt.Errorf("policy %q has no config", p.Name)
		}
		r, err := newReplay(ctx, t, p.Config)
		if err != nil {
			return nil, fmt.Errorf("policy %s: %w", p.Name, err)
		}
		if err := r.run(); err != nil {
			return nil, fmt.Errorf("policy %s: %w", p.Name, err)
		}
		rows = append(rows, r.metrics(p.Name))
	}
	return rows, nil
}

func newReplay(ctx context.Context, t *trace, cfg *config.Config) (*replay, error) {
	r := &replay{ctx: ctx, trace: t, cfg: cfg, now: t.start, pools: map[string]*poolState{}, providerPool: map[string]string{}, active: map[string]int{}, jobs: map[string]*jobState{}}
	for _, id := range cfg.ProviderIDs() {
		pc := cfg.Providers[id]
		key := id
		if pc.SharedQuotaPool != "" {
			key = "pool:" + pc.SharedQuotaPool
		}
		p := r.pools[key]
		if p == nil {
			p = &poolState{id: key, windows: map[string]*windowState{}}
			r.pools[key] = p
		}
		p.providers = append(p.providers, id)
		r.providerPool[id] = key
		r.active[id] = 0
		for _, cw := range pc.Windows {
			if _, exists := p.windows[cw.Name]; exists {
				continue
			}
			w := &windowState{name: cw.Name, kind: cw.Kind, duration: cw.DurationHours}
			if s, ok := initialSample(t, p.providers, cw.Name); ok && s.used != nil {
				w.used, w.measured, w.resetsAt = *s.used, true, s.resetsAt
				if w.kind == "" {
					w.kind = s.kind
				}
				if perishable(w) {
					w.capacity = 100
				}
			}
			p.windows[cw.Name] = w
		}
	}
	for id, spec := range t.jobs {
		r.jobs[id] = &jobState{spec: spec, remaining: spec.duration, waitingSince: spec.arrival}
	}
	return r, nil
}

func initialSample(t *trace, providers []string, window string) (quotaSample, bool) {
	var best quotaSample
	found := false
	for _, id := range providers {
		s, ok := t.samples[sampleKey{provider: id, window: window}]
		if ok && (!found || s.ts.After(best.ts) || s.ts.Equal(best.ts) && s.seq > best.seq) {
			best, found = s, true
		}
	}
	return best, found
}

func perishable(w *windowState) bool {
	return (w.kind == "rolling" || w.kind == "fixed_reset") && w.duration > 0 && w.duration < 168
}

func (r *replay) run() error {
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		r.resetDue()
		r.completeDue()
		r.arriveDue()
		r.stopExhausted()
		if !r.now.Before(r.trace.end) {
			break
		}
		if err := r.dispatchWaiting(); err != nil {
			return err
		}
		next, err := r.nextEvent()
		if err != nil {
			return err
		}
		r.advance(next.Sub(r.now))
		r.now = next
	}
	return nil
}

func (r *replay) nextEvent() (time.Time, error) {
	next := r.trace.end
	consider := func(ts time.Time) {
		if ts.After(r.now) && ts.Before(next) {
			next = ts
		}
	}
	if r.arrivalAt < len(r.trace.arrivals) {
		consider(r.trace.arrivals[r.arrivalAt].ts)
	}
	if r.resetAt < len(r.trace.resets) {
		consider(r.trace.resets[r.resetAt].ts)
	}
	for _, j := range r.running {
		consider(r.now.Add(j.remaining))
	}
	for _, key := range sortedPoolKeys(r.pools) {
		p := r.pools[key]
		for _, name := range sortedWindowKeys(p.windows) {
			w := p.windows[name]
			consider(w.resetsAt)
			if !w.measured || w.used >= 100 {
				continue
			}
			rate := r.burnRate(p, name)
			if rate > 0 {
				consider(r.now.Add(time.Duration((100 - w.used) / rate * float64(time.Hour))))
			}
		}
	}
	if !next.After(r.now) {
		return time.Time{}, fmt.Errorf("no future event at %s", r.now.Format(time.RFC3339))
	}
	return next, nil
}

func (r *replay) advance(elapsed time.Duration) {
	hours := elapsed.Hours()
	for _, j := range r.running {
		j.remaining -= elapsed
	}
	for _, key := range sortedPoolKeys(r.pools) {
		p := r.pools[key]
		for _, name := range sortedWindowKeys(p.windows) {
			w := p.windows[name]
			if !w.measured {
				continue
			}
			burn := r.burnRate(p, name) * hours
			burn = math.Min(burn, 100-w.used)
			if burn <= 0 {
				continue
			}
			w.used += burn
			if perishable(w) {
				w.consumed += burn
			}
		}
	}
}

func (r *replay) burnRate(p *poolState, window string) float64 {
	total := 0.0
	for _, j := range r.running {
		if r.providerPool[j.provider] == p.id {
			total += r.trace.rates[sampleKey{provider: j.provider, window: window}]
		}
	}
	return total
}

func (r *replay) arriveDue() {
	for r.arrivalAt < len(r.trace.arrivals) && !r.trace.arrivals[r.arrivalAt].ts.After(r.now) {
		e := r.trace.arrivals[r.arrivalAt]
		r.waiting = append(r.waiting, r.jobs[e.jobID])
		r.arrivalAt++
	}
}

func (r *replay) completeDue() {
	remaining := r.running[:0]
	for _, j := range r.running {
		if j.remaining > 0 {
			remaining = append(remaining, j)
			continue
		}
		j.running = false
		r.active[j.provider]--
	}
	r.running = remaining
}

func (r *replay) resetDue() {
	for r.resetAt < len(r.trace.resets) && !r.trace.resets[r.resetAt].ts.After(r.now) {
		e := r.trace.resets[r.resetAt]
		key, ok := r.providerPool[e.providerID]
		if ok {
			var payload ledger.WindowReset
			if json.Unmarshal(e.payload, &payload) == nil {
				if w := r.pools[key].windows[payload.WindowName]; w != nil && !w.lastReset.Equal(r.now) {
					r.resetWindow(w, payload.ResetsAtNext)
				}
			}
		}
		r.resetAt++
	}
	for _, key := range sortedPoolKeys(r.pools) {
		p := r.pools[key]
		for _, name := range sortedWindowKeys(p.windows) {
			w := p.windows[name]
			if w.measured && !w.resetsAt.After(r.now) && !w.lastReset.Equal(r.now) {
				r.resetWindow(w, time.Time{})
			}
		}
	}
}

func (r *replay) resetWindow(w *windowState, next time.Time) {
	if w.measured && perishable(w) {
		w.expired += math.Max(0, 100-w.used)
		w.capacity += 100
	}
	w.used = 0
	w.lastReset = r.now
	if next.IsZero() && w.duration > 0 {
		next = r.now.Add(time.Duration(w.duration * float64(time.Hour)))
	}
	w.resetsAt = next
}

func (r *replay) stopExhausted() {
	remaining := r.running[:0]
	for _, j := range r.running {
		p := r.pools[r.providerPool[j.provider]]
		exhausted := false
		for _, w := range p.windows {
			if w.measured && w.used >= 100-1e-9 {
				exhausted = true
				break
			}
		}
		if !exhausted {
			remaining = append(remaining, j)
			continue
		}
		j.running = false
		j.lastProvider = j.provider
		r.active[j.provider]--
		j.waitingSince = r.now
		role, _, err := r.cfg.Role(j.spec.role)
		if err == nil && (j.spec.neverSwitch || role.NeverSwitchMidTask || j.spec.pinned != "") {
			j.forceProvider = j.lastProvider
		}
		r.waiting = append(r.waiting, j)
	}
	r.running = remaining
}

func (r *replay) dispatchWaiting() error {
	sort.SliceStable(r.waiting, func(i, j int) bool {
		a, b := r.waiting[i], r.waiting[j]
		if a.waitingSince.Equal(b.waitingSince) {
			return a.spec.id < b.spec.id
		}
		return a.waitingSince.Before(b.waitingSince)
	})
	for {
		progress := false
		kept := r.waiting[:0]
		for _, j := range r.waiting {
			trigger := scheduler.TriggerInitial
			var exclude []string
			if j.attempts > 0 {
				trigger = scheduler.TriggerFailover
				if j.forceProvider == "" {
					exclude = []string{j.lastProvider}
				}
			}
			d, err := scheduler.Select(scheduler.Request{
				Job:     scheduler.Job{ID: j.spec.id, Role: j.spec.role, PinnedProvider: firstNonEmpty(j.spec.pinned, j.forceProvider), Exclude: exclude},
				Trigger: trigger, Now: r.now, Config: r.cfg, Providers: r.providers(),
			})
			if err != nil {
				return err
			}
			if d.ChosenProvider == "" {
				kept = append(kept, j)
				continue
			}
			j.stall += r.now.Sub(j.waitingSince)
			j.provider = d.ChosenProvider
			j.attempts++
			j.running = true
			r.active[j.provider]++
			if j.attempts > 1 && j.provider != j.lastProvider {
				r.failovers++
			}
			r.running = append(r.running, j)
			progress = true
		}
		r.waiting = kept
		if !progress {
			return nil
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (r *replay) providers() []scheduler.Provider {
	out := make([]scheduler.Provider, 0, len(r.cfg.Providers))
	for _, id := range r.cfg.ProviderIDs() {
		p := r.pools[r.providerPool[id]]
		windows := make([]scheduler.Window, 0, len(p.windows))
		for _, name := range sortedWindowKeys(p.windows) {
			w := p.windows[name]
			sw := scheduler.Window{Name: w.name, Kind: w.kind, DurationHours: w.duration, ResetsAt: w.resetsAt, Confidence: ledger.ConfidenceExact}
			if w.measured {
				used := w.used
				sw.UsedPct = &used
			}
			windows = append(windows, sw)
		}
		state := ledger.StateAvailable
		for _, w := range p.windows {
			if w.measured && w.used >= 100 && (w.resetsAt.IsZero() || w.resetsAt.After(r.now)) {
				state = ledger.StateExhausted
				break
			}
		}
		out = append(out, scheduler.Provider{ID: id, Config: r.cfg.Providers[id], State: state, ActiveJobs: r.active[id], Windows: windows})
	}
	return out
}

func (r *replay) metrics(name string) Metrics {
	used, capacity, expired := 0.0, 0.0, 0.0
	for _, key := range sortedPoolKeys(r.pools) {
		p := r.pools[key]
		for _, window := range sortedWindowKeys(p.windows) {
			w := p.windows[window]
			used += w.consumed
			capacity += w.capacity
			expired += w.expired
		}
	}
	stall := 0.0
	for _, id := range sortedJobKeys(r.jobs) {
		j := r.jobs[id]
		total := j.stall
		if !j.running && !j.waitingSince.IsZero() && j.remaining > 0 {
			total += r.now.Sub(j.waitingSince)
		}
		stall += total.Minutes()
	}
	m := Metrics{Policy: name, TotalStallMinutes: stall, FailoverCount: r.failovers, ScarcePoolReserveLeftPct: r.scarceReserve(), SkippedJobs: r.trace.skipped}
	if capacity > 0 {
		m.PerishableQuotaUsedPct = used / capacity * 100
		m.ExpiredWastedQuotaPct = expired / capacity * 100
	}
	return m
}

func (r *replay) scarceReserve() float64 {
	maxTier := 0
	for _, id := range r.cfg.ProviderIDs() {
		maxTier = max(maxTier, r.cfg.Providers[id].ScarcityTier)
	}
	total, count := 0.0, 0
	for _, key := range sortedPoolKeys(r.pools) {
		p := r.pools[key]
		scarce := false
		for _, id := range p.providers {
			if r.cfg.Providers[id].ScarcityTier == maxTier {
				scarce = true
				break
			}
		}
		if !scarce {
			continue
		}
		for _, name := range sortedWindowKeys(p.windows) {
			w := p.windows[name]
			if w.measured && (w.name == "weekly" || w.duration >= 168) {
				total += 100 - w.used
				count++
			}
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func sortedPoolKeys(m map[string]*poolState) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedWindowKeys(m map[string]*windowState) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedJobKeys(m map[string]*jobState) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
