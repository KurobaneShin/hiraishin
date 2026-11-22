package watch

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/dispatch"
	"github.com/KurobaneShin/hiraishin/internal/engine"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/sensor"
	"github.com/KurobaneShin/hiraishin/internal/stack/stacktest"
)

const cfgText = `
schema_version = 1
[general]
active_profile = "burn-expiring-first"
[providers.kimi]
harness = "kimi-native"
api_key_env = "KIMI_TEST_KEY"
quality_tier = 4
scarcity_tier = 2
speed_tier = 4
[[providers.kimi.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5
[providers.agy]
harness = "antigravity-agy"
quality_tier = 4
scarcity_tier = 3
speed_tier = 4
max_concurrent = 2
[[providers.agy.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5
[roles.default]
allowed_providers = ["kimi", "agy"]
min_quality_tier = 3
[roles.strict]
allowed_providers = ["kimi", "agy"]
min_quality_tier = 3
never_switch_mid_task = true
`

const kimiLimitOnce = `if [ -f .kimi-limited ]; then echo resumed-on-kimi > kimi-done.txt; exit 0; fi
touch .kimi-limited
echo "package x" > dirty.go
echo "429 You've reached your 5-hour usage limit" >&2
exec sleep 30
`

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fixture struct {
	t     *testing.T
	e     *engine.Engine
	w     *Watcher
	clock *clock
}

func setup(t *testing.T, bins map[string]string) *fixture {
	t.Helper()
	return setupWith(t, cfgText, bins)
}

func setupWith(t *testing.T, cfg string, bins map[string]string) *fixture {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range bins {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/usr/bin/env bash\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	paths := config.Paths{Config: filepath.Join(dir, "p.toml"), DataDir: filepath.Join(dir, "data")}
	if err := os.WriteFile(paths.Config, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Now().UTC()}
	e, err := engine.Open(context.Background(), paths, c.now)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, e: e, clock: c, w: New(e, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), 2*time.Second)}
	t.Cleanup(func() {
		jobs, _ := e.Ledger.Reader().Jobs(context.Background())
		for _, j := range jobs {
			atts, _ := e.Ledger.Reader().Attempts(context.Background(), j.ID)
			for _, a := range atts {
				_ = dispatch.Stop(context.Background(), a.Dir, time.Second)
			}
		}
		_ = e.Close()
	})
	return f
}

func (f *fixture) submit(role string) ledger.JobRow {
	f.t.Helper()
	wt := f.t.TempDir()
	if out, err := exec.CommandContext(context.Background(), "git", "-C", wt, "init", "-q").CombinedOutput(); err != nil {
		f.t.Fatalf("git init: %v %s", err, out)
	}
	report := filepath.Join(wt, "report.md")
	if err := os.WriteFile(report, []byte("- [x] step 1 done\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	job := ledger.JobRow{ID: ledger.NewID("job"), Name: "persist-all", Role: role, WorktreePath: wt, BriefPath: filepath.Join(wt, "brief.md"), ReportPath: report, TerminalAdapter: "process"}
	role2, _, _ := f.e.Cfg.Role(role)
	job.NeverSwitch = role2.NeverSwitchMidTask
	d, err := f.e.Queue(context.Background(), job)
	if err != nil {
		f.t.Fatal(err)
	}
	if d.ChosenProvider == "" {
		return job
	}
	ev := f.e.Launch(context.Background(), job, d.DecisionID, d.ChosenProvider, "initial prompt", "")
	if err := f.e.Ledger.Append(context.Background(), ev); err != nil {
		f.t.Fatal(err)
	}
	return job
}

func (f *fixture) job(id string) (ledger.JobRow, []ledger.AttemptRow) {
	f.t.Helper()
	r := f.e.Ledger.Reader()
	j, err := r.Job(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	a, err := r.Attempts(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return j, a
}

func (f *fixture) tickUntil(what string, done func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := f.w.Tick(context.Background()); err != nil {
			f.t.Fatal(err)
		}
		if done() {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %s\n%s", what, f.dump())
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (f *fixture) dump() string {
	var b strings.Builder
	jobs, _ := f.e.Ledger.Reader().Jobs(context.Background())
	for _, j := range jobs {
		fmt.Fprintf(&b, "job %s state=%s provider=%s\n", j.ID, j.State, j.CurrentProvider)
		atts, _ := f.e.Ledger.Reader().Attempts(context.Background(), j.ID)
		for _, a := range atts {
			out, _ := os.ReadFile(filepath.Join(a.Dir, dispatch.OutputFile))
			pg, _ := os.ReadFile(filepath.Join(a.Dir, dispatch.PgidFile))
			code, done, _ := dispatch.ExitCode(a.Dir)
			fmt.Fprintf(&b, "  attempt %d %s exit_reason=%q ended=%v pgid=%q alive=%v exit=%d/%v output=%q\n", a.Num, a.ProviderID, a.ExitReason, !a.EndedAt.IsZero(), pg, dispatch.Alive(a.Dir), code, done, out)
		}
	}
	return b.String()
}

func (f *fixture) state(id string) func() bool {
	return func() bool { j, _ := f.job(id); return j.State == ledger.JobCompletedState }
}

func TestFailoverPreservesWorktreeAndCompletes(t *testing.T) {
	f := setup(t, map[string]string{
		"kimi": kimiLimitOnce,
		"agy":  `printf '%s' "${@: -1}" > resume-prompt.txt; echo "- [x] step 2 done" >> report.md`,
	})
	job := f.submit("default")
	_, atts := f.job(job.ID)
	if len(atts) != 1 || atts[0].ProviderID != "kimi" {
		t.Fatalf("first attempt = %+v", atts)
	}
	kimiDir := atts[0].Dir
	f.tickUntil("COMPLETED", f.state(job.ID))
	if dispatch.Alive(kimiDir) {
		t.Error("kimi agent still running after failover")
	}
	got, atts := f.job(job.ID)
	if len(atts) != 2 || atts[0].ExitReason != "limit_hit" || atts[1].ProviderID != "agy" || got.FailoverCount != 1 {
		t.Fatalf("attempts = %+v failovers = %d", atts, got.FailoverCount)
	}
	if b, err := os.ReadFile(filepath.Join(job.WorktreePath, "dirty.go")); err != nil || string(b) != "package x\n" {
		t.Fatalf("dirty file lost: %q %v", b, err)
	}
	prompt, _ := os.ReadFile(filepath.Join(job.WorktreePath, "resume-prompt.txt"))
	for _, want := range []string{"previous agent (kimi) stopped: 429 You've reached your 5-hour usage limit", "?? dirty.go", "- [x] step 1 done", "## git status --porcelain"} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("resume prompt missing %q:\n%s", want, prompt)
		}
	}
	kimi, _ := f.e.Ledger.Reader().Provider(context.Background(), "kimi")
	if kimi.State != ledger.StateExhausted || kimi.ActiveJobs != 0 {
		t.Errorf("kimi = %+v", kimi)
	}
	decs, _ := f.e.Ledger.Reader().Decisions(context.Background(), job.ID)
	if len(decs) != 2 || decs[1].Trigger != "failover" || decs[1].ChosenProvider != "agy" {
		t.Errorf("decisions = %+v", decs)
	}
}

func TestNeverSwitchWaitsAndRunningJobsStayOnReset(t *testing.T) {
	f := setup(t, map[string]string{"kimi": kimiLimitOnce, "agy": "exec sleep 30"})
	strict := f.submit("strict")
	f.tickUntil("WAITING_QUOTA", func() bool { j, _ := f.job(strict.ID); return j.State == ledger.JobWaitingQuotaState })
	running := f.submit("default")
	r, ratts := f.job(running.ID)
	if r.State != ledger.JobRunningState || ratts[0].ProviderID != "agy" {
		t.Fatalf("new job while kimi exhausted = %s on %+v", r.State, ratts)
	}
	for range 3 {
		if _, err := f.w.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if j, _ := f.job(strict.ID); j.State != ledger.JobWaitingQuotaState {
		t.Fatalf("never_switch job left WAITING_QUOTA before reset: %s", j.State)
	}
	f.clock.add(5*time.Hour + time.Minute)
	f.tickUntil("strict job completed on kimi", f.state(strict.ID))
	_, satts := f.job(strict.ID)
	if len(satts) != 2 || satts[1].ProviderID != "kimi" {
		t.Fatalf("strict attempts = %+v", satts)
	}
	r, ratts = f.job(running.ID)
	if r.State != ledger.JobRunningState || len(ratts) != 1 || r.CurrentProvider != "agy" {
		t.Fatalf("running job switched after reset: %s %+v", r.State, ratts)
	}
}

func TestDegradedDoesNotFailOver(t *testing.T) {
	f := setup(t, map[string]string{"kimi": `echo "Error: 429 Too Many Requests" >&2; echo ok`})
	job := f.submit("default")
	f.tickUntil("COMPLETED", f.state(job.ID))
	_, atts := f.job(job.ID)
	kimi, _ := f.e.Ledger.Reader().Provider(context.Background(), "kimi")
	if len(atts) != 1 || kimi.State == ledger.StateExhausted || kimi.CooldownUntil.IsZero() {
		t.Fatalf("attempts = %d kimi = %+v", len(atts), kimi)
	}
}

func TestNonZeroExitFailsJob(t *testing.T) {
	f := setup(t, map[string]string{"kimi": "exit 4"})
	job := f.submit("default")
	f.tickUntil("FAILED", func() bool { j, _ := f.job(job.ID); return j.State == ledger.JobFailedState })
	_, atts := f.job(job.ID)
	if atts[0].ExitReason != "process_error" {
		t.Errorf("exit reason = %q", atts[0].ExitReason)
	}
}

func TestSessionIDRecordedFromAgentOutput(t *testing.T) {
	f := setup(t, map[string]string{"kimi": `echo '{"type":"system","subtype":"init","session_id":"sess-kimi-1"}'; echo '{"type":"result","session_id":"sess-kimi-1"}'`})
	job := f.submit("default")
	f.tickUntil("COMPLETED", f.state(job.ID))
	if got, _ := f.job(job.ID); got.NativeSessionID != "sess-kimi-1" {
		t.Fatalf("native_session_id = %q", got.NativeSessionID)
	}
}

func TestSessionIDFollowsFailover(t *testing.T) {
	f := setup(t, map[string]string{
		"kimi": "echo '{\"session_id\":\"sess-kimi\"}'\n" + kimiLimitOnce,
		"agy":  `echo '{"type":"step_start","sessionID":"ses_agy"}'`,
	})
	job := f.submit("default")
	f.tickUntil("COMPLETED", f.state(job.ID))
	if got, _ := f.job(job.ID); got.NativeSessionID != "ses_agy" {
		t.Fatalf("native_session_id = %q", got.NativeSessionID)
	}
}

func TestPollSkipsProvidersWithFreshLedgerSamples(t *testing.T) {
	f := setup(t, nil)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"usages":{"limit_5h":{"used_ratio":0.25,"reset_time":"2026-11-22T05:00:00Z"},"limit_7d":{"used_ratio":0.5,"reset_time":"2026-11-25T05:00:00Z"}}}`))
	}))
	defer srv.Close()
	probe := &sensor.Probe{Client: srv.Client(), Getenv: func(k string) string { return map[string]string{"KIMI_TEST_KEY": "k"}[k] },
		Home: t.TempDir(), Now: f.clock.now, URLs: map[sensor.Family]string{sensor.FamilyKimi: srv.URL}}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := New(f.e, probe, quiet, time.Second).Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("first poll hits = %d, want 1", hits)
	}
	if err := New(f.e, probe, quiet, time.Second).Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("a new watcher re-polled a provider sampled moments ago: hits = %d", hits)
	}
	f.clock.add(121 * time.Second)
	if err := New(f.e, probe, quiet, time.Second).Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("stale sample was not refreshed: hits = %d", hits)
	}
	wins, _ := f.e.Ledger.Reader().Windows(context.Background())
	got := map[string]float64{}
	for _, w := range wins {
		if w.ProviderID == "kimi" && w.UsedPct != nil {
			got[w.Name] = *w.UsedPct
		}
	}
	if got["5h"] != 25 || got["weekly"] != 50 {
		t.Fatalf("kimi windows = %v", got)
	}
}

const kimiLimitWithSession = `if [ "$5" = "--session" ]; then echo "$6" > resumed-session.txt; printf '%s' "$2" > continue-prompt.txt; exit 0; fi
echo '{"session_id":"sess-kimi"}'
echo "429 You've reached your 5-hour usage limit" >&2
exec sleep 30
`

func TestFailoverWaitsForANearResetAndResumesTheNativeSession(t *testing.T) {
	f := setupWith(t, strings.Replace(cfgText, "[general]\n", "[general]\nfailover_wait_max_min = 360\n", 1), map[string]string{
		"kimi": kimiLimitWithSession,
		"agy":  "touch agy-ran",
	})
	job := f.submit("default")
	f.tickUntil("WAITING_QUOTA", func() bool { j, _ := f.job(job.ID); return j.State == ledger.JobWaitingQuotaState })
	for range 3 {
		if _, err := f.w.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if j, atts := f.job(job.ID); j.State != ledger.JobWaitingQuotaState || len(atts) != 1 {
		t.Fatalf("job failed over instead of waiting for kimi's reset: %s %+v", j.State, atts)
	}
	f.clock.add(5*time.Hour + time.Minute)
	f.tickUntil("COMPLETED", f.state(job.ID))
	_, atts := f.job(job.ID)
	if len(atts) != 2 || atts[1].ProviderID != "kimi" {
		t.Fatalf("attempts = %+v", atts)
	}
	if b, _ := os.ReadFile(filepath.Join(job.WorktreePath, "resumed-session.txt")); string(b) != "sess-kimi\n" {
		t.Errorf("resumed session = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(job.WorktreePath, "continue-prompt.txt")); !strings.Contains(string(b), "quota has reset") {
		t.Errorf("continue prompt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(job.WorktreePath, "agy-ran")); err == nil {
		t.Error("agy ran although kimi's reset was within failover_wait_max_min")
	}
}

func TestFailoverPastTheWaitPointsTheNextAgentAtTheOldSession(t *testing.T) {
	f := setup(t, map[string]string{
		"kimi": kimiLimitWithSession,
		"agy":  `printf '%s' "${@: -1}" > resume-prompt.txt`,
	})
	job := f.submit("default")
	f.tickUntil("COMPLETED", f.state(job.ID))
	_, atts := f.job(job.ID)
	if len(atts) != 2 || atts[1].ProviderID != "agy" {
		t.Fatalf("attempts = %+v", atts)
	}
	prompt, _ := os.ReadFile(filepath.Join(job.WorktreePath, "resume-prompt.txt"))
	if !strings.Contains(string(prompt), "memory_read_session_observations (session_id sess-kimi, order desc)") {
		t.Errorf("resume prompt lacks the ai-memory pointer:\n%s", prompt)
	}
}

func TestTickReloadsPreferencesWhenTheFileChanges(t *testing.T) {
	f := setup(t, nil)
	path := f.e.Paths.Config
	rewrite := func(body string, mtime time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	tick := func() {
		t.Helper()
		if _, err := f.w.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	rewrite(cfgText+"[roles.build]\nallowed_providers = [\"agy\"]\nmin_quality_tier = 4\n", time.Now().Add(time.Minute))
	tick()
	if _, ok := f.e.Cfg.Roles["build"]; !ok {
		t.Fatal("watch kept the old preferences after the file changed")
	}
	rewrite("not toml [", time.Now().Add(2*time.Minute))
	tick()
	if _, ok := f.e.Cfg.Roles["build"]; !ok {
		t.Fatal("an invalid preferences file replaced the loaded config")
	}
}

func TestCompletedJobFeedsLearning(t *testing.T) {
	f := setup(t, map[string]string{"kimi": "exit 0"})
	job := f.submit("default")
	f.tickUntil("COMPLETED", f.state(job.ID))
	rows, err := f.e.Ledger.Reader().LearningStats(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ProviderID != "kimi" || rows[0].Samples != 1 || rows[0].CompletedOK != 1 || rows[0].EWMAQuality <= 0 {
		t.Fatalf("learning stats after completion = %+v", rows)
	}
}

func TestFailedJobFeedsLearningAsNegativeOutcome(t *testing.T) {
	f := setup(t, map[string]string{"kimi": "exit 4"})
	job := f.submit("default")
	f.tickUntil("FAILED", func() bool { j, _ := f.job(job.ID); return j.State == ledger.JobFailedState })
	rows, err := f.e.Ledger.Reader().LearningStats(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ProviderID != "kimi" || rows[0].Samples != 1 || rows[0].CompletedOK != 0 {
		t.Fatalf("learning stats after failure = %+v", rows)
	}
}

const geminiCfgText = `
schema_version = 1
[general]
active_profile = "balanced"
[providers.gemini]
harness = "gemini-cli"
quality_tier = 4
scarcity_tier = 2
speed_tier = 4
[[providers.gemini.windows]]
name = "daily_rpd"
kind = "fixed_reset"
duration_hours = 24
fixed_reset_cron = "0 0 * * *"
fixed_reset_tz = "America/Los_Angeles"
[roles.default]
allowed_providers = ["gemini"]
min_quality_tier = 2
`

func TestGeminiSingle429BurstDoesNotBenchUntilMidnightPT(t *testing.T) {
	f := setupWith(t, geminiCfgText, map[string]string{
		"gemini": `echo '{"type":"error","message":"429 RESOURCE_EXHAUSTED: Rate limit exceeded"}' >&2; echo '{"type":"result","status":"success"}'`,
	})
	f.clock.t = time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	probeCalls := 0
	f.w.probe = &sensor.Probe{
		Home: t.TempDir(),
		Now:  f.clock.now,
		Run: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			probeCalls++
			return []byte(`{"type":"result","status":"success"}` + "\n"), nil
		},
	}
	job := f.submit("default")
	f.tickUntil("COMPLETED", f.state(job.ID))
	p, err := f.e.Ledger.Reader().Provider(context.Background(), "gemini")
	if err != nil {
		t.Fatal(err)
	}
	if p.State != ledger.StateDegraded || !p.CooldownUntil.Equal(f.clock.now().Add(sensor.GeminiRPMCooldown)) {
		t.Fatalf("after single 429 burst: provider = %+v, want DEGRADED with 65s cooldown", p)
	}
	if probeCalls != 0 {
		t.Fatalf("step-2 probe ran before 65s cooldown: %d", probeCalls)
	}
	f.clock.add(sensor.GeminiRPMCooldown)
	if _, err := f.w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if probeCalls != 1 {
		t.Fatalf("step-2 probe calls = %d, want 1", probeCalls)
	}
	p, _ = f.e.Ledger.Reader().Provider(context.Background(), "gemini")
	if p.State != ledger.StateAvailable {
		t.Fatalf("after clean 65s probe: provider = %+v, want AVAILABLE", p)
	}
}

func TestGeminiPersistent429After65sBenchesUntilMidnightPT(t *testing.T) {
	f := setupWith(t, geminiCfgText, map[string]string{
		"gemini": `echo '{"type":"error","message":"429 RESOURCE_EXHAUSTED: You exceeded your current quota"}' >&2; echo '{"type":"result","status":"success"}'`,
	})
	f.clock.t = time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	f.w.probe = &sensor.Probe{
		Home: t.TempDir(),
		Now:  f.clock.now,
		Run: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(`{"type":"error","message":"429 RESOURCE_EXHAUSTED: You exceeded your current quota"}` + "\n"), fmt.Errorf("exit 1")
		},
	}
	job := f.submit("default")
	f.tickUntil("COMPLETED", f.state(job.ID))
	p, _ := f.e.Ledger.Reader().Provider(context.Background(), "gemini")
	if p.State != ledger.StateDegraded {
		t.Fatalf("before 65s confirmation: state = %s, want DEGRADED", p.State)
	}
	f.clock.add(sensor.GeminiRPMCooldown)
	if _, err := f.w.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, _ = f.e.Ledger.Reader().Provider(context.Background(), "gemini")
	if p.State != ledger.StateExhausted {
		t.Fatalf("after persistent 429 at 65s: state = %s, want EXHAUSTED", p.State)
	}
	wins, _ := f.e.Ledger.Reader().Windows(context.Background())
	wantReset := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	found := false
	for _, w := range wins {
		if w.ProviderID == "gemini" && w.Name == "daily_rpd" {
			found = true
			if w.UsedPct == nil || *w.UsedPct != 100 || !w.ResetsAt.Equal(wantReset) {
				t.Errorf("daily_rpd window = %+v, want 100%% reset %v", w, wantReset)
			}
		}
	}
	if !found {
		t.Fatal("daily_rpd window not recorded")
	}
}

const kimiLimitMidSave = `mkdir -p store && echo "package store" > store/save.go
echo '{"session_id":"sess-kimi"}'
echo "429 You've reached your 5-hour usage limit" >&2
exec sleep 30
`

const watchGraph = `{"nodes": [
  {"id": "save", "label": "SaveAll", "community": 0, "community_name": "Persistence", "source_file": "store/save.go"},
  {"id": "tick", "label": "Tick", "community": 1, "community_name": "Game loop", "source_file": "game/tick.go"}
], "links": [{"source": "tick", "target": "save", "relation": "calls"}]}`

func TestFailoverPromptCarriesStackContextAndRecordsTheOutcome(t *testing.T) {
	mem := stacktest.NewMemory(t, `{"observations": [{"kind": "stop", "created_at": "2026-11-21T10:03:00Z", "body": "Stopped while writing store/save.go"}]}`)
	stacktest.InstallQMD(t, `[{"file": "qmd://orca/game/docs/save.md", "line": 3, "title": "Saving", "score": 0.8, "snippet": "Saves are append-only."}]`)
	cfg := cfgText + fmt.Sprintf("[integrations.ai_memory]\nenabled = true\nurl = %q\nrecord_outcomes = true\n[integrations.qmd]\nenabled = true\ncollections = [\"orca\"]\n[integrations.graphify]\nenabled = true\n", mem.URL)
	f := setupWith(t, cfg, map[string]string{
		"kimi": kimiLimitMidSave,
		"agy":  `printf '%s' "${@: -1}" > resume-prompt.txt`,
	})
	job := f.submit("default")
	stacktest.WriteGraph(t, job.WorktreePath, watchGraph)
	f.tickUntil("COMPLETED", f.state(job.ID))
	prompt, _ := os.ReadFile(filepath.Join(job.WorktreePath, "resume-prompt.txt"))
	for _, want := range []string{
		`<previous-session-recovery session="sess-kimi">`, "stop: Stopped while writing store/save.go",
		"<related-docs>", "qmd://orca/game/docs/save.md:3 (Saving, score 0.80)",
		"<codebase-subgraph>", "Tick (game/tick.go) calls SaveAll (store/save.go)",
		"Inspect the diff, continue the brief",
	} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("resume prompt missing %q:\n%s", want, prompt)
		}
	}
	var page *stacktest.Call
	for _, c := range mem.Calls() {
		if c.Tool == "memory_write_page" {
			page = &c
		}
	}
	if page == nil {
		t.Fatalf("no outcome page written: %+v", mem.Calls())
	}
	path, _ := page.Args["path"].(string)
	body, _ := page.Args["body"].(string)
	if !strings.HasPrefix(path, "hiraishin/outcomes/") || !strings.HasSuffix(path, "-persist-all-"+job.ID+".md") || page.Args["tier"] != "episodic" || page.Args["kind"] != "hiraishin-outcome" {
		t.Errorf("outcome page args = %+v", page.Args)
	}
	for _, want := range []string{"# Outcome: persist-all\n", "- Result: COMPLETED", "1. kimi: limit_hit", "2. agy: completed", "- failover: Picked agy"} {
		if !strings.Contains(body, want) {
			t.Errorf("outcome body missing %q:\n%s", want, body)
		}
	}
	tags := fmt.Sprint(page.Args["tags"])
	if !strings.Contains(tags, "provider:kimi") || !strings.Contains(tags, "provider:agy") || !strings.Contains(tags, "role:default") {
		t.Errorf("tags = %s", tags)
	}
}

func TestWindowResetHonoursTheFixedResetCron(t *testing.T) {
	p := config.Provider{Windows: []config.Window{{Name: "daily", Kind: "fixed_reset", DurationHours: 24, FixedResetCron: "0 3 * * *", FixedResetTZ: "UTC"}}}
	now := time.Date(2026, 11, 22, 1, 0, 0, 0, time.UTC)
	if _, at := windowReset(p, "daily", sensor.Signal{}, now); !at.Equal(time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("reset = %v, want 03:00 UTC from the cron", at)
	}
}

func TestCompletedJobRecordsItsBurnPerWindow(t *testing.T) {
	f := setup(t, map[string]string{"kimi": `while [ ! -f go ]; do sleep 0.05; done`})
	job := f.submit("default")
	_, atts := f.job(job.ID)
	if len(atts) != 1 {
		t.Fatalf("attempts = %d, want 1\n%s", len(atts), f.dump())
	}
	start := atts[0].StartedAt
	reset := start.Add(4 * time.Hour)
	var evs []ledger.Event
	for i := range 10 {
		used := 10 + float64(i)
		evs = append(evs, ledger.Event{TS: start.Add(time.Minute + time.Duration(i)*2*time.Minute), ProviderID: "kimi", Source: ledger.SourceSensor,
			Payload: ledger.QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: &used, ResetsAt: reset, Confidence: ledger.ConfidenceExact}})
	}
	if err := f.e.Ledger.Append(context.Background(), evs...); err != nil {
		t.Fatal(err)
	}
	f.clock.add(20 * time.Minute)
	if err := os.WriteFile(filepath.Join(job.WorktreePath, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.tickUntil("COMPLETED", f.state(job.ID))
	burns, err := f.e.Ledger.Reader().JobBurns(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(burns) != 1 || burns[0].ProviderID != "kimi" || burns[0].WindowName != "5h" || burns[0].PctPerHour < 29.99 || burns[0].PctPerHour > 30.01 || burns[0].DurationMin < 19.99 {
		t.Fatalf("job burns = %+v, want one 5h measurement of 30%%/h over 20 min", burns)
	}
}
