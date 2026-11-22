package doctor_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/doctor"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

func TestDoctorDiagnosesMissingBinaries(t *testing.T) {
	cfg := testConfig(t)
	l, now := openLedger(t)
	deps := doctor.Deps{
		Now:      now,
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound },
		ReadFile: os.ReadFile,
		Stat:     os.Stat,
		Getenv:   os.Getenv,
	}
	report, err := doctor.Check(context.Background(), cfg, l.Reader(), deps)
	if err != nil {
		t.Fatal(err)
	}
	var missing int
	for _, c := range report.Findings {
		if c.Category == "binary" && !c.OK {
			missing++
		}
	}
	if missing != len(cfg.ProviderIDs()) {
		t.Fatalf("want all %d binaries missing, got %d", len(cfg.ProviderIDs()), missing)
	}
}

func TestDoctorReportsAuthErrorAndExpiringToken(t *testing.T) {
	cfg := testConfig(t)
	l, now := openLedger(t)
	ctx := context.Background()
	mustAppend(t, l, ledger.Event{ProviderID: "claude-opus", Source: ledger.SourceConfig, Payload: ledger.ProviderConfigured{Harness: "claude-code", MaxConcurrent: 1}})
	mustAppend(t, l, ledger.Event{ProviderID: "claude-opus", Source: ledger.SourceWatcher, Payload: ledger.AuthFailed{Excerpt: "401 Unauthorized"}})
	mustAppend(t, l, ledger.Event{ProviderID: "kimi-native", Source: ledger.SourceConfig, Payload: ledger.ProviderConfigured{Harness: "kimi-native", MaxConcurrent: 1}})

	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	mkdir(t, filepath.Join(home, ".claude"))
	writeFile(t, filepath.Join(home, ".claude", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"super-secret-token","expiresAt":`+timestampMS(now().Add(30*time.Minute))+`}}`)

	lookPath := map[string]string{"claude": "/bin/claude", "kimi": "/bin/kimi"}
	deps := doctor.Deps{
		Now: now,
		LookPath: func(name string) (string, error) {
			if p, ok := lookPath[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		},
		ReadFile: os.ReadFile,
		Stat:     os.Stat,
		Getenv: func(name string) string {
			if name == "HOME" {
				return home
			}
			return ""
		},
	}
	report, err := doctor.Check(ctx, cfg, l.Reader(), deps)
	if err != nil {
		t.Fatal(err)
	}
	var authError, expiring bool
	out := render(report)
	for _, c := range report.Findings {
		if c.Category == "auth" && c.Provider == "claude-opus" && c.Name == "provider state" {
			authError = !c.OK
		}
		if c.Category == "auth" && c.Provider == "claude-opus" && c.Name == "claude token" {
			expiring = !c.OK
		}
	}
	if !authError {
		t.Fatal("want AUTH_ERROR reported for claude-opus")
	}
	if !expiring {
		t.Fatal("want expiring token reported for claude-opus")
	}
	if strings.Contains(out, "super-secret-token") {
		t.Fatal("doctor output leaked token value")
	}
}

func TestDoctorReportsStaleSensors(t *testing.T) {
	cfg := testConfig(t)
	l, now := openLedger(t)
	ctx := context.Background()
	mustAppend(t, l, ledger.Event{ProviderID: "kimi-native", Source: ledger.SourceConfig, Payload: ledger.ProviderConfigured{Harness: "kimi-native", MaxConcurrent: 1}})
	old := now().Add(-10 * time.Minute)
	mustAppend(t, l, ledger.Event{ProviderID: "kimi-native", Source: ledger.SourceSensor, Payload: ledger.QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: ptrF(10), Confidence: ledger.ConfidenceExact}, TS: old})

	lookPath := map[string]string{"kimi": "/bin/kimi"}
	deps := doctor.Deps{
		Now: now,
		LookPath: func(name string) (string, error) {
			if p, ok := lookPath[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		},
		ReadFile: os.ReadFile,
		Stat:     os.Stat,
		Getenv:   os.Getenv,
	}
	report, err := doctor.Check(ctx, cfg, l.Reader(), deps)
	if err != nil {
		t.Fatal(err)
	}
	var stale bool
	for _, c := range report.Findings {
		if c.Category == "sensor" && c.Provider == "kimi-native" && !c.OK {
			stale = true
		}
	}
	if !stale {
		t.Fatal("want stale sensor for kimi-native")
	}
}

func TestDoctorChecksIntegrationReachability(t *testing.T) {
	cfg := testConfig(t)
	cfg.Integrations.QMD = &config.QMD{Enabled: true}
	cfg.Integrations.AIMemory = &config.AIMemory{Enabled: true, Transport: "http", URL: "http://ai-memory.test/mcp"}
	l, now := openLedger(t)

	var gotURL string
	lookPath := map[string]string{"qmd": "/bin/qmd"}
	deps := doctor.Deps{
		Now: now,
		LookPath: func(name string) (string, error) {
			if p, ok := lookPath[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		},
		ReadFile: os.ReadFile,
		Stat:     os.Stat,
		Getenv:   os.Getenv,
		HTTPGet: func(ctx context.Context, url string) error {
			gotURL = url
			return nil
		},
		Exec: func(ctx context.Context, name string, args ...string) error {
			if name == "qmd" && len(args) == 1 && args[0] == "status" {
				return nil
			}
			return errors.New("unexpected exec")
		},
	}
	report, err := doctor.Check(context.Background(), cfg, l.Reader(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if gotURL != "http://ai-memory.test/mcp" {
		t.Fatalf("want ai-memory URL probed, got %q", gotURL)
	}
	var aiMemoryOK, qmdOK bool
	for _, c := range report.Findings {
		if c.Category == "integration" && c.Name == "ai-memory" && c.OK {
			aiMemoryOK = true
		}
		if c.Category == "integration" && c.Name == "qmd" && c.OK {
			qmdOK = true
		}
	}
	if !aiMemoryOK {
		t.Fatal("want ai-memory reachable")
	}
	if !qmdOK {
		t.Fatal("want qmd reachable")
	}
}

func TestDoctorChecksWorktreeHealth(t *testing.T) {
	cfg := testConfig(t)
	l, now := openLedger(t)
	ctx := context.Background()
	repo := initGitRepo(t)
	mustAppend(t, l, ledger.Event{ProviderID: "kimi-native", Source: ledger.SourceConfig, Payload: ledger.ProviderConfigured{Harness: "kimi-native", MaxConcurrent: 1}})
	jobID := "job_01TEST"
	mustAppend(t, l, ledger.Event{JobID: jobID, Source: ledger.SourceCLI, Payload: ledger.JobQueued{Name: "x", Role: "default", WorktreePath: repo, Branch: "main", BriefPath: "/b", TerminalAdapter: "process"}})
	decID := ledger.NewID("dec")
	mustAppend(t, l, ledger.Event{JobID: jobID, Source: ledger.SourceScheduler, Payload: ledger.DecisionMade{DecisionID: decID, Trigger: "initial_dispatch", Profile: "balanced", ChosenProvider: "kimi-native", Role: "default", Candidates: json.RawMessage("[]"), Summary: "s"}})
	mustAppend(t, l, ledger.Event{JobID: jobID, ProviderID: "kimi-native", Source: ledger.SourceCLI, Payload: ledger.JobDispatched{AttemptID: ledger.NewID("att"), AttemptNum: 1, DecisionID: decID, ProviderID: "kimi-native", TerminalAdapter: "process", TerminalHandle: "123", AttemptDir: "/a"}})

	lookPath := map[string]string{"kimi": "/bin/kimi"}
	deps := doctor.Deps{
		Now: now,
		LookPath: func(name string) (string, error) {
			if p, ok := lookPath[name]; ok {
				return p, nil
			}
			return "", exec.ErrNotFound
		},
		ReadFile: os.ReadFile,
		Stat:     os.Stat,
		Getenv:   os.Getenv,
	}
	report, err := doctor.Check(ctx, cfg, l.Reader(), deps)
	if err != nil {
		t.Fatal(err)
	}
	var healthy bool
	for _, c := range report.Findings {
		if c.Category == "worktree" && c.Name == jobID && c.OK && strings.Contains(c.Detail, "branch main") {
			healthy = true
		}
	}
	if !healthy {
		t.Fatalf("want healthy worktree check, got %+v", report.Findings)
	}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	enabled := true
	return &config.Config{
		SchemaVersion: 1,
		General: config.General{
			DefaultTerminalAdapter: "process",
			ActiveProfile:          "balanced",
			WarnUsedPct:            85,
			ReserveUsedPct:         98,
		},
		Providers: map[string]config.Provider{
			"claude-opus": {
				Enabled: &enabled, Harness: config.HarnessClaudeCode, QualityTier: 5, ScarcityTier: 5, SpeedTier: 3,
				MaxConcurrent: 2, CostClass: "subscription",
				Sensor: config.Sensor{Mode: "auto", PollIntervalSec: 120},
			},
			"kimi-native": {
				Enabled: &enabled, Harness: config.HarnessKimiNative, QualityTier: 4, ScarcityTier: 2, SpeedTier: 4,
				MaxConcurrent: 2, CostClass: "subscription", APIKeyEnv: "KIMI_API_KEY",
				Sensor: config.Sensor{Mode: "auto", PollIntervalSec: 120},
			},
			"glm-claude": {
				Enabled: &enabled, Harness: config.HarnessGLMClaude, QualityTier: 3, ScarcityTier: 2, SpeedTier: 4,
				MaxConcurrent: 2, CostClass: "subscription", APIKeyEnv: "ZAI_API_KEY",
				Sensor: config.Sensor{Mode: "auto", PollIntervalSec: 120},
			},
		},
		Roles: map[string]config.Role{
			"default": {AllowedProviders: []string{"claude-opus", "kimi-native", "glm-claude"}, MinQualityTier: 2},
		},
	}
}

func openLedger(t *testing.T) (*ledger.Ledger, func() time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	l, err := ledger.Open(context.Background(), filepath.Join(t.TempDir(), "ledger.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, func() time.Time { return now }
}

func mustAppend(t *testing.T, l *ledger.Ledger, ev ledger.Event) {
	t.Helper()
	if err := l.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	if err := exec.CommandContext(ctx, "git", "-C", dir, "init").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.CommandContext(ctx, "git", "-C", dir, "config", "user.email", "t@t").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.CommandContext(ctx, "git", "-C", dir, "config", "user.name", "t").Run(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "f"), "x")
	if err := exec.CommandContext(ctx, "git", "-C", dir, "add", ".").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.CommandContext(ctx, "git", "-C", dir, "commit", "-m", "init").Run(); err != nil {
		t.Fatal(err)
	}
	if err := exec.CommandContext(ctx, "git", "-C", dir, "branch", "-m", "main").Run(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func ptrF(v float64) *float64 { return &v }

func timestampMS(t time.Time) string {
	return fmt.Sprintf("%d", t.UnixMilli())
}

func render(r doctor.Report) string {
	var b strings.Builder
	doctor.RenderText(&b, r)
	return b.String()
}

func TestDoctorTrustsARefreshTokenAndSkipsDisabledProviders(t *testing.T) {
	cfg := testConfig(t)
	off := false
	kimi := cfg.Providers["kimi-native"]
	kimi.Enabled, kimi.APIKeyEnv = &off, "KIMI_DOCTOR_UNSET"
	cfg.Providers["kimi-native"] = kimi
	l, now := openLedger(t)
	home := filepath.Join(t.TempDir(), "home")
	mkdir(t, filepath.Join(home, ".claude"))
	writeFile(t, filepath.Join(home, ".claude", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"stale-secret","refreshToken":"refresh-secret","expiresAt":`+timestampMS(now().Add(-3*time.Hour))+`}}`)
	deps := doctor.Deps{
		Now:      now,
		LookPath: func(name string) (string, error) { return "/bin/" + name, nil },
		ReadFile: os.ReadFile,
		Stat:     os.Stat,
		Getenv: func(name string) string {
			if name == "HOME" {
				return home
			}
			return ""
		},
	}
	report, err := doctor.Check(context.Background(), cfg, l.Reader(), deps)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range report.Findings {
		if c.Category == "auth" && c.Provider == "claude-opus" && c.Name == "claude token" && !c.OK {
			t.Errorf("an expired access token with a refresh token failed: %+v", c)
		}
		if c.Category == "auth" && c.Provider == "kimi-native" {
			t.Errorf("disabled provider checked: %+v", c)
		}
	}
	if out := render(report); strings.Contains(out, "stale-secret") || strings.Contains(out, "refresh-secret") {
		t.Fatal("doctor output leaked a token value")
	}
}

func TestDoctorProbesTheAIMemoryServerTheEngineUses(t *testing.T) {
	cfg := testConfig(t)
	cfg.Integrations.AIMemory = &config.AIMemory{Enabled: true}
	l, now := openLedger(t)
	var probed string
	deps := doctor.Deps{
		Now:      now,
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound },
		ReadFile: os.ReadFile,
		Stat:     os.Stat,
		Getenv:   func(string) string { return "" },
		HTTPGet:  func(_ context.Context, url string) error { probed = url; return nil },
		Exec:     func(context.Context, string, ...string) error { return nil },
	}
	if _, err := doctor.Check(context.Background(), cfg, l.Reader(), deps); err != nil {
		t.Fatal(err)
	}
	if probed != config.DefaultAIMemoryURL {
		t.Fatalf("probed %q, want the engine default %q", probed, config.DefaultAIMemoryURL)
	}
}
