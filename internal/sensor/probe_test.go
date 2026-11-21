package sensor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
