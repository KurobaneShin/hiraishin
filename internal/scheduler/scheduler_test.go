package scheduler

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
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
