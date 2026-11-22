package sensor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertSample(t *testing.T, s Sample, window string, used float64, reset time.Time) {
	t.Helper()
	if s.Window != window || s.UsedPct == nil || *s.UsedPct < used-1e-9 || *s.UsedPct > used+1e-9 || !s.ResetsAt.Equal(reset) {
		t.Errorf("sample = %+v (used %v), want %s %.2f %v", s, s.UsedPct, window, used, reset)
	}
}

func TestParseEndpoints(t *testing.T) {
	k, err := ParseKimiUsages(fixture(t, "endpoint/kimi_usages.json"))
	if err != nil || len(k) != 2 {
		t.Fatalf("kimi = %+v, %v", k, err)
	}
	assertSample(t, k[0], "5h", 76, time.Date(2026, 11, 21, 16, 21, 0, 0, time.UTC))
	assertSample(t, k[1], "weekly", 38, time.Date(2026, 11, 24, 6, 49, 37, 0, time.UTC))

	z, err := ParseZAIQuota(fixture(t, "endpoint/zai_quota_limit.json"))
	if err != nil || len(z) != 2 {
		t.Fatalf("zai = %+v, %v", z, err)
	}
	assertSample(t, z[0], "5h", 37, time.UnixMilli(1763747340000).UTC())
	assertSample(t, z[1], "weekly", 50, time.UnixMilli(1764000000000).UTC())

	q, err := ParseQuotaAxi(fixture(t, "endpoint/quota_axi.json"), FamilyClaude)
	if err != nil || len(q) != 2 {
		t.Fatalf("quota-axi = %+v, %v", q, err)
	}
	assertSample(t, q[0], "5h", 40, time.Date(2026, 11, 21, 19, 0, 0, 0, time.UTC))
	if _, err := ParseQuotaAxi(fixture(t, "endpoint/quota_axi.json"), FamilyGLM); err == nil {
		t.Error("want error when quota-axi has no windows for the family")
	}
}

func TestProbeEndpointUsesKeyFromEnvOnly(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write(fixture(t, "endpoint/zai_quota_limit.json"))
	}))
	defer srv.Close()
	p := &Probe{Client: srv.Client(), Getenv: func(k string) string { return map[string]string{"ZAI_API_KEY": "test-key"}[k] },
		Now: time.Now, URLs: map[Family]string{FamilyGLM: srv.URL}}
	prov := config.Provider{Harness: config.HarnessGLMClaude, APIKeyEnv: "ZAI_API_KEY", Sensor: config.Sensor{Mode: "auto"}}
	s, err := p.Sample(context.Background(), prov)
	if err != nil || len(s) != 2 || s[0].Tier != "endpoint" {
		t.Fatalf("samples = %+v, %v", s, err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("auth header = %q", gotAuth)
	}
}

func TestProbeRateLimitedEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }))
	defer srv.Close()
	p := &Probe{Client: srv.Client(), Getenv: func(string) string { return "k" }, Now: time.Now, URLs: map[Family]string{FamilyKimi: srv.URL}}
	prov := config.Provider{Harness: config.HarnessKimiNative, APIKeyEnv: "KIMI_KEY", Sensor: config.Sensor{Mode: "endpoint"}}
	if _, err := p.Sample(context.Background(), prov); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestGuardEnforcesMinimumAndBacksOff(t *testing.T) {
	g := NewGuard()
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	if !g.Allow("p", now) {
		t.Fatal("first poll must be allowed")
	}
	g.Done("p", now, 10*time.Second, false)
	if g.Allow("p", now.Add(119*time.Second)) || !g.Allow("p", now.Add(120*time.Second)) {
		t.Error("interval below 120s must be raised to 120s")
	}
	g.Done("p", now, 120*time.Second, true)
	if g.Allow("p", now.Add(239*time.Second)) || !g.Allow("p", now.Add(240*time.Second)) {
		t.Error("first 429 must double the interval")
	}
}

func TestLocalLogs(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	dir := filepath.Join(home, ".claude", "projects", "-wt")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := `{"timestamp":"2026-11-21T14:00:00Z","message":{"usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":10,"cache_read_input_tokens":9999}}}
{"timestamp":"2026-11-21T08:00:00Z","message":{"usage":{"input_tokens":7777,"output_tokens":1}}}
not json
`
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := ClaudeTokens(ClaudeDir(config.Provider{}, home), now.Add(-5*time.Hour))
	if err != nil || n != 160 {
		t.Fatalf("claude tokens = %d, %v; want 160", n, err)
	}
	agy := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "c1", ".system_generated", "logs")
	if err := os.MkdirAll(agy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agy, "transcript.jsonl"), []byte("{}\n{}\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	turns, err := AgyTurns(home, time.Time{})
	if err != nil || turns != 3 {
		t.Fatalf("agy turns = %d, %v", turns, err)
	}
	if got := ClaudeDir(config.Provider{ConfigDir: "~/profiles/b"}, home); got != filepath.Join(home, "profiles", "b") {
		t.Errorf("ClaudeDir = %q", got)
	}
	shared := filepath.Join(home, "profile-b")
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".claude", "projects"), filepath.Join(shared, "projects")); err != nil {
		t.Fatal(err)
	}
	p := &Probe{Home: home, Now: func() time.Time { return now }}
	prov := config.Provider{Harness: config.HarnessClaudeCode, Sensor: config.Sensor{Mode: "local_logs"}, Windows: []config.Window{{Name: "5h", Kind: "rolling", DurationHours: 5}}}
	if s, err := p.Sample(context.Background(), prov); err != nil || len(s) != 1 || *s[0].RawTokens != 160 {
		t.Fatalf("default account sample = %+v %v", s, err)
	}
	prov.ConfigDir = shared
	if s, err := p.Sample(context.Background(), prov); err != nil || s != nil {
		t.Fatalf("symlinked projects must not be attributed to this account: %+v %v", s, err)
	}
	if n, err := ClaudeTokens(filepath.Join(home, "missing"), now); err != nil || n != 0 {
		t.Fatalf("missing dir = %d, %v", n, err)
	}
}

func TestParseLiveEndpoints(t *testing.T) {
	k, err := ParseKimiUsages(fixture(t, "endpoint/kimi_usages_live.json"))
	if err != nil || len(k) != 2 {
		t.Fatalf("kimi = %+v, %v", k, err)
	}
	assertSample(t, k[0], "5h", 0, time.Date(2026, 11, 22, 5, 17, 29, 0, time.UTC))
	assertSample(t, k[1], "weekly", 40.68, time.Date(2026, 11, 23, 12, 17, 28, 0, time.UTC))

	z, err := ParseZAIQuota(fixture(t, "endpoint/zai_quota_live.json"))
	if err != nil || len(z) != 2 {
		t.Fatalf("zai = %+v, %v", z, err)
	}
	assertSample(t, z[0], "5h", 9, time.UnixMilli(1795310029392).UTC())
	assertSample(t, z[1], "weekly", 94, time.UnixMilli(1795440447983).UTC())

	c, err := ParseClaudeUsage(fixture(t, "endpoint/claude_oauth_usage.json"))
	if err != nil || len(c) != 2 {
		t.Fatalf("claude = %+v, %v", c, err)
	}
	assertSample(t, c[0], "5h", 51, time.Date(2026, 11, 22, 1, 10, 0, 237155000, time.UTC))
	assertSample(t, c[1], "weekly", 41, time.Date(2026, 11, 24, 4, 0, 0, 237174000, time.UTC))

	noSession, err := ParseClaudeUsage(fixture(t, "endpoint/claude_oauth_usage_no_5h.json"))
	if err != nil || len(noSession) != 1 {
		t.Fatalf("claude plan without a session limit = %+v, %v, want the weekly window only", noSession, err)
	}
	assertSample(t, noSession[0], "weekly", 86, time.Date(2026, 11, 25, 1, 59, 59, 525174000, time.UTC))

	idle, err := ParseClaudeUsage([]byte(`{"five_hour":null,"seven_day":{"utilization":10,"resets_at":"2026-11-24T04:00:00Z"},"limits":[{"kind":"session","percent":0},{"kind":"weekly_all","percent":10}]}`))
	if err != nil || len(idle) != 2 {
		t.Fatalf("claude with an idle session limit = %+v, %v", idle, err)
	}
	assertSample(t, idle[0], "5h", 0, time.Time{})

	legacy, err := ParseClaudeUsage([]byte(`{"five_hour":null,"seven_day":{"utilization":10,"resets_at":"2026-11-24T04:00:00Z"}}`))
	if err != nil || len(legacy) != 2 {
		t.Fatalf("claude usage without a limits list = %+v, %v", legacy, err)
	}
	assertSample(t, legacy[0], "5h", 0, time.Time{})

	if _, err := ParseClaudeUsage([]byte(`{"five_hour":null,"seven_day":null}`)); err == nil {
		t.Error("want error when no Claude window is present")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProbeClaudeEndpointReadsAccountToken(t *testing.T) {
	var gotAuth, gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotBeta = r.Header.Get("Authorization"), r.Header.Get("anthropic-beta")
		_, _ = w.Write(fixture(t, "endpoint/claude_oauth_usage.json"))
	}))
	defer srv.Close()
	home := t.TempDir()
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	writeFile(t, filepath.Join(home, "profiles", "b", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"tok-b","expiresAt":1795400000000}}`)
	p := &Probe{Client: srv.Client(), Home: home, Now: func() time.Time { return now }, URLs: map[Family]string{FamilyClaude: srv.URL}}
	prov := config.Provider{Harness: config.HarnessClaudeCode, ConfigDir: "~/profiles/b", Sensor: config.Sensor{Mode: "auto"}}
	s, err := p.Sample(context.Background(), prov)
	if err != nil || len(s) != 2 || s[0].Tier != "endpoint" {
		t.Fatalf("samples = %+v, %v", s, err)
	}
	if gotAuth != "Bearer tok-b" || gotBeta != "oauth-2025-04-20" {
		t.Errorf("headers = %q %q", gotAuth, gotBeta)
	}
	gotAuth = ""
	writeFile(t, filepath.Join(home, "profiles", "b", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"tok-old","expiresAt":1795300000000}}`)
	if s, err := p.Sample(context.Background(), prov); err != nil || gotAuth != "" || (len(s) > 0 && s[0].Tier == "endpoint") {
		t.Fatalf("expired token must not be sent: samples=%+v err=%v auth=%q", s, err, gotAuth)
	}
	prov.Sensor.Mode = "local_logs"
	writeFile(t, filepath.Join(home, "profiles", "b", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"tok-b","expiresAt":1795400000000}}`)
	if _, err := p.Sample(context.Background(), prov); err != nil || gotAuth != "" {
		t.Fatalf("local_logs mode must not call the endpoint: err=%v auth=%q", err, gotAuth)
	}
}

func TestProbeOpenCodeProviderUsesOpenCodeKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write(fixture(t, "endpoint/kimi_usages_live.json"))
	}))
	defer srv.Close()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".local", "share", "opencode", "auth.json"), `{"kimi-code-plan-cn":{"type":"api","key":"oc-kimi"},"zai-coding-plan":{"type":"api","key":"oc-zai"}}`)
	p := &Probe{Client: srv.Client(), Getenv: func(string) string { return "" }, Home: home, Now: time.Now, URLs: map[Family]string{FamilyKimi: srv.URL}}
	prov := config.Provider{Harness: config.HarnessOpenCode, Model: "kimi-code-plan-cn/k3.1-pro", Sensor: config.Sensor{Mode: "auto"}}
	s, err := p.Sample(context.Background(), prov)
	if err != nil || len(s) != 2 || gotAuth != "Bearer oc-kimi" {
		t.Fatalf("samples = %+v, %v, auth = %q", s, err, gotAuth)
	}
}

func TestParseAgyUsage(t *testing.T) {
	s, err := ParseAgyUsage(fixture(t, "agy/usage.jsonl"))
	if err != nil || len(s) != 2 {
		t.Fatalf("agy = %+v, %v", s, err)
	}
	assertSample(t, s[0], "5h", (1-0.41664940118789673)*100, time.Date(2026, 11, 22, 1, 11, 34, 0, time.UTC))
	assertSample(t, s[1], "weekly", (1-0.5754887461662292)*100, time.Date(2026, 11, 28, 15, 11, 34, 0, time.UTC))
	if _, err := ParseAgyUsage([]byte("Authentication required. Please visit the URL\n")); err == nil {
		t.Error("want error when agy prints no usage JSON")
	}
}

func TestProbeAgyRunsUsageCommand(t *testing.T) {
	var got []string
	p := &Probe{Now: time.Now, Home: t.TempDir(), Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return fixture(t, "agy/usage.jsonl"), nil
	}}
	prov := config.Provider{Harness: config.HarnessAgy, Sensor: config.Sensor{Mode: "auto"}}
	s, err := p.Sample(context.Background(), prov)
	if err != nil || len(s) != 2 || s[0].Tier != "cli" {
		t.Fatalf("samples = %+v, %v", s, err)
	}
	want := "env AGY_CLI_DISABLE_AUTO_UPDATE=true agy -p /usage --output-format json --print-timeout 20s"
	if strings.Join(got, " ") != want {
		t.Errorf("command = %q, want %q", strings.Join(got, " "), want)
	}
}

func TestProbeNeverSendsAnotherProvidersOpenCodeKey(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(fixture(t, "endpoint/kimi_usages_live.json"))
	}))
	defer srv.Close()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".local", "share", "opencode", "auth.json"), `{"openrouter":{"type":"api","key":"or-key"}}`)
	p := &Probe{Client: srv.Client(), Getenv: func(string) string { return "" }, Home: home, Now: time.Now, URLs: map[Family]string{FamilyKimi: srv.URL}}
	prov := config.Provider{Harness: config.HarnessOpenCode, Model: "openrouter/moonshotai/kimi-k3", Sensor: config.Sensor{Mode: "endpoint"}}
	if _, err := p.Sample(context.Background(), prov); err != nil || hits != 0 {
		t.Fatalf("openrouter key sent to the Kimi usage endpoint: hits=%d err=%v", hits, err)
	}
}

func TestProbeCodexRolloutSensor(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	rollout := filepath.Join(home, ".codex", "sessions", "2026", "11", "21", "rollout-2026-11-21T14-00-00-0199a213.jsonl")
	writeFile(t, rollout, string(fixture(t, "codex/rollout_sample.jsonl")))
	p := &Probe{Home: home, Now: func() time.Time { return now }}
	prov := config.Provider{
		Harness: config.HarnessCodex,
		Sensor:  config.Sensor{Mode: "auto"},
		Windows: []config.Window{{Name: "5h", Kind: "rolling", DurationHours: 5}, {Name: "weekly", Kind: "fixed_reset", DurationHours: 168}},
	}
	samples, err := p.Sample(context.Background(), prov)
	if err != nil || len(samples) != 2 {
		t.Fatalf("codex samples = %+v, %v", samples, err)
	}
	assertSample(t, samples[0], "5h", 42.5, time.Unix(1795287600, 0).UTC())
	if samples[0].RawTokens == nil || *samples[0].RawTokens != 8100 {
		t.Errorf("5h raw tokens = %v, want 8100", samples[0].RawTokens)
	}
	assertSample(t, samples[1], "weekly", 15.0, time.Unix(1795700000, 0).UTC())

	expired := &Probe{Home: home, Now: func() time.Time { return time.Unix(1795287700, 0).UTC() }}
	expSamples, err := expired.Sample(context.Background(), prov)
	if err != nil || len(expSamples) != 2 || expSamples[0].UsedPct != nil || expSamples[1].Window != "weekly" {
		t.Fatalf("expired 5h sample = %+v, %v", expSamples, err)
	}

	altHome := filepath.Join(t.TempDir(), "custom-codex")
	writeFile(t, filepath.Join(altHome, "sessions", "2026", "11", "21", "rollout-tokonly.jsonl"),
		`{"timestamp":"2026-11-21T14:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":400,"cached_input_tokens":100,"output_tokens":50}}}}`+"\n")
	p2 := &Probe{Home: home, Getenv: func(k string) string { return map[string]string{"CODEX_HOME": altHome}[k] }, Now: func() time.Time { return now }}
	s2, err := p2.Sample(context.Background(), prov)
	if err != nil || len(s2) != 1 || s2[0].UsedPct != nil || s2[0].RawTokens == nil || *s2[0].RawTokens != 350 {
		t.Fatalf("token-only codex sample = %+v, %v", s2, err)
	}
}

func TestProbeGeminiLocalLogsAndLimitProbe(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	chat := filepath.Join(home, ".gemini", "tmp", "projhash", "chats", "session-2026-11-21T14-00.json")
	writeFile(t, chat, string(fixture(t, "gemini/session_chat.json")))
	p := &Probe{Home: home, Now: func() time.Time { return now }}
	prov := config.Provider{
		Harness: config.HarnessGeminiCLI,
		Model:   "gemini-2.5-pro",
		Sensor:  config.Sensor{Mode: "auto"},
		Windows: []config.Window{{Name: "daily_rpd", Kind: "fixed_reset", DurationHours: 24, FixedResetTZ: "America/Los_Angeles"}},
	}
	samples, err := p.Sample(context.Background(), prov)
	if err != nil || len(samples) != 1 {
		t.Fatalf("gemini samples = %+v, %v", samples, err)
	}
	wantReset := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	if samples[0].Window != "daily_rpd" || !samples[0].ResetsAt.Equal(wantReset) || samples[0].RawTokens == nil || *samples[0].RawTokens != 2600 {
		t.Fatalf("gemini sample = %+v (tokens %v), want daily_rpd reset %v tokens 2600", samples[0], samples[0].RawTokens, wantReset)
	}

	var got []string
	p.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return fixture(t, "gemini/stream_session.jsonl"), nil
	}
	sig, hit, err := p.ProbeGeminiLimit(context.Background(), prov)
	if err != nil || hit {
		t.Fatalf("clean probe = %+v hit=%v err=%v", sig, hit, err)
	}
	wantCmd := "gemini -p ok --output-format stream-json --model gemini-2.5-pro"
	if strings.Join(got, " ") != wantCmd {
		t.Errorf("probe command = %q, want %q", strings.Join(got, " "), wantCmd)
	}

	p.Run = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return fixture(t, "gemini/quota_429.jsonl"), errors.New("exit status 1")
	}
	sig, hit, err = p.ProbeGeminiLimit(context.Background(), prov)
	if err != nil || !hit || sig.Kind != Degraded {
		t.Fatalf("429 probe = %+v hit=%v err=%v", sig, hit, err)
	}
}
