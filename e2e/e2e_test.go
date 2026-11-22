package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	_ "modernc.org/sqlite"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hiraishin-e2e")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "hiraishin")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", binary, "./cmd/hiraishin")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build: %v\n%s", err, out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

const providersTOML = `
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
duration_hours = %s
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

type env struct {
	t     *testing.T
	home  string
	paths config.Paths
	extra []string
}

func newEnv(t *testing.T, configTOML string) *env {
	t.Helper()
	home := t.TempDir()
	e := &env{t: t, home: home, paths: config.Paths{Config: filepath.Join(home, ".config", "hiraishin", "preferences.toml"), DataDir: filepath.Join(home, "data")}}
	if configTOML != "" {
		if err := os.MkdirAll(filepath.Dir(e.paths.Config), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.paths.Config, []byte(configTOML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(e.killAgents)
	return e
}

func (e *env) environ() []string {
	bin, err := filepath.Abs(filepath.Join("testdata", "bin"))
	if err != nil {
		e.t.Fatal(err)
	}
	return append([]string{
		"PATH=" + bin + ":/usr/local/bin:/usr/bin:/bin",
		"HOME=" + e.home,
		"HIRAISHIN_CONFIG=" + e.paths.Config,
		"HIRAISHIN_DATA_DIR=" + e.paths.DataDir,
		"GIT_AUTHOR_NAME=e2e", "GIT_AUTHOR_EMAIL=e2e@example.invalid", "GIT_COMMITTER_NAME=e2e", "GIT_COMMITTER_EMAIL=e2e@example.invalid",
	}, e.extra...)
}

type result struct {
	stdout, stderr string
	code           int
	elapsed        time.Duration
}

func (e *env) exec(timeout time.Duration, argv ...string) result {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, argv...)
	cmd.Env = e.environ()
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	start := time.Now()
	err := cmd.Run()
	r := result{stdout: out.String(), stderr: errOut.String(), elapsed: time.Since(start)}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		r.code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatalf("%v: %v", argv, err)
	}
	return r
}

func (e *env) ok(argv ...string) string {
	e.t.Helper()
	r := e.exec(30*time.Second, argv...)
	if r.code != 0 {
		e.t.Fatalf("hiraishin %v: exit %d\nstdout: %s\nstderr: %s", argv, r.code, r.stdout, r.stderr)
	}
	return r.stdout
}

func (e *env) watch() {
	e.t.Helper()
	r := e.exec(60*time.Second, "watch", "--exit-when-idle", "--no-sensors", "--interval", "100ms", "--grace", "2s")
	if r.code != 0 {
		e.t.Fatalf("watch: exit %d\n%s", r.code, r.stderr)
	}
}

func (e *env) startWatch() func() {
	e.t.Helper()
	cmd := exec.CommandContext(context.Background(), binary, "watch", "--no-sensors", "--interval", "100ms", "--grace", "2s")
	cmd.Env = e.environ()
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	return func() {
		_ = cmd.Process.Signal(syscall.SIGINT)
		if err := cmd.Wait(); err != nil {
			e.t.Errorf("watch exited with %v\n%s", err, errOut.String())
		}
	}
}

func (e *env) killAgents() {
	pgids, _ := filepath.Glob(filepath.Join(e.paths.DataDir, "jobs", "*", "attempt-*", "pgid"))
	for _, p := range pgids {
		b, _ := os.ReadFile(p)
		if g, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && g > 1 {
			_ = syscall.Kill(-g, syscall.SIGKILL)
		}
	}
}

func git(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, argv...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=e2e", "GIT_AUTHOR_EMAIL=e2e@example.invalid", "GIT_COMMITTER_NAME=e2e", "GIT_COMMITTER_EMAIL=e2e@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", argv, err, out)
	}
	return string(out)
}

type job struct {
	worktree, brief, report string
}

func (e *env) newJob(name string) job {
	e.t.Helper()
	wt := e.t.TempDir()
	git(e.t, wt, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(wt, "app.go"), []byte("package app\n"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	git(e.t, wt, "add", "app.go")
	git(e.t, wt, "commit", "-q", "-m", "init")
	dir := filepath.Join(e.t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	brief := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(brief, []byte("Persist the game state.\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return job{worktree: wt, brief: brief, report: filepath.Join(dir, "report.md")}
}

func jobID(t *testing.T, out string) string {
	t.Helper()
	for f := range strings.FieldsSeq(out) {
		if strings.HasPrefix(f, "job_") {
			return strings.Trim(f, ".,")
		}
	}
	t.Fatalf("no job id in %q", out)
	return ""
}

type whyDoc struct {
	Job struct {
		ID, State string
	} `json:"job"`
	Decisions []struct {
		DecisionID     string            `json:"decision_id"`
		Trigger        string            `json:"trigger"`
		ChosenProvider string            `json:"chosen_provider"`
		Candidates     []json.RawMessage `json:"candidates"`
	} `json:"decisions"`
}

func (e *env) why(id string) (whyDoc, string) {
	e.t.Helper()
	raw := e.ok("why", id, "--json", "--all")
	var d whyDoc
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		e.t.Fatal(err)
	}
	return d, raw
}

type statusDoc struct {
	Providers []struct {
		ID         string `json:"id"`
		State      string `json:"state"`
		ActiveJobs int    `json:"active_jobs"`
		Windows    []struct {
			Name            string   `json:"name"`
			UsedPct         *float64 `json:"used_pct"`
			ResetsInSeconds *int64   `json:"resets_in_seconds"`
			Confidence      string   `json:"confidence"`
		} `json:"windows"`
	} `json:"providers"`
	Jobs map[string]int `json:"jobs"`
}

func (e *env) status() statusDoc {
	e.t.Helper()
	var d statusDoc
	if err := json.Unmarshal([]byte(e.ok("status", "--json")), &d); err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (d statusDoc) provider(id string) (string, int) {
	for _, p := range d.Providers {
		if p.ID == id {
			return p.State, p.ActiveJobs
		}
	}
	return "", -1
}

func (e *env) db() *sql.DB {
	e.t.Helper()
	db, err := sql.Open("sqlite", "file:"+e.paths.Ledger()+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = db.Close() })
	return db
}

func (e *env) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func waitUntil(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func validate(t *testing.T, schema, doc string) {
	t.Helper()
	sch, err := jsonschema.NewCompiler().Compile(filepath.Join("..", "docs", "schemas", schema))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("%s: %v", schema, err)
	}
}

func TestE2EInitWritesValidConfigWithoutSecrets(t *testing.T) {
	e := newEnv(t, "")
	const secret = "sk-e2e-zai-secret-0123456789"
	e.extra = []string{"ZAI_API_KEY=" + secret}
	oc := filepath.Join(e.home, ".config", "opencode")
	if err := os.MkdirAll(oc, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oc, "opencode.json"), []byte(`{"agent":{"kimi-worker":{"model":"kimi-code-plan-cn/k3.1-pro"},"glm-strong":{"model":"zai-coding-plan/glm-5.3"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.exec(30*time.Second, "init", "--yes")
	if r.code != 0 {
		t.Fatalf("init: %d %s", r.code, r.stderr)
	}
	body, err := os.ReadFile(e.paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]string{"file": string(body), "stdout": r.stdout, "stderr": r.stderr} {
		if strings.Contains(s, secret) {
			t.Errorf("secret leaked to %s", name)
		}
	}
	if fi, _ := os.Stat(e.paths.Config); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	cfg, err := config.Load(e.paths.Config)
	if err != nil {
		t.Fatalf("generated config does not validate: %v", err)
	}
	for _, id := range []string{"claude", "glm-claude", "kimi-native", "agy", "codex", "gemini"} {
		if _, ok := cfg.Providers[id]; !ok {
			t.Errorf("provider %s not detected: %v", id, cfg.ProviderIDs())
		}
	}
	if cfg.Providers["glm-claude"].APIKeyEnv != "ZAI_API_KEY" {
		t.Errorf("glm-claude api_key_env = %q", cfg.Providers["glm-claude"].APIKeyEnv)
	}
	if !strings.Contains(e.ok("status"), "kimi-native") {
		t.Error("status does not list detected providers")
	}
}

func TestE2EFailoverPreservesDirtyWorktreeAndCompletes(t *testing.T) {
	e := newEnv(t, fmt.Sprintf(providersTOML, "5"))
	j := e.newJob("persist-all")
	out := e.ok("run", "--brief", j.brief, "--worktree", j.worktree)
	id := jobID(t, out)
	if !strings.Contains(out, "on kimi via process") {
		t.Fatalf("first dispatch = %q", out)
	}
	e.watch()

	d, raw := e.why(id)
	if d.Job.State != "COMPLETED" {
		t.Fatalf("job state = %s", d.Job.State)
	}
	if diff, _ := os.ReadFile(filepath.Join(j.worktree, "app.go")); !strings.Contains(string(diff), "func Save() error") {
		t.Errorf("tracked edit lost: %q", diff)
	}
	if b, err := os.ReadFile(filepath.Join(j.worktree, "load.go")); err != nil || !strings.Contains(string(b), "func Load()") {
		t.Errorf("untracked file lost: %q %v", b, err)
	}
	st := git(t, j.worktree, "status", "--porcelain")
	if !strings.Contains(st, " M app.go") || !strings.Contains(st, "?? load.go") {
		t.Errorf("git status after failover = %q", st)
	}
	prompt, _ := os.ReadFile(filepath.Join(j.worktree, ".git", "agy-prompt.txt"))
	for _, want := range []string{"previous agent (kimi) stopped: 429 You've reached your 5-hour usage limit", "## git status --porcelain", " M app.go", "?? load.go", "## git diff --stat HEAD", "app.go | 2 ++", "- [x] kimi: wrote the save layer", "Brief: " + j.brief} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("resume prompt missing %q:\n%s", want, prompt)
		}
	}
	if rep, _ := os.ReadFile(j.report); !strings.Contains(string(rep), "- [x] agy: finished the brief") {
		t.Errorf("report = %q", rep)
	}
	if n := e.count(`SELECT count(*) FROM job_attempts WHERE job_id = ? AND exit_reason = 'limit_hit' AND provider_id = 'kimi'`, id); n != 1 {
		t.Errorf("kimi attempts ended by limit_hit = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM job_attempts WHERE job_id = ? AND provider_id = 'agy' AND exit_reason = 'completed'`, id); n != 1 {
		t.Errorf("agy attempts completed = %d", n)
	}

	t.Run("AC2 status", func(t *testing.T) {
		r := e.exec(10*time.Second, "status")
		if r.code != 0 || r.elapsed >= 500*time.Millisecond {
			t.Fatalf("status exit %d in %v", r.code, r.elapsed)
		}
		for _, want := range []string{"kimi", "exhausted", "agy", "available", "5h", "100%", "inferred", "0 jobs", "0/2"} {
			if !strings.Contains(r.stdout, want) {
				t.Errorf("status missing %q:\n%s", want, r.stdout)
			}
		}
		raw := e.ok("status", "--json")
		validate(t, "status.schema.json", raw)
		s := e.status()
		for _, p := range s.Providers {
			if p.ID != "kimi" {
				continue
			}
			w := p.Windows[0]
			if w.UsedPct == nil || *w.UsedPct != 100 || w.ResetsInSeconds == nil || *w.ResetsInSeconds < 4*3600 || w.Confidence != "inferred" {
				t.Errorf("kimi window = %+v", w)
			}
		}
	})

	t.Run("AC5 why", func(t *testing.T) {
		validate(t, "why.schema.json", raw)
		if len(d.Decisions) != 2 || d.Decisions[0].ChosenProvider != "kimi" || d.Decisions[1].Trigger != "failover" || d.Decisions[1].ChosenProvider != "agy" {
			t.Fatalf("decisions = %+v", d.Decisions)
		}
		for _, dec := range d.Decisions {
			var stored string
			if err := e.db().QueryRowContext(context.Background(), `SELECT candidates_json FROM decisions WHERE decision_id = ?`, dec.DecisionID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			var want, got []map[string]any
			if err := json.Unmarshal([]byte(stored), &want); err != nil {
				t.Fatal(err)
			}
			for _, c := range dec.Candidates {
				var m map[string]any
				if err := json.Unmarshal(c, &m); err != nil {
					t.Fatal(err)
				}
				got = append(got, m)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("why candidates differ from decisions row %s:\nwhy: %v\nrow: %v", dec.DecisionID, got, want)
			}
		}
		text := e.ok("why", id)
		for _, want := range []string{"failover", "✗ kimi", "state=EXHAUSTED", "excluded for this failover", "✓ agy", "picked", "quality", "speed", "help routing"} {
			if !strings.Contains(text, want) {
				t.Errorf("why text missing %q:\n%s", want, text)
			}
		}
	})
}

func TestE2EFixturesClassifyWithoutFalseFailover(t *testing.T) {
	const cfg = `
schema_version = 1
[general]
active_profile = "balanced"
[providers.claude]
harness = "claude-code"
quality_tier = 5
scarcity_tier = 3
speed_tier = 3
[providers.kimi]
harness = "kimi-native"
quality_tier = 4
scarcity_tier = 2
speed_tier = 4
[providers.kimi-oc]
harness = "opencode"
harness_agent = "kimi-worker"
model = "kimi-code-plan-cn/k3.1-pro"
quality_tier = 4
scarcity_tier = 2
speed_tier = 4
[providers.glm]
harness = "glm-claude"
api_key_env = "E2E_UNSET_KEY"
quality_tier = 4
scarcity_tier = 1
speed_tier = 3
[providers.glm-oc]
harness = "opencode"
harness_agent = "glm-strong"
model = "zai-coding-plan/glm-5.3"
quality_tier = 4
scarcity_tier = 1
speed_tier = 3
[providers.agy]
harness = "antigravity-agy"
quality_tier = 4
scarcity_tier = 3
speed_tier = 4
[providers.codex]
harness = "codex"
quality_tier = 4
scarcity_tier = 3
speed_tier = 4
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
allowed_providers = ["claude", "kimi", "kimi-oc", "glm", "glm-oc", "agy", "codex", "gemini"]
min_quality_tier = 2
`
	tests := []struct {
		fixture, provider, want string
	}{
		{"claude/limit_5h_epoch.txt", "claude", "EXHAUSTED"},
		{"claude/limit_5h_banner.txt", "claude", "EXHAUSTED"},
		{"claude/limit_weekly.txt", "claude", "EXHAUSTED"},
		{"claude/stream_limit_result.jsonl", "claude", "EXHAUSTED"},
		{"claude/rate_limit.jsonl", "claude", "DEGRADED"},
		{"claude/overloaded.jsonl", "claude", "DEGRADED"},
		{"claude/auth_expired.jsonl", "claude", "AUTH_ERROR"},
		{"claude/stream_normal.jsonl", "claude", ""},
		{"kimi/native_limit_5h.txt", "kimi", "EXHAUSTED"},
		{"kimi/opencode_limit_5h.txt", "kimi-oc", "EXHAUSTED"},
		{"kimi/opencode_burst_429.txt", "kimi-oc", "DEGRADED"},
		{"kimi/auth_401.txt", "kimi", "AUTH_ERROR"},
		{"kimi/opencode_normal.jsonl", "kimi-oc", ""},
		{"glm/claude_1113.jsonl", "glm", "EXHAUSTED"},
		{"glm/opencode_limit.txt", "glm-oc", "EXHAUSTED"},
		{"glm/rate_1302.jsonl", "glm", "DEGRADED"},
		{"glm/rate_1302_x3.jsonl", "glm", "EXHAUSTED"},
		{"glm/auth_1001.jsonl", "glm", "AUTH_ERROR"},
		{"agy/429.txt", "agy", "DEGRADED"},
		{"agy/429_x2.txt", "agy", "EXHAUSTED"},
		{"agy/401.txt", "agy", "AUTH_ERROR"},
		{"agy/normal.txt", "agy", ""},
		{"codex/limit_5h.jsonl", "codex", "EXHAUSTED"},
		{"codex/limit_weekly.jsonl", "codex", "EXHAUSTED"},
		{"codex/rate_429.jsonl", "codex", "DEGRADED"},
		{"codex/auth_401.txt", "codex", "AUTH_ERROR"},
		{"codex/exec_normal.jsonl", "codex", ""},
		{"gemini/quota_429.jsonl", "gemini", "DEGRADED"},
		{"gemini/quota_429_burst.jsonl", "gemini", "DEGRADED"},
		{"gemini/quota_daily.jsonl", "gemini", "EXHAUSTED"},
		{"gemini/auth_401.txt", "gemini", "AUTH_ERROR"},
		{"gemini/stream_normal.jsonl", "gemini", ""},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, cfg)
			fixture, err := filepath.Abs(filepath.Join("..", "internal", "sensor", "testdata", "fixtures", tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			e.extra = []string{"MOCK_FIXTURE=" + fixture}
			j := e.newJob("fixture")
			id := jobID(t, e.ok("run", "--brief", j.brief, "--worktree", j.worktree, "--provider", tt.provider))
			e.watch()
			d, _ := e.why(id)
			limits := e.count(`SELECT count(*) FROM events WHERE kind = 'provider.limit_hit' AND provider_id = ?`, tt.provider)
			auths := e.count(`SELECT count(*) FROM events WHERE kind = 'provider.auth_failed' AND provider_id = ?`, tt.provider)
			degraded := e.count(`SELECT count(*) FROM events WHERE kind = 'provider.degraded' AND provider_id = ?`, tt.provider)
			failovers := e.count(`SELECT count(*) FROM events WHERE kind = 'job.failover_started' AND job_id = ?`, id)
			got := fmt.Sprintf("limit=%d auth=%d degraded>0=%v failover=%d state=%s", limits, auths, degraded > 0, failovers, d.Job.State)
			want := map[string]string{
				"EXHAUSTED":  "limit=1 auth=0 degraded>0=%v failover=1 state=WAITING_QUOTA",
				"AUTH_ERROR": "limit=0 auth=1 degraded>0=false failover=1 state=WAITING_QUOTA",
				"DEGRADED":   "limit=0 auth=0 degraded>0=true failover=0 state=COMPLETED",
				"":           "limit=0 auth=0 degraded>0=false failover=0 state=COMPLETED",
			}[tt.want]
			if tt.want == "EXHAUSTED" {
				want = fmt.Sprintf(want, degraded > 0)
			}
			if got != want {
				t.Fatalf("got  %s\nwant %s", got, want)
			}
		})
	}
}

func TestE2EFailoverBetweenClaudeAccounts(t *testing.T) {
	home := t.TempDir()
	acctA := filepath.Join(home, "profiles", "a", "home")
	acctB := filepath.Join(home, "profiles", "b", "home")
	e := newEnv(t, fmt.Sprintf(`
schema_version = 1
[general]
active_profile = "burn-expiring-first"
[providers.claude-a]
harness = "claude-code"
config_dir = %q
shared_quota_pool = "claude-a"
quality_tier = 5
scarcity_tier = 3
speed_tier = 3
[[providers.claude-a.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5
[providers.claude-b]
harness = "claude-code"
config_dir = %q
shared_quota_pool = "claude-b"
quality_tier = 5
scarcity_tier = 3
speed_tier = 3
[[providers.claude-b.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5
[roles.default]
allowed_providers = ["claude-a", "claude-b"]
min_quality_tier = 4
`, acctA, acctB))
	e.extra = []string{"MOCK_CLAUDE_LIMITED_DIR=" + acctA, "CLAUDE_CONFIG_DIR=/inherited/from/caller"}
	j := e.newJob("multi-account")
	out := e.ok("run", "--brief", j.brief, "--worktree", j.worktree)
	id := jobID(t, out)
	if !strings.Contains(out, "on claude-a") {
		t.Fatalf("first dispatch = %q", out)
	}
	e.watch()
	d, _ := e.why(id)
	if d.Job.State != "COMPLETED" || len(d.Decisions) != 2 || d.Decisions[1].ChosenProvider != "claude-b" {
		t.Fatalf("job = %s decisions = %+v", d.Job.State, d.Decisions)
	}
	seen, _ := os.ReadFile(filepath.Join(j.worktree, ".git", "claude-accounts.txt"))
	if string(seen) != acctA+"\n"+acctB+"\n" {
		t.Errorf("accounts used = %q, want A then B", seen)
	}
	if _, err := os.Stat(filepath.Join(j.worktree, "half.go")); err != nil {
		t.Errorf("account A's dirty file lost: %v", err)
	}
	if a := e.count(`SELECT count(*) FROM events WHERE kind = 'provider.limit_hit' AND provider_id = 'claude-a'`); a != 1 {
		t.Errorf("claude-a limit_hit = %d", a)
	}
	if b := e.count(`SELECT count(*) FROM events WHERE kind = 'provider.limit_hit' AND provider_id = 'claude-b'`); b != 0 {
		t.Errorf("claude-b marked exhausted by account A's limit: %d", b)
	}
	if state, _ := e.status().provider("claude-b"); state != "AVAILABLE" {
		t.Errorf("claude-b = %s", state)
	}
}

func TestE2EPausedAndDrainingGetNoDispatches(t *testing.T) {
	e := newEnv(t, fmt.Sprintf(providersTOML, "5"))
	e.ok("pause", "kimi", "--reason", "e2e")
	a := e.newJob("a")
	if err := os.WriteFile(filepath.Join(a.worktree, ".git", "hang"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	outA := e.ok("run", "--brief", a.brief, "--worktree", a.worktree)
	idA := jobID(t, outA)
	if !strings.Contains(outA, "on agy") {
		t.Fatalf("paused kimi was not skipped: %q", outA)
	}
	if out := e.ok("drain", "agy"); !strings.Contains(out, "agy: DRAINING") {
		t.Fatalf("drain with a running job = %q", out)
	}
	b := e.newJob("b")
	outB := e.ok("run", "--brief", b.brief, "--worktree", b.worktree)
	idB := jobID(t, outB)
	if !strings.Contains(outB, "WAITING_QUOTA") {
		t.Fatalf("job dispatched while kimi PAUSED and agy DRAINING: %q", outB)
	}
	text := e.ok("why", idB)
	for _, want := range []string{"state=PAUSED", "state=DRAINING"} {
		if !strings.Contains(text, want) {
			t.Errorf("why missing %q:\n%s", want, text)
		}
	}
	if n := e.count(`SELECT count(*) FROM events WHERE kind = 'job.dispatched'`); n != 1 {
		t.Errorf("dispatches = %d, want 1", n)
	}
	if state, _ := e.status().provider("agy"); state != "DRAINING" {
		t.Errorf("agy = %s before its job ends", state)
	}
	e.ok("cancel", idA)
	if state, active := e.status().provider("agy"); state != "PAUSED" || active != 0 {
		t.Errorf("agy after last job ended = %s (%d active), want PAUSED", state, active)
	}
}

func TestE2ERunningJobsDoNotSwitchWhenProviderResets(t *testing.T) {
	e := newEnv(t, fmt.Sprintf(providersTOML, "0.001"))
	stop := e.startWatch()
	s := e.newJob("strict")
	idS := jobID(t, e.ok("run", "--brief", s.brief, "--worktree", s.worktree, "--role", "strict"))
	waitUntil(t, "strict job to wait for kimi", 15*time.Second, func() bool { d, _ := e.why(idS); return d.Job.State == "WAITING_QUOTA" })
	r := e.newJob("running")
	if err := os.WriteFile(filepath.Join(r.worktree, ".git", "hang"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	outR := e.ok("run", "--brief", r.brief, "--worktree", r.worktree)
	idR := jobID(t, outR)
	if !strings.Contains(outR, "on agy") {
		t.Fatalf("new job during kimi exhaustion = %q", outR)
	}
	waitUntil(t, "strict job to finish on kimi after the reset", 30*time.Second, func() bool { d, _ := e.why(idS); return d.Job.State == "COMPLETED" })
	stop()
	if n := e.count(`SELECT count(*) FROM events WHERE kind = 'provider.window_reset' AND provider_id = 'kimi'`); n < 1 {
		t.Errorf("kimi window never reset")
	}
	if n := e.count(`SELECT count(*) FROM job_attempts WHERE job_id = ? AND provider_id = 'kimi'`, idS); n != 2 {
		t.Errorf("strict attempts on kimi = %d, want 2 (never switched to agy)", n)
	}
	d, _ := e.why(idR)
	if d.Job.State != "RUNNING" || len(d.Decisions) != 1 {
		t.Errorf("running job = %s with %d decisions, want RUNNING with 1", d.Job.State, len(d.Decisions))
	}
	if n := e.count(`SELECT count(*) FROM job_attempts WHERE job_id = ?`, idR); n != 1 {
		t.Errorf("running job attempts = %d, want 1", n)
	}
	if rep, _ := os.ReadFile(s.report); !strings.Contains(string(rep), "kimi: finished after the window reset") {
		t.Errorf("strict report = %q", rep)
	}
}
