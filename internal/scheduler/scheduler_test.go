package scheduler

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/forecast"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

var now = time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

func prov(q, s, sp, maxC int, metered bool) config.Provider {
	cc := "subscription"
	if metered {
		cc = "metered"
	}
	return config.Provider{Harness: config.HarnessKimiNative, CostClass: cc, QualityTier: q, ScarcityTier: s, SpeedTier: sp, MaxConcurrent: maxC,
		Windows: []config.Window{{Name: "5h", Kind: "rolling", DurationHours: 5}, {Name: "weekly", Kind: "fixed_reset", DurationHours: 168}}}
}

func baseConfig() *config.Config {
	return &config.Config{
		SchemaVersion: 1,
		General:       config.General{ActiveProfile: "burn-expiring-first", WarnUsedPct: 85, ReserveUsedPct: 98},
		Providers: map[string]config.Provider{
			"kimi":   prov(4, 2, 4, 3, false),
			"agy":    prov(4, 3, 4, 3, false),
			"sonnet": prov(4, 4, 4, 4, false),
			"opus":   prov(5, 5, 3, 4, false),
			"api":    prov(5, 1, 5, 4, true),
		},
		Roles: map[string]config.Role{"gameplay": {AllowedProviders: []string{"kimi", "agy", "sonnet", "api"}, MinQualityTier: 3}},
	}
}

func snapshot(cfg *config.Config, edit func(map[string]*Provider)) []Provider {
	ps := Snapshot(cfg, nil, nil)
	byID := map[string]*Provider{}
	for i := range ps {
		byID[ps[i].ID] = &ps[i]
	}
	if edit != nil {
		edit(byID)
	}
	return ps
}

func setWindow(p *Provider, name string, used float64, resets time.Time) {
	for i := range p.Windows {
		if p.Windows[i].Name == name {
			p.Windows[i].UsedPct, p.Windows[i].ResetsAt, p.Windows[i].Confidence = f(used), resets, ledger.ConfidenceExact
		}
	}
}

func candidate(t *testing.T, d Decision, id string) Candidate {
	t.Helper()
	for _, c := range d.Candidates {
		if c.Provider == id {
			return c
		}
	}
	t.Fatalf("no candidate %s", id)
	return Candidate{}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestScoringFormula(t *testing.T) {
	cfg := baseConfig()
	ps := snapshot(cfg, func(m map[string]*Provider) {
		setWindow(m["kimi"], "5h", 24, now.Add(72*time.Minute))
		setWindow(m["kimi"], "weekly", 38, now.Add(72*time.Hour))
		m["kimi"].ActiveJobs = 1
	})
	d, err := Select(Request{Job: Job{ID: "job_1", Role: "gameplay"}, Trigger: TriggerInitial, Now: now, Config: cfg, Providers: ps})
	if err != nil {
		t.Fatal(err)
	}
	k := candidate(t, d, "kimi")
	wantE := 0.76 * (1 - 72.0/300) * 1.6
	wantS := 0.6*0.75 + 0.4*0.62
	if !near(k.Features.Quality, 0.75) || !near(k.Features.Expiry, math.Min(wantE, 1)) || !near(k.Features.Scarcity, wantS) || !near(k.Features.Latency, 0.75) {
		t.Fatalf("features = %+v", *k.Features)
	}
	wantScore := 0.2*0.75 + 0.5*math.Min(wantE, 1) + 0.2*wantS + 0.1*0.75 + 0.03
	if !near(k.Score, wantScore) || d.ChosenProvider != "kimi" || k.Status != StatusSelected {
		t.Fatalf("score = %v want %v, chosen %s", k.Score, wantScore, d.ChosenProvider)
	}
	a := candidate(t, d, "agy")
	if !a.ExpiryNeutral || !near(a.Features.Expiry, NeutralExpiry) || !near(a.OrderBonus, 0.02) {
		t.Errorf("agy = %+v", a)
	}
	if !strings.Contains(d.Summary, "Picked kimi") || !strings.Contains(d.Summary, "largest edge: expiry") {
		t.Errorf("summary = %q", d.Summary)
	}
}

func TestStageOneGates(t *testing.T) {
	reset := now.Add(5 * time.Hour)
	tests := []struct {
		name    string
		edit    func(map[string]*Provider, *config.Config, *Job)
		trigger string
		target  string
		reason  string
	}{
		{"exhausted", func(m map[string]*Provider, _ *config.Config, _ *Job) {
			m["kimi"].State = ledger.StateExhausted
			setWindow(m["kimi"], "5h", 100, reset)
		}, TriggerInitial, "kimi", "state=EXHAUSTED (window 5h resets 20:00 UTC)"},
		{"window at 100%", func(m map[string]*Provider, _ *config.Config, _ *Job) {
			setWindow(m["kimi"], "weekly", 100, reset)
		}, TriggerInitial, "kimi", "state=EXHAUSTED (window weekly resets 20:00 UTC)"},
		{"paused", func(m map[string]*Provider, _ *config.Config, _ *Job) { m["kimi"].State = ledger.StatePaused }, TriggerInitial, "kimi", "state=PAUSED"},
		{"draining", func(m map[string]*Provider, _ *config.Config, _ *Job) { m["kimi"].State = ledger.StateDraining }, TriggerInitial, "kimi", "state=DRAINING"},
		{"auth", func(m map[string]*Provider, _ *config.Config, _ *Job) { m["kimi"].State = ledger.StateAuthError }, TriggerInitial, "kimi", "state=AUTH_ERROR"},
		{"cooldown", func(m map[string]*Provider, _ *config.Config, _ *Job) {
			m["kimi"].State, m["kimi"].CooldownUntil = ledger.StateDegraded, now.Add(time.Minute)
		}, TriggerInitial, "kimi", "cooldown until 15:01 UTC"},
		{"disabled", func(m map[string]*Provider, _ *config.Config, _ *Job) {
			off := false
			m["kimi"].Config.Enabled = &off
		}, TriggerInitial, "kimi", "disabled"},
		{"metered", nil, TriggerInitial, "api", "metered and general.allow_metered_fallback = false"},
		{"not allowed", nil, TriggerInitial, "opus", "not in roles.gameplay.allowed_providers"},
		{"denied", func(_ map[string]*Provider, c *config.Config, _ *Job) {
			r := c.Roles["gameplay"]
			r.DenyProviders = []string{"agy"}
			c.Roles["gameplay"] = r
		}, TriggerInitial, "agy", "in roles.gameplay.deny_providers"},
		{"quality", func(_ map[string]*Provider, c *config.Config, _ *Job) {
			r := c.Roles["gameplay"]
			r.MinQualityTier = 5
			c.Roles["gameplay"] = r
		}, TriggerInitial, "kimi", "quality_tier 4 < min_quality_tier 5"},
		{"slots", func(m map[string]*Provider, _ *config.Config, _ *Job) { m["kimi"].ActiveJobs = 3 }, TriggerInitial, "kimi", "slots 3/3 full"},
		{"reserve", func(m map[string]*Provider, _ *config.Config, _ *Job) { setWindow(m["kimi"], "weekly", 98.5, reset) }, TriggerInitial, "kimi", "window weekly 98.5% used >= reserve 98.0%"},
		{"pin", func(_ map[string]*Provider, _ *config.Config, j *Job) { j.PinnedProvider = "agy" }, TriggerInitial, "kimi", "job pinned to agy"},
		{"excluded", func(_ map[string]*Provider, _ *config.Config, j *Job) { j.Exclude = []string{"kimi"} }, TriggerFailover, "kimi", "excluded for this failover"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig()
			job := Job{ID: "job_1", Role: "gameplay"}
			ps := snapshot(cfg, func(m map[string]*Provider) {
				if tt.edit != nil {
					tt.edit(m, cfg, &job)
				}
			})
			d, err := Select(Request{Job: job, Trigger: tt.trigger, Now: now, Config: cfg, Providers: ps})
			if err != nil {
				t.Fatal(err)
			}
			c := candidate(t, d, tt.target)
			if c.Status != StatusRejected || !strings.Contains(strings.Join(c.Rejections, "; "), tt.reason) {
				t.Fatalf("candidate %s = %s %v, want rejection %q", tt.target, c.Status, c.Rejections, tt.reason)
			}
			if c.Features != nil {
				t.Errorf("rejected candidate scored")
			}
		})
	}
}

func TestFailoverMayUseReserve(t *testing.T) {
	cfg := baseConfig()
	ps := snapshot(cfg, func(m map[string]*Provider) { setWindow(m["kimi"], "weekly", 99, now.Add(time.Hour)) })
	d, _ := Select(Request{Job: Job{ID: "j", Role: "gameplay"}, Trigger: TriggerFailover, Now: now, Config: cfg, Providers: ps})
	if c := candidate(t, d, "kimi"); c.Status == StatusRejected {
		t.Fatalf("failover rejected reserve window: %v", c.Rejections)
	}
	off := false
	cfg.General.ReserveAllowFailover = &off
	d, _ = Select(Request{Job: Job{ID: "j", Role: "gameplay"}, Trigger: TriggerFailover, Now: now, Config: cfg, Providers: ps})
	if c := candidate(t, d, "kimi"); c.Status != StatusRejected {
		t.Fatal("reserve_allow_failover = false must keep the reserve gate")
	}
}

func TestNoEligibleProviderWaits(t *testing.T) {
	cfg := baseConfig()
	ps := snapshot(cfg, func(m map[string]*Provider) {
		for id, p := range m {
			p.State = ledger.StateExhausted
			setWindow(p, "5h", 100, now.Add(time.Duration(len(id))*time.Hour))
		}
	})
	d, err := Select(Request{Job: Job{ID: "j", Role: "gameplay"}, Trigger: TriggerInitial, Now: now, Config: cfg, Providers: ps})
	if err != nil {
		t.Fatal(err)
	}
	if d.ChosenProvider != "" || !d.EarliestResetAt.Equal(now.Add(3*time.Hour)) || !strings.Contains(d.Summary, "WAITING_QUOTA") {
		t.Fatalf("decision = %+v", d)
	}
}

func TestProfilesAndPreferences(t *testing.T) {
	cfg := baseConfig()
	ps := snapshot(cfg, nil)
	pick := func(req Request) string {
		req.Job, req.Now, req.Config, req.Providers, req.Trigger = Job{ID: "j", Role: "gameplay"}, now, cfg, ps, TriggerInitial
		d, err := Select(req)
		if err != nil {
			t.Fatal(err)
		}
		return d.ChosenProvider + "/" + d.Profile
	}
	if got := pick(Request{RuntimeProfile: "save-scarce"}); got != "kimi/save-scarce" {
		t.Errorf("save-scarce = %s", got)
	}
	if got := pick(Request{Prefers: []Prefer{{ProviderID: "sonnet"}}}); got != "sonnet/burn-expiring-first" {
		t.Errorf("prefer = %s", got)
	}
	r := cfg.Roles["gameplay"]
	r.ProfileOverride = "quality-first"
	cfg.Roles["gameplay"] = r
	if got := pick(Request{RuntimeProfile: "save-scarce"}); !strings.HasSuffix(got, "/quality-first") {
		t.Errorf("role override = %s", got)
	}
}

func TestSnapshotSharesPoolWindowsAndWarnDegrades(t *testing.T) {
	cfg := baseConfig()
	for _, id := range []string{"kimi", "agy"} {
		p := cfg.Providers[id]
		p.SharedQuotaPool = "pool"
		cfg.Providers[id] = p
	}
	rows := []ledger.ProviderRow{{ID: "kimi", State: ledger.StateAvailable}, {ID: "agy", State: ledger.StateAvailable}}
	wins := []ledger.WindowRow{{ProviderID: "kimi", Name: "5h", Kind: "rolling", UsedPct: f(90), ResetsAt: now.Add(time.Hour), Confidence: ledger.ConfidenceExact, SampledAt: now}}
	ps := Snapshot(cfg, rows, wins)
	for _, p := range ps {
		if p.ID != "agy" {
			continue
		}
		if p.Windows[0].UsedPct == nil || *p.Windows[0].UsedPct != 90 {
			t.Fatalf("pool window not shared: %+v", p.Windows)
		}
		if got := EffectiveState(p, 85, now); got != ledger.StateDegraded {
			t.Errorf("effective state = %s, want DEGRADED", got)
		}
	}
}

func TestExpiryFallsBackToTheShortestWindowWithAKnownReset(t *testing.T) {
	cfg := baseConfig()
	ps := snapshot(cfg, func(m map[string]*Provider) {
		setWindow(m["kimi"], "5h", 0, time.Time{})
		setWindow(m["kimi"], "weekly", 86, now.Add(72*time.Hour))
	})
	d, err := Select(Request{Job: Job{ID: "j", Role: "gameplay"}, Trigger: TriggerInitial, Now: now, Config: cfg, Providers: ps})
	if err != nil {
		t.Fatal(err)
	}
	k := candidate(t, d, "kimi")
	want := 0.14 * (1 - 72.0/168) * expiryGain
	if k.ExpiryNeutral || !near(k.Features.Expiry, want) {
		t.Fatalf("expiry = %v neutral = %v, want %v from the weekly window", k.Features.Expiry, k.ExpiryNeutral, want)
	}
}

func TestFullSlotsWaitForAFreeSlotNotAReset(t *testing.T) {
	cfg := baseConfig()
	ps := snapshot(cfg, func(m map[string]*Provider) {
		for _, p := range m {
			p.ActiveJobs = p.Config.MaxConcurrent
		}
	})
	d, err := Select(Request{Job: Job{ID: "j", Role: "gameplay"}, Trigger: TriggerInitial, Now: now, Config: cfg, Providers: ps})
	if err != nil {
		t.Fatal(err)
	}
	if d.ChosenProvider != "" || !slices.Equal(d.FullProviders, []string{"agy", "kimi", "sonnet"}) {
		t.Fatalf("full = %v decision = %+v", d.FullProviders, d)
	}
	if !strings.Contains(d.Summary, "No free slot") || strings.Contains(d.Summary, "reset") {
		t.Errorf("summary = %q", d.Summary)
	}
}

func TestAttachForecastsSharesPoolTracesAndUsesTheFixedResetCron(t *testing.T) {
	cfg := baseConfig()
	for _, id := range []string{"kimi", "agy"} {
		p := cfg.Providers[id]
		p.SharedQuotaPool = "pool"
		cfg.Providers[id] = p
	}
	sonnet := cfg.Providers["sonnet"]
	sonnet.Windows = []config.Window{{Name: "daily", Kind: "fixed_reset", FixedResetCron: "0 0 * * *", FixedResetTZ: "America/Los_Angeles"}}
	cfg.Providers["sonnet"] = sonnet
	reset := now.Add(3 * time.Hour)
	var samples []ledger.SampleRow
	var wins []ledger.WindowRow
	for m := 0; m <= 120; m += 10 {
		at := now.Add(time.Duration(m-120) * time.Minute)
		member := []string{"kimi", "agy"}[m/10%2]
		samples = append(samples,
			ledger.SampleRow{ProviderID: member, WindowName: "5h", At: at, UsedPct: 25 * float64(m) / 60, ResetsAt: reset},
			ledger.SampleRow{ProviderID: "sonnet", WindowName: "daily", At: at, UsedPct: 2 * float64(m) / 60})
		wins = []ledger.WindowRow{
			{ProviderID: member, Name: "5h", Kind: "rolling", UsedPct: f(25 * float64(m) / 60), ResetsAt: reset, Confidence: ledger.ConfidenceExact, SampledAt: at},
			{ProviderID: "sonnet", Name: "daily", Kind: "fixed_reset", UsedPct: f(2 * float64(m) / 60), Confidence: ledger.ConfidenceExact, SampledAt: at},
		}
	}
	ps := AttachForecasts(cfg, Snapshot(cfg, nil, wins), samples, now)
	byID := map[string]Provider{}
	for _, p := range ps {
		byID[p.ID] = p
	}
	for _, id := range []string{"kimi", "agy"} {
		w := byID[id].Windows[0]
		if w.Forecast == nil || !near(w.Forecast.PctPerHour, 25) || !w.Forecast.EmptiesBeforeReset {
			t.Fatalf("%s 5h forecast = %+v, want 25%%/h emptying before reset", id, w.Forecast)
		}
		if got := EffectiveState(byID[id], 85, now); got != ledger.StateAvailable {
			t.Errorf("%s at 50%% used is forecast to empty but effective state = %s, want AVAILABLE", id, got)
		}
	}
	daily := byID["sonnet"].Windows[0]
	if daily.Forecast == nil || daily.Forecast.TimeToResetSeconds == nil || *daily.Forecast.TimeToResetSeconds != 17*3600 {
		t.Fatalf("sonnet daily forecast = %+v, want reset at midnight PT (17h after 15:00 UTC)", daily.Forecast)
	}
	if daily.Forecast.EmptiesBeforeReset || EffectiveState(byID["sonnet"], 85, now) != ledger.StateAvailable {
		t.Errorf("sonnet burns 2%%/h with 96%% left and 17h to reset: forecast = %+v", *daily.Forecast)
	}
	if byID["opus"].Windows[0].Forecast != nil {
		t.Errorf("opus has no samples but got a forecast: %+v", byID["opus"].Windows[0].Forecast)
	}
}

func TestShortWindowCannotWasteWhileALongerWindowBinds(t *testing.T) {
	short, long := now.Add(2*time.Hour), now.Add(18*time.Hour)
	tests := []struct {
		name       string
		weeklyUsed float64
		weeklyRate float64
		wantWaste  float64
		wantState  string
	}{
		{"weekly at 100%", 100, 0, 0, ledger.StateExhausted},
		{"weekly runs out before its reset", 90, 1, 0, ledger.StateDegraded},
		{"weekly has room", 10, 1, 1, ledger.StateAvailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig()
			ps := snapshot(cfg, func(m map[string]*Provider) {
				setWindow(m["kimi"], "5h", 3, short)
				setWindow(m["kimi"], "weekly", tt.weeklyUsed, long)
			})
			var samples []ledger.SampleRow
			for m := 0; m <= 60; m += 10 {
				at := now.Add(time.Duration(m-60) * time.Minute)
				samples = append(samples,
					ledger.SampleRow{ProviderID: "kimi", WindowName: "5h", At: at, UsedPct: 3, ResetsAt: short},
					ledger.SampleRow{ProviderID: "kimi", WindowName: "weekly", At: at, UsedPct: tt.weeklyUsed - tt.weeklyRate*float64(60-m)/60, ResetsAt: long})
			}
			for _, p := range AttachForecasts(cfg, ps, samples, now) {
				if p.ID != "kimi" {
					continue
				}
				if f := p.Windows[0].Forecast; f == nil || f.PerishableWasteRisk != tt.wantWaste {
					t.Errorf("5h forecast = %+v, want waste risk %v", f, tt.wantWaste)
				}
				if got := EffectiveState(p, 85, now); got != tt.wantState {
					t.Errorf("effective state = %s, want %s", got, tt.wantState)
				}
			}
		})
	}
}

func paced(rate, risk float64, empties bool, reset time.Duration) *forecast.Forecast {
	s := int64(reset.Seconds())
	return &forecast.Forecast{PctPerHour: rate, PerishableWasteRisk: risk, EmptiesBeforeReset: empties, TimeToResetSeconds: &s}
}

func TestWasteCountsOnlyWhatALongerWindowCanStillAbsorb(t *testing.T) {
	ws := []Window{
		{Name: "5h", UsedPct: f(40), Forecast: paced(3, 0.8, false, 2*time.Hour)},
		{Name: "weekly", UsedPct: f(80), Forecast: paced(1, 0.25, false, 48*time.Hour)},
	}
	capWaste(ws, now)
	if got := ws[0].Forecast.PerishableWasteRisk; !near(got, 0.25) {
		t.Errorf("5h waste risk = %v, want 0.25: weekly spare 5 pts x 3 (5h pts per weekly pt) = 15 of the 60 left", got)
	}
	if got := ws[1].Forecast.PerishableWasteRisk; got != 0.25 {
		t.Errorf("weekly waste risk = %v, want it unchanged at 0.25", got)
	}
}

func TestExpiryCountsOnlyTheRoomLongerWindowsLeave(t *testing.T) {
	cfg := baseConfig()
	ps := snapshot(cfg, func(m map[string]*Provider) {
		for id, weekly := range map[string]*forecast.Forecast{"kimi": paced(1, 0.25, false, 48*time.Hour), "agy": paced(1, 0, true, 48*time.Hour)} {
			setWindow(m[id], "5h", 40, now.Add(2*time.Hour))
			setWindow(m[id], "weekly", 80, now.Add(48*time.Hour))
			m[id].Windows[0].Forecast, m[id].Windows[1].Forecast = paced(3, 0.8, false, 2*time.Hour), weekly
		}
	})
	d, err := Select(Request{Job: Job{ID: "j", Role: "gameplay"}, Trigger: TriggerInitial, Now: now, Config: cfg, Providers: ps})
	if err != nil {
		t.Fatal(err)
	}
	k, a := candidate(t, d, "kimi"), candidate(t, d, "agy")
	if want := 0.15 * (1 - 2.0/5) * expiryGain; !near(k.Features.Expiry, want) || k.ExpiryLimitedBy != "weekly" {
		t.Errorf("kimi expiry = %v limited by %q, want %v limited by weekly", k.Features.Expiry, k.ExpiryLimitedBy, want)
	}
	if a.Features.Expiry != 0 || a.ExpiryLimitedBy != "weekly" {
		t.Errorf("agy expiry = %v limited by %q, want 0: its weekly runs out before reset", a.Features.Expiry, a.ExpiryLimitedBy)
	}
}

func TestOutlookCarriesForecastsOntoAFreshSnapshot(t *testing.T) {
	cfg := baseConfig()
	ps := snapshot(cfg, func(m map[string]*Provider) { m["kimi"].Windows[1].Forecast = paced(1, 0, true, time.Hour) })
	fresh := snapshot(cfg, nil)
	OutlookOf(ps).Apply(fresh)
	for _, p := range fresh {
		if got := p.Windows[1].Forecast; (p.ID == "kimi") != (got != nil) {
			t.Errorf("%s weekly forecast = %+v", p.ID, got)
		}
	}
}

func TestTaskSizingFloorOnlyRaisesMinQualityTier(t *testing.T) {
	cfg := baseConfig()
	ps := snapshot(cfg, nil)
	d, err := Select(Request{Job: Job{ID: "j", Role: "gameplay"}, Trigger: TriggerInitial, Now: now, Config: cfg, Providers: ps, MinQualityTier: 5, MinQualityTierReason: "graphify (god nodes Engine)"})
	if err != nil {
		t.Fatal(err)
	}
	if d.MinQualityTier != 5 || d.ChosenProvider != "" || !strings.Contains(d.Summary, "min_quality_tier raised from 3 to 5 by graphify (god nodes Engine).") {
		t.Errorf("raised decision = %s tier %d: %s", d.ChosenProvider, d.MinQualityTier, d.Summary)
	}
	if c := candidate(t, d, "kimi"); c.Status != StatusRejected || !slices.Contains(c.Rejections, "quality_tier 4 < min_quality_tier 5") {
		t.Errorf("kimi = %+v", c)
	}
	d, err = Select(Request{Job: Job{ID: "j", Role: "gameplay"}, Trigger: TriggerInitial, Now: now, Config: cfg, Providers: ps, MinQualityTier: 1, MinQualityTierReason: "graphify"})
	if err != nil {
		t.Fatal(err)
	}
	if d.MinQualityTier != 3 || strings.Contains(d.Summary, "raised") {
		t.Errorf("a lower floor changed the decision: tier %d: %s", d.MinQualityTier, d.Summary)
	}
}

func TestSnapshotShowsOnlyConfiguredWindowsWhenTheConfigListsThem(t *testing.T) {
	cfg := baseConfig()
	sonnet := cfg.Providers["sonnet"]
	sonnet.Windows = []config.Window{{Name: "weekly", Kind: "fixed_reset", DurationHours: 168}}
	cfg.Providers["sonnet"] = sonnet
	kimi := cfg.Providers["kimi"]
	kimi.Windows = nil
	cfg.Providers["kimi"] = kimi
	wins := []ledger.WindowRow{
		{ProviderID: "sonnet", Name: "5h", Kind: "rolling", UsedPct: f(0), SampledAt: now},
		{ProviderID: "sonnet", Name: "weekly", Kind: "fixed_reset", UsedPct: f(86), SampledAt: now},
		{ProviderID: "kimi", Name: "daily", Kind: "fixed_reset", UsedPct: f(12), SampledAt: now},
	}
	names := map[string][]string{}
	for _, p := range Snapshot(cfg, nil, wins) {
		for _, w := range p.Windows {
			names[p.ID] = append(names[p.ID], w.Name)
		}
	}
	if got := names["sonnet"]; len(got) != 1 || got[0] != "weekly" {
		t.Errorf("sonnet windows = %v, want only the configured weekly window", got)
	}
	if got := names["kimi"]; len(got) != 1 || got[0] != "daily" {
		t.Errorf("kimi without configured windows = %v, want the observed daily window", got)
	}
}

func TestEstimateJobBurnWeighsTheNewestMeasurementsByDuration(t *testing.T) {
	var older []ledger.JobBurnRow
	for i, rate := range []float64{9, 1, 5, 3, 7} {
		older = append(older, ledger.JobBurnRow{ProviderID: "kimi", JobBurnMeasured: ledger.JobBurnMeasured{WindowName: "5h", PctPerHour: rate, DurationMin: float64(10 * (i + 1))}})
	}
	var newest []ledger.JobBurnRow
	for range JobBurnWindow {
		newest = append(newest, ledger.JobBurnRow{ProviderID: "kimi", JobBurnMeasured: ledger.JobBurnMeasured{WindowName: "5h", PctPerHour: 100, DurationMin: 999}})
	}
	got := EstimateJobBurn(append(newest, older...))["kimi"]["5h"]
	if got.Samples != JobBurnWindow || got.PctPerHour != 100 {
		t.Fatalf("estimate = %+v, want the newest %d rows only", got, JobBurnWindow)
	}
	got = EstimateJobBurn(older)["kimi"]["5h"]
	if !near(got.PctPerHour, 730.0/150) || got.DurationMin != 30 || got.Samples != 5 {
		t.Fatalf("estimate of 5 rows = %+v, want 4.87%%/h weighted by duration and a median of 30 min", got)
	}
	quantized := []ledger.JobBurnRow{
		{ProviderID: "claude", JobBurnMeasured: ledger.JobBurnMeasured{WindowName: "weekly", PctPerHour: 0, DurationMin: 10}},
		{ProviderID: "claude", JobBurnMeasured: ledger.JobBurnMeasured{WindowName: "weekly", PctPerHour: 0, DurationMin: 10}},
		{ProviderID: "claude", JobBurnMeasured: ledger.JobBurnMeasured{WindowName: "weekly", PctPerHour: 6, DurationMin: 10}},
	}
	if got := EstimateJobBurn(quantized)["claude"]["weekly"]; !near(got.PctPerHour, 2) {
		t.Fatalf("integer-percent steps seen by one job in three = %+v, want 2%%/h, not a median of 0", got)
	}
}

func TestQuotaBrakeCountsTheJobsAlreadyRunningOnThePool(t *testing.T) {
	cfg := baseConfig()
	for _, id := range []string{"kimi", "agy"} {
		p := cfg.Providers[id]
		p.SharedQuotaPool, p.MaxConcurrent = "pool", 0
		cfg.Providers[id] = p
	}
	burn := map[string]map[string]JobBurn{
		"kimi": {"5h": {PctPerHour: 10, DurationMin: 60}},
		"agy":  {"5h": {PctPerHour: 20, DurationMin: 60}},
	}
	decide := func(kimiActive, agyActive int, used float64) Candidate {
		ps := snapshot(cfg, func(m map[string]*Provider) {
			m["kimi"].ActiveJobs, m["agy"].ActiveJobs = kimiActive, agyActive
			setWindow(m["kimi"], "5h", used, now.Add(3*time.Hour))
			setWindow(m["agy"], "5h", used, now.Add(3*time.Hour))
		})
		d, err := Select(Request{Job: Job{ID: "j", Role: "gameplay"}, Trigger: TriggerInitial, Now: now, Config: cfg, Providers: ps, JobBurn: burn})
		if err != nil {
			t.Fatal(err)
		}
		return candidate(t, d, "kimi")
	}
	if c := decide(5, 0, 30); c.Status == StatusRejected {
		t.Errorf("30%% + 6 jobs x 10%%/h x 1h = 90%%, below reserve 98%%: %v", c.Rejections)
	}
	if c := decide(5, 1, 30); c.Status != StatusRejected || !strings.Contains(c.Rejections[0], "quota brake: window 5h at 30.0% with 6 running") {
		t.Errorf("30%% + 5x10 + 1x20 + 10 = 110%%, want a quota brake: %+v", c.Rejections)
	}
	if c := decide(0, 0, 30); c.Status == StatusRejected || c.MaxConcurrent != 0 {
		t.Errorf("an idle provider without max_concurrent must stay eligible: %+v", c)
	}
	delete(burn, "kimi")
	if c := decide(50, 0, 30); c.Status == StatusRejected {
		t.Errorf("without a measurement for kimi there is no brake and no fixed cap: %v", c.Rejections)
	}
}

func TestBacklogRunsOnlyOnQuotaForecastToGoUnused(t *testing.T) {
	cfg := baseConfig()
	cfg.General.BacklogMinUnusedPct, cfg.General.BacklogHorizonHours = 10, 24
	burn := map[string]map[string]JobBurn{"kimi": {"5h": {PctPerHour: 10, DurationMin: 60}}}
	decide := func(backlog bool, kimiActive int, edit func(map[string]*Provider)) Decision {
		ps := snapshot(cfg, func(m map[string]*Provider) {
			m["kimi"].ActiveJobs = kimiActive
			for _, id := range []string{"kimi", "agy", "sonnet"} {
				setWindow(m[id], "5h", 40, now.Add(3*time.Hour))
				setWindow(m[id], "weekly", 10, now.Add(6*24*time.Hour))
			}
			m["kimi"].Windows[0].Forecast = paced(5, 0.5, false, 3*time.Hour)
			m["agy"].Windows[0].Forecast = paced(15, 0.1, false, 3*time.Hour)
			if edit != nil {
				edit(m)
			}
		})
		d, err := Select(Request{Job: Job{ID: "j", Role: "gameplay", Backlog: backlog}, Trigger: TriggerInitial, Now: now, Config: cfg, Providers: ps, JobBurn: burn})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := decide(true, 0, nil)
	if d.ChosenProvider != "kimi" {
		t.Fatalf("60%% left x 0.5 risk = 30%% unused, want kimi chosen: %s", d.Summary)
	}
	if c := candidate(t, d, "agy"); c.Status != StatusRejected || !strings.Contains(c.Rejections[0], "backlog: 5h leaves ~6% unused") {
		t.Errorf("agy wastes 6%% < 10%%, want rejected: %v", c.Rejections)
	}
	if c := candidate(t, d, "sonnet"); c.Status != StatusRejected || !strings.Contains(c.Rejections[0], "no window with a burn forecast resets within 24h") {
		t.Errorf("sonnet has no forecast, want rejected: %v", c.Rejections)
	}
	if d := decide(true, 2, nil); d.ChosenProvider != "kimi" {
		t.Errorf("30%% unused - 2 running x 10%%/h x 1h = 10%%, still enough: %s", d.Summary)
	}
	if c := candidate(t, decide(true, 3, nil), "kimi"); c.Status != StatusRejected {
		t.Errorf("30%% unused - 3 running x 10%%/h x 1h = 0%%, want rejected: %+v", c)
	}
	far := decide(true, 0, func(m map[string]*Provider) {
		m["kimi"].Windows[0].Forecast = nil
		m["kimi"].Windows[1].Forecast = paced(0, 1, false, 6*24*time.Hour)
	})
	if c := candidate(t, far, "kimi"); c.Status != StatusRejected || !strings.Contains(c.Rejections[0], "within 24h") {
		t.Errorf("a weekly window resetting in 6 days is past the horizon: %v", c.Rejections)
	}
	if d := decide(false, 0, nil); candidate(t, d, "sonnet").Status == StatusRejected {
		t.Errorf("a normal job must ignore the backlog rule: %v", candidate(t, d, "sonnet").Rejections)
	}
}
