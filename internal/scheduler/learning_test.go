package scheduler

import (
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

func learningConfig() config.Learning {
	return config.Learning{Enabled: true, MinSamples: 5, MaxDelta: 0.12, EWMAAlpha: 0.25, WeightCompletion: 0.45, WeightReviewRounds: 0.25, WeightBuildFailures: 0.20, WeightDuration: 0.10}
}

func TestOutcomeQualityFollowsDocumentedFormula(t *testing.T) {
	c := learningConfig()
	tests := []struct {
		name     string
		o        Outcome
		roleMin  float64
		expected float64
	}{
		{"clean fast success", Outcome{Success: true, DurationMin: 20}, 30, 1.0},
		{"failure with rework", Outcome{ReviewRounds: 3, BuildFailures: 1, DurationMin: 60}, 30, 0.25/4 + 0.20/2 + 0.10*0.5},
		{"very slow floors the duration term", Outcome{Success: true, DurationMin: 1000}, 30, 0.45 + 0.25 + 0.20 + 0.10*0.2},
		{"unknown duration is neutral", Outcome{Success: true}, 30, 1.0},
		{"no role history is neutral", Outcome{Success: true, DurationMin: 90}, 0, 1.0},
	}
	for _, tt := range tests {
		if got := OutcomeQuality(c, tt.o, tt.roleMin); !near(got, tt.expected) {
			t.Errorf("%s: quality = %.6f, want %.6f", tt.name, got, tt.expected)
		}
	}
}

func TestRoleDurationIsMeanOfKnownDurations(t *testing.T) {
	rows := []ledger.LearningRow{{AvgDurationMin: 20, Samples: 1}, {AvgDurationMin: 40, Samples: 3}, {AvgDurationMin: 0, Samples: 2}}
	if got := RoleDurationMin(rows); got != 30 {
		t.Fatalf("role duration = %v", got)
	}
	if got := RoleDurationMin(nil); got != 0 {
		t.Fatalf("empty role duration = %v", got)
	}
}

func TestLearnDeltasAreZeroBelowMinSamples(t *testing.T) {
	c := learningConfig()
	rows := []ledger.LearningRow{{ProviderID: "kimi", Samples: 4, EWMAQuality: 1}, {ProviderID: "agy", Samples: 5, EWMAQuality: 0.2}}
	got := LearnDeltas(c, rows)
	if got["kimi"] != 0 {
		t.Errorf("kimi below min_samples got %v", got["kimi"])
	}
	if want := (0.2 - 0.6) * 0.12 * 2; !near(got["agy"], want) {
		t.Errorf("agy = %v, want %v against baseline 0.6", got["agy"], want)
	}
}

func TestLearnDeltasMeasureAgainstRoleBaseline(t *testing.T) {
	c := learningConfig()
	rows := []ledger.LearningRow{{ProviderID: "kimi", Samples: 9, EWMAQuality: 0.70}, {ProviderID: "agy", Samples: 9, EWMAQuality: 0.66}}
	got := LearnDeltas(c, rows)
	if one := LearnDeltas(c, rows[:1]); one["kimi"] != 0 {
		t.Errorf("a lone provider is its own baseline, got %v", one["kimi"])
	}
	if c.MaxDelta = 0.01; !near(LearnDeltas(c, rows)["kimi"], 0.01*0.02*2) {
		t.Errorf("max_delta scales the delta")
	}
	c.MaxDelta = 0.12
	if !near(got["kimi"], 0.02*0.12*2) || !near(got["agy"], -0.02*0.12*2) {
		t.Fatalf("deltas = %v", got)
	}
	c.Enabled = false
	if got := LearnDeltas(c, rows); len(got) != 0 {
		t.Fatalf("disabled learning produced %v", got)
	}
}

func TestLearnDeltasNeverExceedMaxDelta(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	for range 5000 {
		c := learningConfig()
		c.MaxDelta = r.Float64() * config.MaxLearnDelta
		c.MinSamples = 1 + r.IntN(6)
		rows := make([]ledger.LearningRow, 1+r.IntN(6))
		for i := range rows {
			rows[i] = ledger.LearningRow{ProviderID: string(rune('a' + i)), Samples: r.IntN(10), EWMAQuality: r.Float64()*4 - 2}
		}
		for id, d := range LearnDeltas(c, rows) {
			if math.Abs(d) > c.MaxDelta+1e-12 || math.IsNaN(d) {
				t.Fatalf("delta %s = %v exceeds ±%v for %+v", id, d, c.MaxDelta, rows)
			}
		}
	}
}

func TestSelectClampsLearnedDeltaIntoTheScore(t *testing.T) {
	cfg := baseConfig()
	cfg.Learning = learningConfig()
	ps := snapshot(cfg, nil)
	req := Request{Job: Job{ID: "j", Role: "gameplay"}, Trigger: TriggerInitial, Now: now, Config: cfg, Providers: ps}
	base, err := Select(req)
	if err != nil {
		t.Fatal(err)
	}
	req.LearnDeltas = map[string]float64{"kimi": 5, "agy": -5}
	got, err := Select(req)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]float64{"kimi": 0.12, "agy": -0.12, "sonnet": 0} {
		c, b := candidate(t, got, id), candidate(t, base, id)
		if !near(c.LearnDelta, want) || !near(c.Score, b.Score+want) {
			t.Errorf("%s: learn=%v score=%v base=%v, want learn %v", id, c.LearnDelta, c.Score, b.Score, want)
		}
	}
	cfg.Learning.Enabled = false
	off, err := Select(req)
	if err != nil {
		t.Fatal(err)
	}
	if c := candidate(t, off, "kimi"); c.LearnDelta != 0 {
		t.Errorf("disabled learning still applied %v", c.LearnDelta)
	}
}

func TestSummaryNamesLearningWhenItDecides(t *testing.T) {
	cfg := baseConfig()
	cfg.Learning = learningConfig()
	cfg.Roles["gameplay"] = config.Role{AllowedProviders: []string{"kimi", "agy"}, MinQualityTier: 3}
	cfg.Providers["agy"] = cfg.Providers["kimi"]
	d, err := Select(Request{Job: Job{ID: "j", Role: "gameplay"}, Trigger: TriggerInitial, Now: now, Config: cfg, Providers: snapshot(cfg, nil), LearnDeltas: map[string]float64{"agy": 0.1}})
	if err != nil {
		t.Fatal(err)
	}
	if d.ChosenProvider != "agy" || !strings.Contains(d.Summary, "largest edge: learned") {
		t.Fatalf("chosen %s, summary %q", d.ChosenProvider, d.Summary)
	}
}

var propertyIDs = []string{"a", "b", "c", "d", "e", "f"}

func randomSubset(r *rand.Rand, ids []string, oneIn int) []string {
	out := []string{}
	for _, i := range r.Perm(len(ids)) {
		if r.IntN(oneIn) == 0 {
			out = append(out, ids[i])
		}
	}
	return out
}

func randomScenario(r *rand.Rand) Request {
	off := false
	cfg := &config.Config{
		SchemaVersion: 1,
		General:       config.General{ActiveProfile: []string{"quality-first", "burn-expiring-first", "save-scarce", "balanced"}[r.IntN(4)], WarnUsedPct: 85, ReserveUsedPct: 98, AllowMeteredFallback: r.IntN(2) == 0},
		Learning:      learningConfig(),
		Providers:     map[string]config.Provider{},
	}
	cfg.Learning.Enabled = r.IntN(5) > 0
	cfg.Learning.MaxDelta = r.Float64() * config.MaxLearnDelta
	for _, id := range propertyIDs {
		p := prov(1+r.IntN(5), 1+r.IntN(5), 1+r.IntN(5), 1+r.IntN(3), r.IntN(3) == 0)
		if r.IntN(6) == 0 {
			p.Enabled = &off
		}
		cfg.Providers[id] = p
	}
	allowed := randomSubset(r, propertyIDs, 2)
	if len(allowed) == 0 {
		allowed = []string{propertyIDs[r.IntN(len(propertyIDs))]}
	}
	role := config.Role{AllowedProviders: allowed, DenyProviders: randomSubset(r, propertyIDs, 4), MinQualityTier: 1 + r.IntN(5)}
	if r.IntN(5) == 0 {
		role.PinProvider = propertyIDs[r.IntN(len(propertyIDs))]
	}
	cfg.Roles = map[string]config.Role{"r": role}
	states := []string{ledger.StateAvailable, ledger.StateAvailable, ledger.StateDegraded, ledger.StatePaused, ledger.StateExhausted, ledger.StateDraining}
	ps := snapshot(cfg, func(m map[string]*Provider) {
		for _, p := range m {
			p.State = states[r.IntN(len(states))]
			p.ActiveJobs = r.IntN(4)
			if r.IntN(2) == 0 {
				setWindow(p, "5h", r.Float64()*100, now.Add(time.Duration(r.IntN(300))*time.Minute))
			}
		}
	})
	job := Job{ID: "j", Role: "r"}
	if r.IntN(5) == 0 {
		job.PinnedProvider = propertyIDs[r.IntN(len(propertyIDs))]
	}
	if r.IntN(5) == 0 {
		job.Exclude = randomSubset(r, propertyIDs, 3)
	}
	learned := map[string]float64{}
	for _, id := range propertyIDs {
		learned[id] = (r.Float64()*2 - 1) * math.Pow(10, float64(r.IntN(4)-1))
	}
	return Request{Job: job, Trigger: []string{TriggerInitial, TriggerFailover}[r.IntN(2)], Now: now, Config: cfg, Providers: ps, LearnDeltas: learned}
}

func passesHardGates(req Request, id string) bool {
	cfg := req.Config
	role := cfg.Roles["r"]
	pin := req.Job.PinnedProvider
	if pin == "" {
		pin = role.PinProvider
	}
	var p Provider
	for _, x := range req.Providers {
		if x.ID == id {
			p = x
		}
	}
	return p.Config.IsEnabled() &&
		slices.Contains(role.AllowedProviders, id) &&
		!slices.Contains(role.DenyProviders, id) &&
		p.Config.QualityTier >= role.MinQualityTier &&
		(pin == "" || pin == id) &&
		(p.Config.CostClass != "metered" || cfg.General.AllowMeteredFallback || pin == id) &&
		p.ActiveJobs < p.Config.MaxConcurrent
}

func TestLearningNeverBreaksAHardGate(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	chosen := 0
	for i := range 5000 {
		req := randomScenario(r)
		got, err := Select(req)
		if err != nil {
			t.Fatal(err)
		}
		plain := req
		plain.LearnDeltas = nil
		base, err := Select(plain)
		if err != nil {
			t.Fatal(err)
		}
		bound := 0.0
		if req.Config.Learning.Enabled {
			bound = req.Config.Learning.MaxDelta
		}
		for j, c := range got.Candidates {
			b := base.Candidates[j]
			if c.Provider != b.Provider || (c.Status == StatusRejected) != (b.Status == StatusRejected) || !slices.Equal(c.Rejections, b.Rejections) {
				t.Fatalf("case %d: learning changed eligibility of %s: %+v vs %+v", i, c.Provider, c, b)
			}
			if math.Abs(c.LearnDelta) > bound+1e-12 {
				t.Fatalf("case %d: %s learn delta %v exceeds ±%v", i, c.Provider, c.LearnDelta, bound)
			}
		}
		if (got.ChosenProvider == "") != (base.ChosenProvider == "") {
			t.Fatalf("case %d: learning changed whether any provider is chosen: %q vs %q", i, got.ChosenProvider, base.ChosenProvider)
		}
		if got.ChosenProvider != "" {
			chosen++
			if !passesHardGates(req, got.ChosenProvider) {
				t.Fatalf("case %d: learning picked %s past a hard gate", i, got.ChosenProvider)
			}
		}
	}
	if chosen < 500 {
		t.Fatalf("only %d of 5000 scenarios chose a provider; generator too strict to exercise gates", chosen)
	}
}
