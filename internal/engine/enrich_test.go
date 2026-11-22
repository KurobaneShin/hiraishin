package engine_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/dispatch"
	"github.com/KurobaneShin/hiraishin/internal/engine"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
	"github.com/KurobaneShin/hiraishin/internal/stack/stacktest"
)

const stackConfig = `
schema_version = 1
[general]
active_profile = "burn-expiring-first"
[providers.mid]
harness = "kimi-native"
api_key_env = "MID_TEST_KEY"
quality_tier = 3
scarcity_tier = 2
speed_tier = 5
[providers.top]
harness = "antigravity-agy"
quality_tier = 4
scarcity_tier = 3
speed_tier = 3
[roles.default]
allowed_providers = ["mid", "top"]
min_quality_tier = 3
[integrations.ai_memory]
enabled = true
url = %q
record_outcomes = true
[integrations.qmd]
enabled = true
collections = ["orca"]
[integrations.graphify]
enabled = true
auto_size_tier = true
`

var stackGraph = func() string {
	nodes := []string{
		`{"id": "save", "label": "SaveAll", "community": 0, "community_name": "Persistence", "source_file": "store/save.go"}`,
		`{"id": "load", "label": "LoadAll", "community": 0, "community_name": "Persistence", "source_file": "store/load.go"}`,
		`{"id": "tick", "label": "Tick", "community": 1, "community_name": "Game loop", "source_file": "game/tick.go"}`,
		`{"id": "net", "label": "Broadcast", "community": 2, "community_name": "Network", "source_file": "net/broadcast.go"}`,
	}
	links := []string{
		`{"source": "tick", "target": "save", "relation": "calls"}`,
		`{"source": "save", "target": "load", "relation": "references"}`,
		`{"source": "net", "target": "tick", "relation": "calls"}`,
	}
	for h := range 10 {
		nodes = append(nodes, fmt.Sprintf(`{"id": "hub%d", "label": "Hub%d", "community": 3, "source_file": "util/hub.go"}`, h, h))
		for l := range 3 {
			nodes = append(nodes, fmt.Sprintf(`{"id": "leaf%d_%d", "label": "Leaf", "community": 3, "source_file": "util/leaf.go"}`, h, l))
			links = append(links, fmt.Sprintf(`{"source": "hub%d", "target": "leaf%d_%d", "relation": "calls"}`, h, h, l))
		}
	}
	return `{"nodes": [` + strings.Join(nodes, ",") + `], "links": [` + strings.Join(links, ",") + `]}`
}()

const stackDocs = `[{"file": "qmd://orca/game/docs/adr/0002-save-format.md", "line": 7, "title": "Save format", "score": 0.7, "snippet": "@@ -7,2 @@\nSaves are append-only."}]`

const stackObservations = `{"observations": [{"kind": "stop", "created_at": "2026-11-21T10:03:00Z", "body": "Stopped by the 5-hour limit while wiring SaveAll"}]}`

func openEngine(t *testing.T, memoryURL string) *engine.Engine {
	t.Helper()
	dir := t.TempDir()
	paths := config.Paths{Config: filepath.Join(dir, "p.toml"), DataDir: filepath.Join(dir, "data")}
	if err := os.WriteFile(paths.Config, fmt.Appendf(nil, stackConfig, memoryURL), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := engine.Open(context.Background(), paths, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func stackJob(t *testing.T, brief string, dirty ...string) ledger.JobRow {
	t.Helper()
	wt := t.TempDir()
	for _, argv := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", wt}, argv...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", argv, err, out)
		}
	}
	for _, f := range dirty {
		if err := os.MkdirAll(filepath.Join(wt, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wt, f), []byte("package x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	briefPath := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(briefPath, []byte(brief), 0o600); err != nil {
		t.Fatal(err)
	}
	return ledger.JobRow{ID: "job_1", Name: "persist-all", Role: "default", WorktreePath: wt, BriefPath: briefPath, ReportPath: filepath.Join(wt, "report.md"), NativeSessionID: "ses_k1"}
}

func TestResumePromptCarriesAllThreeIntegrations(t *testing.T) {
	mem := stacktest.NewMemory(t, stackObservations)
	args := stacktest.InstallQMD(t, stackDocs)
	e := openEngine(t, mem.URL)
	job := stackJob(t, "# Persist all\nSave every entity on each tick.\n", "store/save.go")
	stacktest.WriteGraph(t, job.WorktreePath, stackGraph)

	extra := e.Enrich(context.Background(), job, true)
	prompt, err := dispatch.ResumePrompt(dispatch.PromptData{JobID: job.ID, JobName: job.Name, Worktree: job.WorktreePath, BriefPath: job.BriefPath, ReportPath: job.ReportPath, FromProvider: "mid", SessionID: job.NativeSessionID, Reason: "limit", Enrichment: extra}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<previous-session-recovery session="ses_k1">`, "stop: Stopped by the 5-hour limit while wiring SaveAll",
		"<related-docs>", "qmd://orca/game/docs/adr/0002-save-format.md:7 (Save format, score 0.70)",
		"<codebase-subgraph>", "Tick (game/tick.go) calls SaveAll (store/save.go)", "Broadcast (net/broadcast.go) calls Tick (game/tick.go)",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("resume prompt missing %q:\n%s", want, prompt)
		}
	}
	argv, _ := os.ReadFile(args)
	for _, want := range []string{"intent: resume context for persist-all (default)", "lex: persist-all default save.go", "vec: Persist all Save every entity on each tick."} {
		if !strings.Contains(string(argv), want) {
			t.Errorf("qmd query missing %q:\n%s", want, argv)
		}
	}
	if calls := mem.Calls(); len(calls) != 1 || calls[0].Args["session_id"] != "ses_k1" || calls[0].Args["project"] != strings.ToLower(filepath.Base(job.WorktreePath)) {
		t.Errorf("ai-memory calls = %+v", calls)
	}

	initial := e.Enrich(context.Background(), job, false)
	if !strings.Contains(initial, "<related-docs>") || strings.Contains(initial, "<codebase-subgraph>") || strings.Contains(initial, "previous-session-recovery") {
		t.Errorf("initial enrichment = %q", initial)
	}
	if len(mem.Calls()) != 1 {
		t.Errorf("initial dispatch called ai-memory: %+v", mem.Calls())
	}
}

func TestGraphSizingRaisesMinQualityTier(t *testing.T) {
	e := openEngine(t, stacktest.ClosedURL(t))
	t.Setenv("PATH", "/usr/bin:/bin")
	local := stackJob(t, "Fix a bug in store/load.go.\n")
	stacktest.WriteGraph(t, local.WorktreePath, stackGraph)
	d, _, err := e.Decide(context.Background(), e.Ledger.Reader(), local, scheduler.TriggerDryRun, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.MinQualityTier != 3 || d.ChosenProvider != "mid" {
		t.Errorf("local task: tier %d chosen %s: %s", d.MinQualityTier, d.ChosenProvider, d.Summary)
	}

	wide := stackJob(t, "Touch store/load.go and game/tick.go.\n", "net/broadcast.go")
	stacktest.WriteGraph(t, wide.WorktreePath, stackGraph)
	d, _, err = e.Decide(context.Background(), e.Ledger.Reader(), wide, scheduler.TriggerDryRun, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.MinQualityTier != 4 || d.ChosenProvider != "top" || !strings.Contains(d.Summary, "min_quality_tier raised from 3 to 4 by graphify (3 files; 3 communities") {
		t.Errorf("wide task: tier %d chosen %s: %s", d.MinQualityTier, d.ChosenProvider, d.Summary)
	}
}

func TestAbsentIntegrationsFallBackCleanly(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv("PATH", "/usr/bin:/bin")
	if _, err := exec.LookPath("qmd"); err == nil {
		t.Skip("qmd is installed in /usr/bin")
	}
	e := openEngine(t, stacktest.ClosedURL(t))
	job := stackJob(t, "Touch store/load.go and game/tick.go.\n", "net/broadcast.go")
	ctx := context.Background()
	if _, err := e.Queue(ctx, job); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	extra := e.Enrich(ctx, job, true)
	enrichTook := time.Since(start)
	if extra != "" {
		t.Errorf("enrichment without tools = %q", extra)
	}
	start = time.Now()
	d, _, err := e.Decide(ctx, e.Ledger.Reader(), job, scheduler.TriggerDryRun, nil)
	decideTook := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if d.MinQualityTier != 3 || strings.Contains(d.Summary, "graphify") {
		t.Errorf("decision without graph: tier %d: %s", d.MinQualityTier, d.Summary)
	}
	start = time.Now()
	if err := e.RecordOutcome(ctx, job.ID); err == nil {
		t.Error("RecordOutcome without ai-memory reported success")
	}
	outcomeTook := time.Since(start)

	e.Cfg.Integrations = config.Integrations{}
	start = time.Now()
	if _, _, err := e.Decide(ctx, e.Ledger.Reader(), job, scheduler.TriggerDryRun, nil); err != nil {
		t.Fatal(err)
	}
	baseline := time.Since(start)
	t.Logf("enrich %v, decide %v vs baseline %v, outcome %v", enrichTook, decideTook, baseline, outcomeTook)
	if over := enrichTook + decideTook - baseline + outcomeTook; over > 50*time.Millisecond {
		t.Errorf("absent tools cost %v (enrich %v, decide %v vs %v, outcome %v)", over, enrichTook, decideTook, baseline, outcomeTook)
	}
	for _, name := range []string{"qmd", "ai-memory", "graphify"} {
		if !strings.Contains(logs.String(), "integration="+name) {
			t.Errorf("no debug note for %s:\n%s", name, logs.String())
		}
	}
	if strings.Contains(logs.String(), "level=WARN") || strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("absent tools logged above debug:\n%s", logs.String())
	}
}

func TestStdioTransportIsSkippedNotMisrouted(t *testing.T) {
	mem := stacktest.NewMemory(t, stackObservations)
	e := openEngine(t, mem.URL)
	e.Cfg.Integrations.AIMemory.Transport = "stdio"
	e.Cfg.Integrations.QMD, e.Cfg.Integrations.Graphify = nil, nil
	if got := e.Enrich(context.Background(), stackJob(t, "x"), true); got != "" || len(mem.Calls()) != 0 {
		t.Errorf("stdio transport enrichment = %q, calls %+v", got, mem.Calls())
	}
}
