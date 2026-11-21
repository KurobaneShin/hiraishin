package watch

import (
	"context"
	"io"
	"log/slog"
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
)

const cfgText = `
schema_version = 1
[general]
active_profile = "burn-expiring-first"
[providers.kimi]
harness = "kimi-native"
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
	if err := os.WriteFile(paths.Config, []byte(cfgText), 0o600); err != nil {
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
	ev := f.e.Launch(context.Background(), job, d.DecisionID, d.ChosenProvider, "initial prompt")
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
			f.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(30 * time.Millisecond)
	}
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
