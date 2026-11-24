package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/sim"
)

func writeSimLedger(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "day.db")
	start := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	l, err := ledger.Open(context.Background(), path, func() time.Time { return start })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	append := func(ts time.Time, provider, job string, payload ledger.Payload) {
		t.Helper()
		if err := l.Append(context.Background(), ledger.Event{TS: ts, ProviderID: provider, JobID: job, Source: ledger.SourceSensor, Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"alpha", "beta"} {
		append(start.Add(-time.Hour), id, "", ledger.ProviderConfigured{Harness: "kimi-native", MaxConcurrent: 1})
		for _, sample := range []struct {
			ts   time.Time
			used float64
		}{{start.Add(-10 * time.Minute), 10}, {start, 20}} {
			used := sample.used
			append(sample.ts, id, "", ledger.QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: &used, ResetsAt: start.Add(2 * time.Hour), Confidence: ledger.ConfidenceExact})
		}
		used := 30.0
		append(start, id, "", ledger.QuotaSampled{WindowName: "weekly", WindowKind: "fixed_reset", UsedPct: &used, ResetsAt: start.Add(72 * time.Hour), Confidence: ledger.ConfidenceExact})
	}
	append(start, "", "job_1", ledger.JobQueued{Name: "front-01", Role: "default", WorktreePath: "/front-01", Branch: "front-01", BriefPath: "/brief.md", TerminalAdapter: "process"})
	append(start.Add(30*time.Minute), "", "job_1", ledger.JobCompleted{DurationMin: 30})
	append(start.Add(2*time.Hour), "alpha", "", ledger.WindowReset{WindowName: "5h", ResetsAtNext: start.Add(7 * time.Hour)})
	return path
}

func writePolicy(t *testing.T, dir, profile string) string {
	t.Helper()
	path := filepath.Join(dir, profile+".toml")
	body := fmt.Sprintf(`schema_version = 1
[general]
active_profile = %q
reserve_used_pct = 100
[learning]
enabled = false
frozen = true
[providers.alpha]
harness = "claude-code"
cost_class = "subscription"
quality_tier = 5
scarcity_tier = 5
speed_tier = 3
max_concurrent = 1
[[providers.alpha.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5
[[providers.alpha.windows]]
name = "weekly"
kind = "fixed_reset"
duration_hours = 168
[providers.beta]
harness = "kimi-native"
cost_class = "subscription"
quality_tier = 3
scarcity_tier = 1
speed_tier = 4
max_concurrent = 1
[[providers.beta.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5
[[providers.beta.windows]]
name = "weekly"
kind = "fixed_reset"
duration_hours = 168
[roles.default]
allowed_providers = ["alpha", "beta"]
min_quality_tier = 1
`, profile)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSimCommand(t *testing.T) {
	ledgerPath := writeSimLedger(t)
	dir := t.TempDir()
	policies := writePolicy(t, dir, "quality-first") + "," + writePolicy(t, dir, "save-scarce")
	run := func(format string) (int, string, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := Run(context.Background(), []string{"sim", "--ledger", ledgerPath, "--policies", policies, "--format", format}, Env{Stdout: &out, Stderr: &errOut, Getenv: func(string) string { return "" }})
		return code, out.String(), errOut.String()
	}
	code, out, errOut := run("json")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
	var rows []sim.Metrics
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Policy != "quality-first" || rows[1].Policy != "save-scarce" {
		t.Fatalf("rows = %+v", rows)
	}
	code, out, errOut = run("markdown")
	if code != 0 || !strings.Contains(out, "| Policy | Perishable quota used (%)") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
	code, out, errOut = run("csv")
	if code != 0 || !strings.Contains(out, "policy,perishable_quota_used_pct") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}

func TestSimCommandUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"sim", "--ledger", "day.db", "--policies", "one.toml"}, Env{Stdout: &out, Stderr: &errOut, Getenv: func(string) string { return "" }})
	if code != ExitUsage || !strings.Contains(errOut.String(), "at least two policies") {
		t.Fatalf("code = %d, stderr = %q", code, errOut.String())
	}
}
