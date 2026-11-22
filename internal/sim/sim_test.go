package sim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

var fixtureStart = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func testConfig(profile string, maxConcurrent int) *config.Config {
	return &config.Config{
		SchemaVersion: 1,
		General:       config.General{ActiveProfile: profile, WarnUsedPct: 85, ReserveUsedPct: 100},
		Providers: map[string]config.Provider{
			"alpha": {Harness: config.HarnessClaudeCode, CostClass: "subscription", QualityTier: 5, ScarcityTier: 5, SpeedTier: 3, MaxConcurrent: maxConcurrent, Windows: []config.Window{{Name: "5h", Kind: "rolling", DurationHours: 5}, {Name: "weekly", Kind: "fixed_reset", DurationHours: 168}}},
			"beta":  {Harness: config.HarnessKimiNative, CostClass: "subscription", QualityTier: 3, ScarcityTier: 1, SpeedTier: 4, MaxConcurrent: maxConcurrent, Windows: []config.Window{{Name: "5h", Kind: "rolling", DurationHours: 5}, {Name: "weekly", Kind: "fixed_reset", DurationHours: 168}}},
		},
		Roles: map[string]config.Role{"default": {AllowedProviders: []string{"alpha", "beta"}, MinQualityTier: 1}},
	}
}

func appendEvent(t *testing.T, l *ledger.Ledger, ts time.Time, providerID, jobID string, payload ledger.Payload) {
	t.Helper()
	if err := l.Append(context.Background(), ledger.Event{TS: ts, ProviderID: providerID, JobID: jobID, Source: ledger.SourceSensor, Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

func percentage(v float64) *float64 { return &v }

func sample(t *testing.T, l *ledger.Ledger, ts time.Time, providerID, window string, used float64, reset time.Time) {
	t.Helper()
	kind := "rolling"
	if window == "weekly" {
		kind = "fixed_reset"
	}
	appendEvent(t, l, ts, providerID, "", ledger.QuotaSampled{WindowName: window, WindowKind: kind, UsedPct: percentage(used), ResetsAt: reset, Confidence: ledger.ConfidenceExact})
}

func failoverLedger(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "day.db")
	l, err := ledger.Open(context.Background(), path, func() time.Time { return fixtureStart })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	for _, id := range []string{"alpha", "beta"} {
		appendEvent(t, l, fixtureStart.Add(-time.Hour), id, "", ledger.ProviderConfigured{Harness: config.HarnessKimiNative, MaxConcurrent: 1})
	}
	reset := fixtureStart.Add(2 * time.Hour)
	weeklyReset := fixtureStart.Add(72 * time.Hour)
	sample(t, l, fixtureStart.Add(-10*time.Minute), "alpha", "5h", 88, reset)
	sample(t, l, fixtureStart, "alpha", "5h", 98, reset)
	sample(t, l, fixtureStart.Add(-10*time.Minute), "beta", "5h", 0, reset)
	sample(t, l, fixtureStart, "beta", "5h", 10, reset)
	sample(t, l, fixtureStart, "alpha", "weekly", 20, weeklyReset)
	sample(t, l, fixtureStart, "beta", "weekly", 30, weeklyReset)
	appendEvent(t, l, fixtureStart, "", "job_1", ledger.JobQueued{Name: "front-01", Role: "default", WorktreePath: "/front-01", Branch: "front-01", BriefPath: "/front-01/brief.md", TerminalAdapter: "process"})
	appendEvent(t, l, fixtureStart.Add(time.Hour), "", "job_1", ledger.JobCompleted{DurationMin: 60})
	appendEvent(t, l, reset, "alpha", "", ledger.WindowReset{WindowName: "5h", ResetsAtNext: reset.Add(5 * time.Hour)})
	appendEvent(t, l, reset, "beta", "", ledger.WindowReset{WindowName: "5h", ResetsAtNext: reset.Add(5 * time.Hour)})
	return path
}

func TestReplayMetrics(t *testing.T) {
	rows, err := Replay(context.Background(), failoverLedger(t), []Policy{{Name: "quality-first", Config: testConfig("quality-first", 1)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.Policy != "quality-first" || got.FailoverCount != 1 {
		t.Fatalf("metrics = %+v", got)
	}
	assertMetric(t, "perishable used", got.PerishableQuotaUsedPct, 15)
	assertMetric(t, "expired wasted", got.ExpiredWastedQuotaPct, 8)
	assertMetric(t, "stall minutes", got.TotalStallMinutes, 0)
	assertMetric(t, "scarce reserve", got.ScarcePoolReserveLeftPct, 80)
}

func assertMetric(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestReplayCountsQuotaStallUntilReset(t *testing.T) {
	cfg := testConfig("quality-first", 1)
	role := cfg.Roles["default"]
	role.AllowedProviders = []string{"alpha"}
	cfg.Roles["default"] = role
	rows, err := Replay(context.Background(), failoverLedger(t), []Policy{{Name: "quality-first", Config: cfg}})
	if err != nil {
		t.Fatal(err)
	}
	assertMetric(t, "stall minutes", rows[0].TotalStallMinutes, 118)
	if rows[0].FailoverCount != 0 {
		t.Fatalf("failovers = %d, want 0", rows[0].FailoverCount)
	}
}

func TestReplayDeterministic(t *testing.T) {
	path := failoverLedger(t)
	policies := []Policy{{Name: "quality-first", Config: testConfig("quality-first", 1)}, {Name: "save-scarce", Config: testConfig("save-scarce", 1)}}
	first, err := Replay(context.Background(), path, policies)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Replay(context.Background(), path, policies)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("first = %+v, second = %+v", first, second)
	}
	var a, b bytes.Buffer
	if err := Render(&a, "json", first); err != nil {
		t.Fatal(err)
	}
	if err := Render(&b, "json", second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("json differs:\n%s\n%s", a.Bytes(), b.Bytes())
	}
}

func TestRenderFormats(t *testing.T) {
	rows := []Metrics{{Policy: "balanced", PerishableQuotaUsedPct: 42.5, ExpiredWastedQuotaPct: 12.25, TotalStallMinutes: 18, FailoverCount: 3, ScarcePoolReserveLeftPct: 61}}
	var markdown bytes.Buffer
	if err := Render(&markdown, "markdown", rows); err != nil {
		t.Fatal(err)
	}
	want := "| Policy | Perishable quota used (%) | Expired wasted quota (%) | Total stall minutes | Failover count | Scarce-pool reserve left (%) |\n|---|---:|---:|---:|---:|---:|\n| balanced | 42.50 | 12.25 | 18.00 | 3 | 61.00 |\n"
	if markdown.String() != want {
		t.Fatalf("markdown = %q, want %q", markdown.String(), want)
	}
	var csvOut bytes.Buffer
	if err := Render(&csvOut, "csv", rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csvOut.String(), "policy,perishable_quota_used_pct,expired_wasted_quota_pct,total_stall_minutes,failover_count,scarce_pool_reserve_left_pct\n") || !strings.Contains(csvOut.String(), "balanced,42.50,12.25,18.00,3,61.00\n") {
		t.Fatalf("csv = %q", csvOut.String())
	}
	var jsonOut bytes.Buffer
	if err := Render(&jsonOut, "json", rows); err != nil {
		t.Fatal(err)
	}
	var decoded []Metrics
	if err := json.Unmarshal(jsonOut.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, rows) {
		t.Fatalf("json = %+v, want %+v", decoded, rows)
	}
	if err := Render(&bytes.Buffer{}, "yaml", rows); err == nil {
		t.Fatal("unknown format accepted")
	}
}

func TestReplay24Hour12FrontLedgerUnderTwoSeconds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "day.db")
	l, err := ledger.Open(context.Background(), path, func() time.Time { return fixtureStart })
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"alpha", "beta"} {
		appendEvent(t, l, fixtureStart.Add(-2*time.Hour), id, "", ledger.ProviderConfigured{Harness: config.HarnessKimiNative, MaxConcurrent: 3})
		for window := range 2 {
			name := "5h"
			kind := "rolling"
			duration := 5 * time.Hour
			if window == 1 {
				name = "weekly"
				kind = "fixed_reset"
				duration = 72 * time.Hour
			}
			for hour := -1; hour <= 24; hour++ {
				ts := fixtureStart.Add(time.Duration(hour) * time.Hour)
				used := float64((hour+1)*2 + i)
				appendEvent(t, l, ts, id, "", ledger.QuotaSampled{WindowName: name, WindowKind: kind, UsedPct: percentage(used), ResetsAt: ts.Add(duration), Confidence: ledger.ConfidenceExact})
			}
		}
	}
	for front := range 12 {
		for hour := range 24 {
			id := fmt.Sprintf("job_%02d_%02d", front, hour)
			ts := fixtureStart.Add(time.Duration(hour)*time.Hour + time.Duration(front)*time.Minute)
			appendEvent(t, l, ts, "", id, ledger.JobQueued{Name: id, Role: "default", WorktreePath: "/front", Branch: "front", BriefPath: "/brief.md", TerminalAdapter: "process"})
			appendEvent(t, l, ts.Add(30*time.Minute), "", id, ledger.JobCompleted{DurationMin: 30})
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = Replay(context.Background(), path, []Policy{{Name: "balanced", Config: testConfig("balanced", 3)}, {Name: "save-scarce", Config: testConfig("save-scarce", 3)}})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("replay took %s, want under 2s", elapsed)
	}
}

func TestReplaySkipsJobsThatNeverCompleted(t *testing.T) {
	path := failoverLedger(t)
	l, err := ledger.Open(context.Background(), path, func() time.Time { return fixtureStart })
	if err != nil {
		t.Fatal(err)
	}
	appendEvent(t, l, fixtureStart.Add(30*time.Minute), "", "job_running", ledger.JobQueued{Name: "front-02", Role: "default", WorktreePath: "/front-02", Branch: "front-02", BriefPath: "/front-02/brief.md", TerminalAdapter: "process"})
	_ = l.Close()
	rows, err := Replay(context.Background(), path, []Policy{{Name: "quality-first", Config: testConfig("quality-first", 1)}})
	if err != nil {
		t.Fatalf("a live ledger with a running job must replay: %v", err)
	}
	if rows[0].SkippedJobs != 1 || rows[0].FailoverCount != 1 {
		t.Fatalf("metrics = %+v", rows[0])
	}
}
