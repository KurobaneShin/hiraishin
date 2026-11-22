package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/forecast"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
)

type Engine struct {
	Cfg    *config.Config
	Ledger *ledger.Ledger
	Paths  config.Paths
	Now    func() time.Time
	cfgMod time.Time
}

func Open(ctx context.Context, paths config.Paths, now func() time.Time) (*Engine, error) {
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return nil, fmt.Errorf("%w (run: hiraishin init)", err)
	}
	l, err := ledger.Open(ctx, paths.Ledger(), now)
	if err != nil {
		return nil, err
	}
	e := &Engine{Cfg: cfg, Ledger: l, Paths: paths, Now: now}
	if st, err := os.Stat(paths.Config); err == nil {
		e.cfgMod = st.ModTime()
	}
	if err := e.sync(ctx); err != nil {
		_ = l.Close()
		return nil, err
	}
	return e, nil
}

func (e *Engine) Close() error { return e.Ledger.Close() }

func (e *Engine) Reload(ctx context.Context) (bool, error) {
	st, err := os.Stat(e.Paths.Config)
	if err != nil {
		return false, err
	}
	if st.ModTime().Equal(e.cfgMod) {
		return false, nil
	}
	e.cfgMod = st.ModTime()
	cfg, err := config.Load(e.Paths.Config)
	if err != nil {
		return false, err
	}
	old := e.Cfg
	e.Cfg = cfg
	if err := e.sync(ctx); err != nil {
		e.Cfg = old
		return false, err
	}
	return true, nil
}

func (e *Engine) sync(ctx context.Context) error {
	return e.Ledger.Update(ctx, func(ctx context.Context, tx *ledger.Tx) ([]ledger.Event, error) {
		rows, err := tx.Providers(ctx)
		if err != nil {
			return nil, err
		}
		have := make(map[string]ledger.ProviderRow, len(rows))
		for _, r := range rows {
			have[r.ID] = r
		}
		var evs []ledger.Event
		for _, id := range e.Cfg.ProviderIDs() {
			p := e.Cfg.Providers[id]
			if r, ok := have[id]; ok && r.Harness == p.Harness && r.MaxConcurrent == p.MaxConcurrent {
				continue
			}
			evs = append(evs, ledger.Event{ProviderID: id, Source: ledger.SourceConfig, Payload: ledger.ProviderConfigured{Harness: p.Harness, MaxConcurrent: p.MaxConcurrent}})
		}
		return evs, nil
	})
}

func (e *Engine) Maintain(ctx context.Context) error {
	return e.Ledger.Update(ctx, func(ctx context.Context, tx *ledger.Tx) ([]ledger.Event, error) {
		return e.maintenanceEvents(ctx, tx)
	})
}

func (e *Engine) maintenanceEvents(ctx context.Context, tx *ledger.Tx) ([]ledger.Event, error) {
	now := e.Now()
	rows, err := tx.Providers(ctx)
	if err != nil {
		return nil, err
	}
	wins, err := tx.Windows(ctx)
	if err != nil {
		return nil, err
	}
	state := map[string]string{}
	var evs []ledger.Event
	for _, r := range rows {
		state[r.ID] = r.State
		if r.State == ledger.StateDegraded && !r.CooldownUntil.IsZero() && !r.CooldownUntil.After(now) {
			evs = append(evs, ledger.Event{ProviderID: r.ID, Source: ledger.SourceScheduler, Payload: ledger.Recovered{}})
		}
	}
	for _, w := range wins {
		if w.UsedPct == nil || *w.UsedPct < 100 || w.ResetsAt.IsZero() || w.ResetsAt.After(now) {
			continue
		}
		evs = append(evs, ledger.Event{ProviderID: w.ProviderID, Source: ledger.SourceScheduler, Payload: ledger.WindowReset{WindowName: w.Name, PreviousState: state[w.ProviderID]}})
	}
	return evs, nil
}

func (e *Engine) Snapshot(ctx context.Context, tx *ledger.Tx) ([]scheduler.Provider, error) {
	rows, err := tx.Providers(ctx)
	if err != nil {
		return nil, err
	}
	wins, err := tx.Windows(ctx)
	if err != nil {
		return nil, err
	}
	now := e.Now()
	samples, err := tx.QuotaSamples(ctx, now.Add(-forecast.Lookback))
	if err != nil {
		return nil, err
	}
	return scheduler.AttachForecasts(e.Cfg, scheduler.Snapshot(e.Cfg, rows, wins), samples, now), nil
}

func (e *Engine) Decide(ctx context.Context, tx *ledger.Tx, job ledger.JobRow, trigger string, exclude []string) (scheduler.Decision, ledger.Event, error) {
	providers, err := e.Snapshot(ctx, tx)
	if err != nil {
		return scheduler.Decision{}, ledger.Event{}, err
	}
	profile, err := tx.Profile(ctx)
	if err != nil {
		return scheduler.Decision{}, ledger.Event{}, err
	}
	prefRows, err := tx.Prefers(ctx, e.Now())
	if err != nil {
		return scheduler.Decision{}, ledger.Event{}, err
	}
	prefers := make([]scheduler.Prefer, len(prefRows))
	for i, p := range prefRows {
		prefers[i] = scheduler.Prefer{ProviderID: p.ProviderID, Role: p.Role}
	}
	learned, err := tx.LearningStats(ctx, job.Role)
	if err != nil {
		return scheduler.Decision{}, ledger.Event{}, err
	}
	floor, floorReason := e.tierFloor(ctx, tx, job)
	d, err := scheduler.Select(scheduler.Request{
		Job:     scheduler.Job{ID: job.ID, Role: job.Role, PinnedProvider: job.PinnedProvider, Exclude: exclude},
		Trigger: trigger, Now: e.Now(), Config: e.Cfg, RuntimeProfile: profile, Prefers: prefers, Providers: providers,
		LearnDeltas:    scheduler.LearnDeltas(e.Cfg.Learning, learned),
		MinQualityTier: floor, MinQualityTierReason: floorReason,
	})
	if err != nil {
		return scheduler.Decision{}, ledger.Event{}, err
	}
	d.DecisionID = ledger.NewID("dec")
	cands, err := json.Marshal(d.Candidates)
	if err != nil {
		return scheduler.Decision{}, ledger.Event{}, err
	}
	weights, err := json.Marshal(d.Weights)
	if err != nil {
		return scheduler.Decision{}, ledger.Event{}, err
	}
	ev := ledger.Event{JobID: job.ID, Source: ledger.SourceScheduler, Payload: ledger.DecisionMade{
		DecisionID: d.DecisionID, Trigger: trigger, Profile: d.Profile, ChosenProvider: d.ChosenProvider, Role: d.Role,
		MinQualityTier: d.MinQualityTier, Weights: weights, Candidates: cands, Summary: d.Summary,
		EarliestResetAt: d.EarliestResetAt, BlockedProviders: d.BlockedProviders,
	}}
	return d, ev, nil
}

func (e *Engine) OutcomeEvent(ctx context.Context, tx *ledger.Tx, providerID, role, jobID, source string, o scheduler.Outcome) (ledger.Event, error) {
	rows, err := tx.LearningStats(ctx, role)
	if err != nil {
		return ledger.Event{}, err
	}
	frozen, err := tx.LearningFrozen(ctx)
	if err != nil {
		return ledger.Event{}, err
	}
	c := e.Cfg.Learning
	return ledger.Event{ProviderID: providerID, JobID: jobID, Source: source, Payload: ledger.OutcomeRecorded{
		Role: role, Success: o.Success, ReviewRounds: o.ReviewRounds, BuildFailures: o.BuildFailures, DurationMin: o.DurationMin,
		Quality: scheduler.OutcomeQuality(c, o, scheduler.RoleDurationMin(rows)), Alpha: c.EWMAAlpha, Frozen: frozen || c.Frozen,
	}}, nil
}

func DecisionFromRow(row ledger.DecisionRow) (scheduler.Decision, error) {
	d := scheduler.Decision{
		SchemaVersion: scheduler.SchemaVersion, DecisionID: row.ID, JobID: row.JobID, Trigger: row.Trigger, Role: row.Role,
		MinQualityTier: row.MinQualityTier, Profile: row.Profile, ChosenProvider: row.ChosenProvider,
		Summary: row.Summary, EarliestResetAt: row.EarliestResetAt, CreatedAt: row.CreatedAt,
	}
	for _, part := range []struct {
		raw string
		dst any
	}{{row.CandidatesJSON, &d.Candidates}, {row.WeightsJSON, &d.Weights}, {row.BlockedJSON, &d.BlockedProviders}} {
		if err := json.Unmarshal([]byte(part.raw), part.dst); err != nil {
			return scheduler.Decision{}, fmt.Errorf("decode decision %s: %w", row.ID, err)
		}
	}
	return d, nil
}
