package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/engine"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
)

const testConfig = `
schema_version = 1
[general]
active_profile = "burn-expiring-first"
[providers.kimi]
harness = "kimi-native"
quality_tier = 4
scarcity_tier = 2
speed_tier = 4
max_concurrent = 2
[[providers.kimi.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5
[providers.agy]
harness = "antigravity-agy"
quality_tier = 4
scarcity_tier = 3
speed_tier = 4
max_concurrent = 1
[[providers.agy.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5
[roles.default]
allowed_providers = ["kimi", "agy"]
min_quality_tier = 3
`

type harness struct {
	t   *testing.T
	env Env
	out *bytes.Buffer
	err *bytes.Buffer
	now time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	vars := map[string]string{"HOME": dir, "HIRAISHIN_CONFIG": filepath.Join(dir, "p.toml"), "HIRAISHIN_DATA_DIR": filepath.Join(dir, "data")}
	if err := os.WriteFile(vars["HIRAISHIN_CONFIG"], []byte(testConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, out: &bytes.Buffer{}, err: &bytes.Buffer{}, now: time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)}
	h.env = Env{Stdin: strings.NewReader(""), Stdout: h.out, Stderr: h.err, Getenv: func(k string) string { return vars[k] }, Now: func() time.Time { return h.now }}
	return h
}

func (h *harness) run(want int, argv ...string) string {
	h.t.Helper()
	h.out.Reset()
	h.err.Reset()
	if code := Run(context.Background(), argv, h.env); code != want {
		h.t.Fatalf("%v: exit %d want %d\nstdout: %s\nstderr: %s", argv, code, want, h.out, h.err)
	}
	return h.out.String()
}

func (h *harness) engine() *engine.Engine {
	h.t.Helper()
	e, err := engine.Open(context.Background(), config.ResolvePaths(h.env.Getenv), h.env.Now)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = e.Close() })
	return e
}

func (h *harness) decide(jobID string) scheduler.Decision {
	h.t.Helper()
	e := h.engine()
	var d scheduler.Decision
	err := e.Ledger.Update(context.Background(), func(ctx context.Context, tx *ledger.Tx) ([]ledger.Event, error) {
		evs := []ledger.Event{{JobID: jobID, Source: ledger.SourceCLI, Payload: ledger.JobQueued{Name: "persist-all", Role: "gameplay", WorktreePath: "/wt", Branch: "b", BriefPath: "/b.md", TerminalAdapter: "process"}}}
		var dec ledger.Event
		var err error
		d, dec, err = e.Decide(ctx, tx, ledger.JobRow{ID: jobID, Role: "gameplay"}, scheduler.TriggerDryRun, nil)
		return append(evs, dec), err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return d
}

func validate(t *testing.T, schemaFile, doc string) {
	t.Helper()
	c := jsonschema.NewCompiler()
	sch, err := c.Compile(filepath.Join("..", "..", "docs", "schemas", schemaFile))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("%s: %v\n%s", schemaFile, err, doc)
	}
}

func TestPauseDrainResume(t *testing.T) {
	h := newHarness(t)
	if out := h.run(0, "pause", "kimi", "--reason", "test"); !strings.Contains(out, "kimi: PAUSED") {
		t.Fatalf("pause = %q", out)
	}
	d := h.decide("job_a")
	if d.ChosenProvider != "agy" {
		t.Fatalf("paused provider chosen: %s", d.ChosenProvider)
	}
	if out := h.run(0, "resume", "kimi"); !strings.Contains(out, "kimi: AVAILABLE") {
		t.Fatalf("resume = %q", out)
	}
	if out := h.run(0, "drain", "agy"); !strings.Contains(out, "agy: PAUSED") {
		t.Fatalf("idle drain must settle to PAUSED: %q", out)
	}
	h.run(2, "pause", "nope")
	h.run(2, "pause")
}

func TestProfileAndPrefer(t *testing.T) {
	h := newHarness(t)
	if out := h.run(0, "profile"); strings.TrimSpace(out) != "burn-expiring-first" {
		t.Fatalf("profile = %q", out)
	}
	h.run(0, "profile", "set", "save-scarce")
	if out := h.run(0, "profile"); strings.TrimSpace(out) != "save-scarce" {
		t.Fatalf("profile after set = %q", out)
	}
	h.run(2, "profile", "set", "yolo")
	h.run(0, "prefer", "agy", "--ttl", "1h")
	if d := h.decide("job_p"); d.ChosenProvider != "agy" {
		t.Fatalf("prefer ignored: %s", d.Summary)
	}
	h.now = h.now.Add(2 * time.Hour)
	if d := h.decide("job_q"); d.ChosenProvider != "kimi" {
		t.Fatalf("expired prefer still applied: %s", d.Summary)
	}
}

func TestStatusJSONAndText(t *testing.T) {
	h := newHarness(t)
	e := h.engine()
	used := 40.0
	if err := e.Ledger.Append(context.Background(), ledger.Event{ProviderID: "kimi", Source: ledger.SourceSensor, Payload: ledger.QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: &used, ResetsAt: h.now.Add(72 * time.Minute), Confidence: ledger.ConfidenceExact}}); err != nil {
		t.Fatal(err)
	}
	_ = e.Close()
	doc := h.run(0, "status", "--json", "--no-sensors")
	validate(t, "status.schema.json", doc)
	text := h.run(0, "status", "--no-sensors")
	for _, want := range []string{"kimi", "available", "0/2 jobs", "40%", "resets 1h12m", "agy", "profile burn-expiring-first", "hiraishin help routing"} {
		if !strings.Contains(text, want) {
			t.Errorf("status text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Errorf("status wrote ANSI escapes to a non-terminal:\n%q", text)
	}
}

func TestRenderStatusGroupsPoolsAndColorsByThreshold(t *testing.T) {
	high, low := 90.0, 10.0
	shared := []statusWindow{{Name: "5h", UsedPct: &low, Confidence: "exact"}, {Name: "weekly", UsedPct: &high, Confidence: "exact"}}
	doc := statusDoc{Profile: "balanced", Providers: []statusProvider{
		{ID: "glm-a", Pool: "zai", State: "DEGRADED", MaxConcurrent: 2, Windows: shared},
		{ID: "glm-b", Pool: "zai", State: "DEGRADED", MaxConcurrent: 2, Windows: shared, LastError: "usage endpoint: HTTP 500"},
		{ID: "agy", State: "AVAILABLE", MaxConcurrent: 1, Windows: []statusWindow{{Name: "5h", Confidence: "estimated"}}},
	}}
	var plain, color bytes.Buffer
	if err := renderStatus(&plain, doc, 85, 98, palette{}); err != nil {
		t.Fatal(err)
	}
	if err := renderStatus(&color, doc, 85, 98, palette{on: true}); err != nil {
		t.Fatal(err)
	}
	text := plain.String()
	if n := strings.Count(text, "90%"); n != 1 {
		t.Errorf("pool windows must print once, got %d:\n%s", n, text)
	}
	for _, want := range []string{"zai", "glm-a", "glm-b", "degraded", "usage endpoint: HTTP 500", "agy", "?", "estimated", "2 near limit"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(color.String(), "\x1b[33m") || !strings.Contains(color.String(), "\x1b[32m") {
		t.Errorf("want yellow for 90%% and green for 10%%:\n%q", color.String())
	}
}

func TestWhyMatchesDecisionRow(t *testing.T) {
	h := newHarness(t)
	h.run(0, "pause", "agy")
	d := h.decide("job_w")
	raw := h.run(0, "why", "job_w", "--json")
	validate(t, "why.schema.json", raw)
	var doc whyDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	got := doc.Decisions[0]
	if got.DecisionID != d.DecisionID || got.ChosenProvider != "kimi" || got.Summary != d.Summary || len(got.Candidates) != 2 {
		t.Fatalf("why = %+v\nwant %+v", got, d)
	}
	for i := range d.Candidates {
		if got.Candidates[i].Score != d.Candidates[i].Score || strings.Join(got.Candidates[i].Rejections, ";") != strings.Join(d.Candidates[i].Rejections, ";") {
			t.Errorf("candidate %d differs: %+v vs %+v", i, got.Candidates[i], d.Candidates[i])
		}
	}
	text := h.run(0, "why", d.DecisionID)
	for _, want := range []string{"decision " + d.DecisionID, "profile burn-expiring-first", "quality 0.20", "expiring 0.50", "✓ kimi", "picked", "✗ agy", "state=PAUSED", "order +0.030", "Picked kimi", "hiraishin help routing"} {
		if !strings.Contains(text, want) {
			t.Errorf("why text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Errorf("why wrote ANSI escapes to a non-terminal:\n%q", text)
	}
	help := h.run(0, "help", "routing")
	for _, want := range []string{"Slots", "max_concurrent", "reserve_used_pct", "never_switch_mid_task", "shared_quota_pool", "burn-expiring-first", "hiraishin why"} {
		if !strings.Contains(help, want) {
			t.Errorf("help routing missing %q:\n%s", want, help)
		}
	}
	if root := h.run(0, "--help"); !strings.Contains(root, "routing") {
		t.Errorf("root help does not list the routing topic:\n%s", root)
	}
	h.run(1, "why", "job_missing")
}

func TestForecastRunningOutBeforeResetDegradesStatusAndCandidates(t *testing.T) {
	h := newHarness(t)
	e := h.engine()
	reset := h.now.Add(3 * time.Hour)
	var evs []ledger.Event
	for m := 0; m <= 120; m += 10 {
		fast, flat := 25*float64(m)/60, 20.0
		at := h.now.Add(time.Duration(m-120) * time.Minute)
		evs = append(evs,
			ledger.Event{TS: at, ProviderID: "kimi", Source: ledger.SourceSensor, Payload: ledger.QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: &fast, ResetsAt: reset, Confidence: ledger.ConfidenceExact}},
			ledger.Event{TS: at, ProviderID: "agy", Source: ledger.SourceSensor, Payload: ledger.QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: &flat, ResetsAt: reset, Confidence: ledger.ConfidenceExact}})
	}
	if err := e.Ledger.Append(context.Background(), evs...); err != nil {
		t.Fatal(err)
	}
	_ = e.Close()
	raw := h.run(0, "status", "--json", "--no-sensors")
	validate(t, "status.schema.json", raw)
	var doc statusDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, p := range doc.Providers {
		states[p.ID] = p.State
		f := p.Windows[0].Forecast
		if f == nil {
			t.Fatalf("%s: no forecast in %s", p.ID, raw)
		}
		if p.ID == "kimi" && (f.PctPerHour < 24.9 || f.PctPerHour > 25.1 || !f.EmptiesBeforeReset || f.TimeToEmptySeconds == nil || *f.TimeToEmptySeconds != 2*3600) {
			t.Errorf("kimi forecast = %+v", *f)
		}
		if p.ID == "agy" && (f.PctPerHour != 0 || f.EmptiesBeforeReset || f.PerishableWasteRisk != 1) {
			t.Errorf("agy forecast = %+v", *f)
		}
	}
	if states["kimi"] != ledger.StateDegraded || states["agy"] != ledger.StateAvailable {
		t.Fatalf("states = %v, want kimi DEGRADED and agy AVAILABLE", states)
	}
	text := h.run(0, "status", "--no-sensors")
	if !strings.Contains(text, "degraded") || !strings.Contains(text, "empty in 2h") || !strings.Contains(text, "25%/h") {
		t.Errorf("status text lacks the forecast:\n%s", text)
	}
	d := h.decide("job_f")
	if k := candidateOf(t, d, "kimi"); k.State != ledger.StateDegraded || k.DegradedPenalty != scheduler.DegradedPenalty {
		t.Errorf("kimi candidate state = %s penalty = %v, want DEGRADED with penalty", k.State, k.DegradedPenalty)
	}
	if a := candidateOf(t, d, "agy"); a.State != ledger.StateAvailable {
		t.Errorf("agy candidate state = %s, want AVAILABLE", a.State)
	}
}

func candidateOf(t *testing.T, d scheduler.Decision, id string) scheduler.Candidate {
	t.Helper()
	for _, c := range d.Candidates {
		if c.Provider == id {
			return c
		}
	}
	t.Fatalf("no candidate %s in %s", id, d.Summary)
	return scheduler.Candidate{}
}
