package scheduler

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/forecast"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

const (
	SchemaVersion     = 1
	OrderBonusMax     = 0.03
	PreferBonus       = 0.15
	DegradedPenalty   = 0.08
	NeutralExpiry     = 0.50
	expiryGain        = 1.6
	minTimeFrac       = 0.05
	weeklyHours       = 168
	scarcityBaseShare = 0.6
)

const (
	TriggerInitial         = "initial_dispatch"
	TriggerFailover        = "failover"
	TriggerRetryAfterReset = "retry_after_reset"
	TriggerDryRun          = "dry_run"
)

const (
	StatusSelected = "selected"
	StatusEligible = "eligible"
	StatusRejected = "rejected"
)

type Window struct {
	Name          string             `json:"name"`
	Kind          string             `json:"kind"`
	DurationHours float64            `json:"duration_hours"`
	UsedPct       *float64           `json:"used_pct"`
	ResetsAt      time.Time          `json:"resets_at,omitzero"`
	Confidence    string             `json:"confidence"`
	Forecast      *forecast.Forecast `json:"forecast,omitempty"`
}

type Provider struct {
	ID            string
	Config        config.Provider
	State         string
	ActiveJobs    int
	CooldownUntil time.Time
	Windows       []Window
}

type Job struct {
	ID             string
	Role           string
	PinnedProvider string
	Exclude        []string
}

type Prefer struct {
	ProviderID string
	Role       string
}

type Request struct {
	Job                  Job
	Trigger              string
	Now                  time.Time
	Config               *config.Config
	RuntimeProfile       string
	Prefers              []Prefer
	Providers            []Provider
	LearnDeltas          map[string]float64
	MinQualityTier       int
	MinQualityTierReason string
}

type Features struct {
	Quality  float64 `json:"quality"`
	Expiry   float64 `json:"expiry"`
	Scarcity float64 `json:"scarcity"`
	Latency  float64 `json:"latency"`
}

type Candidate struct {
	Provider        string    `json:"provider"`
	Status          string    `json:"status"`
	Rejections      []string  `json:"rejections"`
	State           string    `json:"state"`
	Features        *Features `json:"features"`
	Weighted        *Features `json:"weighted"`
	OrderBonus      float64   `json:"order_bonus"`
	PreferBonus     float64   `json:"prefer_bonus"`
	DegradedPenalty float64   `json:"degraded_penalty"`
	LearnDelta      float64   `json:"learn_delta"`
	Score           float64   `json:"score"`
	ExpiryNeutral   bool      `json:"expiry_neutral"`
	Windows         []Window  `json:"windows"`
	ActiveJobs      int       `json:"active_jobs"`
	MaxConcurrent   int       `json:"max_concurrent"`
}

type Decision struct {
	SchemaVersion    int            `json:"schema_version"`
	DecisionID       string         `json:"decision_id"`
	JobID            string         `json:"job_id"`
	Trigger          string         `json:"trigger"`
	Role             string         `json:"role"`
	MinQualityTier   int            `json:"min_quality_tier"`
	Profile          string         `json:"profile"`
	Weights          config.Weights `json:"weights"`
	ChosenProvider   string         `json:"chosen_provider"`
	Candidates       []Candidate    `json:"candidates"`
	Summary          string         `json:"summary"`
	EarliestResetAt  time.Time      `json:"earliest_reset_at,omitzero"`
	BlockedProviders []string       `json:"blocked_providers"`
	FullProviders    []string       `json:"full_providers,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
}

func Select(req Request) (Decision, error) {
	role, roleName, err := req.Config.Role(req.Job.Role)
	if err != nil {
		return Decision{}, err
	}
	configuredTier := role.MinQualityTier
	role.MinQualityTier = max(role.MinQualityTier, req.MinQualityTier)
	profile := cmp.Or(role.ProfileOverride, req.RuntimeProfile, req.Config.General.ActiveProfile)
	weights, err := req.Config.ProfileWeights(profile)
	if err != nil {
		return Decision{}, err
	}
	d := Decision{
		SchemaVersion: SchemaVersion, JobID: req.Job.ID, Trigger: req.Trigger, Role: roleName,
		MinQualityTier: role.MinQualityTier, Profile: profile, Weights: weights, CreatedAt: req.Now,
		Candidates: []Candidate{}, BlockedProviders: []string{},
	}
	pin := cmp.Or(req.Job.PinnedProvider, role.PinProvider)
	l := newLookup(req, role, roleName)
	for _, p := range orderProviders(req.Providers, l.rank) {
		c := evaluate(req, role, roleName, pin, weights, p, l)
		d.Candidates = append(d.Candidates, c)
		if len(c.Rejections) == 1 && p.ActiveJobs >= p.Config.MaxConcurrent {
			d.FullProviders = append(d.FullProviders, p.ID)
		}
	}
	best := -1
	for i, c := range d.Candidates {
		if c.Status != StatusEligible {
			continue
		}
		if best < 0 || c.Score > d.Candidates[best].Score+1e-12 {
			best = i
		}
	}
	if best >= 0 {
		d.Candidates[best].Status = StatusSelected
		d.ChosenProvider = d.Candidates[best].Provider
	} else {
		d.EarliestResetAt, d.BlockedProviders = blocked(req, l)
		slices.Sort(d.FullProviders)
	}
	d.Summary = summarize(d)
	if role.MinQualityTier > configuredTier {
		d.Summary += fmt.Sprintf(" min_quality_tier raised from %d to %d by %s.", configuredTier, role.MinQualityTier, req.MinQualityTierReason)
	}
	return d, nil
}

type lookup struct {
	rank    map[string]int
	deny    map[string]bool
	exclude map[string]bool
	prefer  map[string]bool
}

func set(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func newLookup(req Request, role config.Role, roleName string) lookup {
	l := lookup{rank: make(map[string]int, len(role.AllowedProviders)), deny: set(role.DenyProviders), exclude: set(req.Job.Exclude), prefer: map[string]bool{}}
	for i, id := range role.AllowedProviders {
		if _, dup := l.rank[id]; !dup {
			l.rank[id] = i
		}
	}
	for _, pr := range req.Prefers {
		if pr.Role == "" || pr.Role == roleName {
			l.prefer[pr.ProviderID] = true
		}
	}
	return l
}

func orderProviders(all []Provider, ranks map[string]int) []Provider {
	rank := func(id string) int {
		if i, ok := ranks[id]; ok {
			return i
		}
		return len(ranks)
	}
	out := slices.Clone(all)
	slices.SortStableFunc(out, func(a, b Provider) int {
		return cmp.Or(cmp.Compare(rank(a.ID), rank(b.ID)), strings.Compare(a.ID, b.ID))
	})
	return out
}

func EffectiveState(p Provider, warnPct float64, now time.Time) string {
	switch p.State {
	case ledger.StateAvailable, ledger.StateDegraded:
		if p.CooldownUntil.After(now) {
			return ledger.StateDegraded
		}
		for _, w := range p.Windows {
			if w.UsedPct != nil && *w.UsedPct >= warnPct {
				return ledger.StateDegraded
			}
		}
		return ledger.StateAvailable
	}
	return p.State
}

func evaluate(req Request, role config.Role, roleName, pin string, w config.Weights, p Provider, l lookup) Candidate {
	cfg := req.Config
	state := EffectiveState(p, cfg.General.WarnUsedPct, req.Now)
	c := Candidate{Provider: p.ID, State: state, Rejections: []string{}, Windows: p.Windows, ActiveJobs: p.ActiveJobs, MaxConcurrent: p.Config.MaxConcurrent}
	reject := func(format string, args ...any) { c.Rejections = append(c.Rejections, fmt.Sprintf(format, args...)) }
	if !p.Config.IsEnabled() {
		reject("disabled in preferences.toml")
	}
	if state != ledger.StateAvailable && state != ledger.StateDegraded {
		reject("state=%s%s", state, exhaustedDetail(p, req.Now))
	}
	if p.CooldownUntil.After(req.Now) {
		reject("cooldown until %s", clock(p.CooldownUntil))
	}
	if p.Config.CostClass == "metered" && !cfg.General.AllowMeteredFallback && pin != p.ID {
		reject("metered and general.allow_metered_fallback = false")
	}
	if pin != "" && pin != p.ID {
		reject("job pinned to %s", pin)
	}
	rank, allowed := l.rank[p.ID]
	if !allowed {
		reject("not in roles.%s.allowed_providers", roleName)
	}
	if l.deny[p.ID] {
		reject("in roles.%s.deny_providers", roleName)
	}
	if p.Config.QualityTier < role.MinQualityTier {
		reject("quality_tier %d < min_quality_tier %d", p.Config.QualityTier, role.MinQualityTier)
	}
	if p.ActiveJobs >= p.Config.MaxConcurrent {
		reject("slots %d/%d full", p.ActiveJobs, p.Config.MaxConcurrent)
	}
	limit := cfg.General.ReserveUsedPct
	if req.Trigger == TriggerFailover && cfg.General.AllowFailoverPastReserve() {
		limit = 100
	}
	for _, win := range p.Windows {
		if win.UsedPct != nil && *win.UsedPct >= limit && state != ledger.StateExhausted {
			reject("window %s %.1f%% used >= reserve %.1f%%", win.Name, *win.UsedPct, limit)
		}
	}
	if l.exclude[p.ID] {
		reject("excluded for this %s", req.Trigger)
	}
	if len(c.Rejections) > 0 {
		c.Status = StatusRejected
		return c
	}
	c.Status = StatusEligible
	f, neutral := features(p, req.Now)
	c.Features, c.ExpiryNeutral = &f, neutral
	c.Weighted = &Features{Quality: w.Quality * f.Quality, Expiry: w.Expiry * f.Expiry, Scarcity: w.Scarcity * f.Scarcity, Latency: w.Latency * f.Latency}
	c.OrderBonus = OrderBonusMax * (1 - float64(rank)/float64(max(1, len(role.AllowedProviders)-1)))
	if l.prefer[p.ID] {
		c.PreferBonus = PreferBonus
	}
	if state == ledger.StateDegraded {
		c.DegradedPenalty = DegradedPenalty
	}
	if bound := cfg.Learning.MaxDelta; cfg.Learning.Enabled && bound > 0 {
		c.LearnDelta = clamp(req.LearnDeltas[p.ID], -bound, bound)
	}
	c.Score = c.Weighted.Quality + c.Weighted.Expiry + c.Weighted.Scarcity + c.Weighted.Latency + c.OrderBonus + c.PreferBonus - c.DegradedPenalty + c.LearnDelta
	return c
}

func features(p Provider, now time.Time) (Features, bool) {
	f := Features{
		Quality: float64(p.Config.QualityTier-1) / 4,
		Latency: float64(p.Config.SpeedTier-1) / 4,
	}
	f.Expiry, f.Scarcity = NeutralExpiry, 1-float64(p.Config.ScarcityTier-1)/4
	neutral := true
	if w, ok := shortestMeasured(p.Windows); ok {
		rem := (100 - *w.UsedPct) / 100
		frac := clamp(w.ResetsAt.Sub(now).Hours()/w.DurationHours, minTimeFrac, 1)
		f.Expiry, neutral = clamp(rem*(1-frac)*expiryGain, 0, 1), false
	}
	for _, w := range p.Windows {
		if w.UsedPct != nil && (w.Name == "weekly" || w.DurationHours >= weeklyHours) {
			f.Scarcity = scarcityBaseShare*f.Scarcity + (1-scarcityBaseShare)*(100-*w.UsedPct)/100
			break
		}
	}
	return f, neutral
}

func shortestMeasured(ws []Window) (Window, bool) {
	var best Window
	found := false
	for _, w := range ws {
		if w.DurationHours <= 0 || (w.Kind != "rolling" && w.Kind != "fixed_reset") || w.UsedPct == nil || w.ResetsAt.IsZero() {
			continue
		}
		if !found || w.DurationHours < best.DurationHours {
			best, found = w, true
		}
	}
	return best, found
}

func clamp(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }

func exhaustedDetail(p Provider, now time.Time) string {
	for _, w := range p.Windows {
		if w.UsedPct != nil && *w.UsedPct >= 100 && w.ResetsAt.After(now) {
			return fmt.Sprintf(" (window %s resets %s)", w.Name, clock(w.ResetsAt))
		}
	}
	return ""
}

func clock(t time.Time) string { return t.UTC().Format("15:04 UTC") }

func blocked(req Request, l lookup) (time.Time, []string) {
	var earliest time.Time
	ids := []string{}
	for _, p := range req.Providers {
		if _, ok := l.rank[p.ID]; !ok {
			continue
		}
		ids = append(ids, p.ID)
		for _, w := range p.Windows {
			if w.UsedPct != nil && *w.UsedPct >= req.Config.General.ReserveUsedPct && w.ResetsAt.After(req.Now) {
				if earliest.IsZero() || w.ResetsAt.Before(earliest) {
					earliest = w.ResetsAt
				}
			}
		}
	}
	slices.Sort(ids)
	return earliest, ids
}

func summarize(d Decision) string {
	if d.ChosenProvider == "" && len(d.FullProviders) > 0 {
		return fmt.Sprintf("No free slot for role %s: %s full; the job starts when one of their jobs finishes.", d.Role, strings.Join(d.FullProviders, ", "))
	}
	if d.ChosenProvider == "" {
		s := fmt.Sprintf("No eligible provider for role %s: %d candidates rejected.", d.Role, len(d.Candidates))
		if !d.EarliestResetAt.IsZero() {
			s += fmt.Sprintf(" Earliest reset %s; the job waits in WAITING_QUOTA.", clock(d.EarliestResetAt))
		}
		return s
	}
	var chosen, runner *Candidate
	for i := range d.Candidates {
		c := &d.Candidates[i]
		switch {
		case c.Status == StatusSelected:
			chosen = c
		case c.Status == StatusEligible && (runner == nil || c.Score > runner.Score):
			runner = c
		}
	}
	s := fmt.Sprintf("Picked %s (score %.3f) under %s.", chosen.Provider, chosen.Score, d.Profile)
	if runner == nil {
		return s + " It was the only eligible provider."
	}
	name, edge := largestEdge(chosen, runner)
	return s + fmt.Sprintf(" Runner-up %s scored %.3f; largest edge: %s (%+.3f).", runner.Provider, runner.Score, name, edge)
}

func largestEdge(a, b *Candidate) (string, float64) {
	parts := []struct {
		name string
		diff float64
	}{
		{"quality", a.Weighted.Quality - b.Weighted.Quality},
		{"expiry", a.Weighted.Expiry - b.Weighted.Expiry},
		{"scarcity", a.Weighted.Scarcity - b.Weighted.Scarcity},
		{"latency", a.Weighted.Latency - b.Weighted.Latency},
		{"order", a.OrderBonus - b.OrderBonus},
		{"prefer", a.PreferBonus - b.PreferBonus},
		{"degraded penalty", b.DegradedPenalty - a.DegradedPenalty},
		{"learned", a.LearnDelta - b.LearnDelta},
	}
	best := parts[0]
	for _, p := range parts[1:] {
		if p.diff > best.diff {
			best = p
		}
	}
	return best.name, best.diff
}
