package sensor

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

func replay(t *testing.T, f Family, fixture string, now time.Time, step time.Duration) (Signal, bool) {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", "fixtures", fixture))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	tr := NewTracker()
	var last Signal
	found := false
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		if sig, ok := Classify(f, sc.Text(), now); ok {
			last, found = tr.Observe("p", sig, now), true
		}
		now = now.Add(step)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return last, found
}

func TestFixtures(t *testing.T) {
	now := time.Date(2026, 11, 21, 15, 9, 0, 0, time.UTC)
	tests := []struct {
		fixture string
		family  Family
		want    SignalKind
		window  string
		resetAt time.Time
	}{
		{"claude/limit_5h_epoch.txt", FamilyClaude, Exhausted, "5h", time.Unix(1763769600, 0).UTC()},
		{"claude/limit_5h_banner.txt", FamilyClaude, Exhausted, "5h", time.Date(2026, 11, 21, 20, 0, 0, 0, time.UTC)},
		{"claude/limit_weekly.txt", FamilyClaude, Exhausted, "weekly", time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)},
		{"claude/stream_limit_result.jsonl", FamilyClaude, Exhausted, "5h", time.Unix(1763769600, 0).UTC()},
		{"claude/rate_limit.jsonl", FamilyClaude, Degraded, "", time.Time{}},
		{"claude/overloaded.jsonl", FamilyClaude, Degraded, "", time.Time{}},
		{"claude/auth_expired.jsonl", FamilyClaude, AuthError, "", time.Time{}},
		{"claude/stream_normal.jsonl", FamilyClaude, "", "", time.Time{}},
		{"kimi/native_limit_5h.txt", FamilyKimi, Exhausted, "5h", time.Time{}},
		{"kimi/opencode_limit_5h.txt", FamilyKimi, Exhausted, "5h", time.Time{}},
		{"kimi/opencode_burst_429.txt", FamilyKimi, Degraded, "", time.Time{}},
		{"kimi/auth_401.txt", FamilyKimi, AuthError, "", time.Time{}},
		{"kimi/opencode_normal.jsonl", FamilyKimi, "", "", time.Time{}},
		{"glm/claude_1113.jsonl", FamilyGLM, Exhausted, "5h", time.Time{}},
		{"glm/opencode_limit.txt", FamilyGLM, Exhausted, "5h", time.Time{}},
		{"glm/rate_1302.jsonl", FamilyGLM, Degraded, "5h", time.Time{}},
		{"glm/rate_1302_x3.jsonl", FamilyGLM, Exhausted, "5h", time.Time{}},
		{"glm/auth_1001.jsonl", FamilyGLM, AuthError, "", time.Time{}},
		{"agy/429.txt", FamilyAgy, Degraded, "", time.Time{}},
		{"agy/429_x2.txt", FamilyAgy, Exhausted, "", time.Time{}},
		{"agy/401.txt", FamilyAgy, AuthError, "", time.Time{}},
		{"agy/normal.txt", FamilyAgy, "", "", time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			sig, ok := replay(t, tt.family, tt.fixture, now, 10*time.Second)
			if tt.want == "" {
				if ok {
					t.Fatalf("false positive: %+v", sig)
				}
				return
			}
			if !ok || sig.Kind != tt.want || sig.Window != tt.window {
				t.Fatalf("got %+v (found=%v), want %s window %q", sig, ok, tt.want, tt.window)
			}
			if !sig.ResetAt.Equal(tt.resetAt) {
				t.Errorf("reset = %v, want %v", sig.ResetAt, tt.resetAt)
			}
		})
	}
}

func TestEscalationWindowExpires(t *testing.T) {
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	sig, ok := replay(t, FamilyAgy, "agy/429_x2.txt", now, 6*time.Minute)
	if !ok || sig.Kind != Degraded {
		t.Fatalf("two 429s 6 minutes apart = %+v, want DEGRADED", sig)
	}
}

func TestFamilyOf(t *testing.T) {
	tests := []struct {
		p    config.Provider
		want Family
	}{
		{config.Provider{Harness: config.HarnessClaudeCode}, FamilyClaude},
		{config.Provider{Harness: config.HarnessKimiNative}, FamilyKimi},
		{config.Provider{Harness: config.HarnessGLMClaude}, FamilyGLM},
		{config.Provider{Harness: config.HarnessAgy}, FamilyAgy},
		{config.Provider{Harness: config.HarnessOpenCode, HarnessAgent: "kimi-worker", Model: "kimi-code-plan-cn/k3.1-pro"}, FamilyKimi},
		{config.Provider{Harness: config.HarnessOpenCode, HarnessAgent: "glm-strong", Model: "zai-coding-plan/glm-5.3"}, FamilyGLM},
		{config.Provider{Harness: config.HarnessCodex}, FamilyUnknown},
	}
	for _, tt := range tests {
		if got := FamilyOf(tt.p); got != tt.want {
			t.Errorf("FamilyOf(%+v) = %q, want %q", tt.p, got, tt.want)
		}
	}
}

func TestSessionID(t *testing.T) {
	for line, want := range map[string]string{
		`{"type":"system","subtype":"init","session_id":"0b5f-claude"}`: "0b5f-claude",
		`{"type":"step_start","sessionID":"ses_opencode","part":{}}`:    "ses_opencode",
		`  {"type":"assistant","session_id":"padded"}  `:                "padded",
		`{"type":"text","part":{"text":"no session here"}}`:             "",
		`plain stderr mentioning session_id`:                            "",
		`{"session_id": broken json`:                                    "",
	} {
		if got := SessionID(line); got != want {
			t.Errorf("SessionID(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestSessionIDFromRealOutput(t *testing.T) {
	for fixture, want := range map[string]string{
		"claude/stream_session.jsonl": "383189bb-beef-40f3-b5bc-ebc6e9bd40d8",
		"agy/stream_session.jsonl":    "ec1ed19a-3184-4492-a5ea-e476872ad084",
		"kimi/opencode_session.jsonl": "ses_dffacd84effeNAPzFe2haThJSy",
		"kimi/native_session.jsonl":   "session_e83dce87-13ae-4e26-ac84-6217302c0e43",
		"glm/claude_session.jsonl":    "27188b27-d2ef-458e-a60d-3c77fc5ce8cd",
	} {
		b, err := os.ReadFile(filepath.Join("testdata", "fixtures", fixture))
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		for line := range strings.Lines(string(b)) {
			if got = SessionID(line); got != "" {
				break
			}
		}
		if got != want {
			t.Errorf("%s: session id = %q, want %q", fixture, got, want)
		}
	}
}
