package sensor

import (
	"cmp"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

type Family string

const (
	FamilyClaude  Family = "claude"
	FamilyKimi    Family = "kimi"
	FamilyGLM     Family = "glm"
	FamilyAgy     Family = "agy"
	FamilyUnknown Family = ""
)

func FamilyOf(p config.Provider) Family {
	switch p.Harness {
	case config.HarnessClaudeCode:
		return FamilyClaude
	case config.HarnessKimiNative:
		return FamilyKimi
	case config.HarnessGLMClaude:
		return FamilyGLM
	case config.HarnessAgy:
		return FamilyAgy
	case config.HarnessOpenCode:
		id := strings.ToLower(p.Model + " " + p.HarnessAgent)
		switch {
		case strings.Contains(id, "kimi"):
			return FamilyKimi
		case strings.Contains(id, "glm"), strings.Contains(id, "zai"):
			return FamilyGLM
		}
	}
	return FamilyUnknown
}

type SignalKind string

const (
	Exhausted SignalKind = "EXHAUSTED"
	Degraded  SignalKind = "DEGRADED"
	AuthError SignalKind = "AUTH_ERROR"
)

type Escalation struct {
	Count  int
	Within time.Duration
}

type Signal struct {
	Kind     SignalKind
	Pattern  string
	Window   string
	Excerpt  string
	ResetAt  time.Time
	Cooldown time.Duration
	Escalate *Escalation
}

type rule struct {
	id       string
	kind     SignalKind
	re       *regexp.Regexp
	window   string
	cooldown time.Duration
	escalate *Escalation
	reset    func(line string, now time.Time) time.Time
}

func rules(f Family) []rule {
	switch f {
	case FamilyClaude:
		return claudeRules
	case FamilyKimi:
		return kimiRules
	case FamilyGLM:
		return glmRules
	case FamilyAgy:
		return agyRules
	}
	return nil
}

var (
	claudeRules = []rule{
		{id: "claude.auth", kind: AuthError, re: regexp.MustCompile(`authentication_error|OAuth token has expired|API Error: 401`)},
		{id: "claude.limit_weekly", kind: Exhausted, window: "weekly", re: regexp.MustCompile(`(?i)weekly usage limit|weekly limit`), reset: claudeReset},
		{id: "claude.limit_5h", kind: Exhausted, window: "5h", re: regexp.MustCompile(`(?i)usage limit reached|hit your limit`), reset: claudeReset},
		{id: "claude.rate_limit", kind: Degraded, cooldown: time.Minute, re: regexp.MustCompile(`rate_limit_error`)},
		{id: "claude.overloaded", kind: Degraded, cooldown: 30 * time.Second, re: regexp.MustCompile(`overloaded_error|API Error: 529`)},
	}
	kimiRules = []rule{
		{id: "kimi.auth", kind: AuthError, re: regexp.MustCompile(`(?i)invalid_api_key|\b401\b.{0,40}unauthori[sz]ed|(error|status)\W{0,3}401\b`)},
		{id: "kimi.limit_weekly", kind: Exhausted, window: "weekly", re: regexp.MustCompile(`(?i)reached your (weekly|monthly) usage limit`)},
		{id: "kimi.limit_5h", kind: Exhausted, window: "5h", re: regexp.MustCompile(`(?i)reached your 5-hour usage limit|5-hour usage limit`)},
		{id: "kimi.burst_429", kind: Degraded, cooldown: time.Minute, re: regexp.MustCompile(`(?i)\b429\b|too many requests`)},
	}
	glmRules = []rule{
		{id: "glm.auth", kind: AuthError, re: regexp.MustCompile(`(?i)invalid api key|"code"\s*:\s*"?1001\b|(error|status)\W{0,3}401\b|authentication_error`)},
		{id: "glm.limit_1113", kind: Exhausted, window: "5h", re: regexp.MustCompile(`(?i)GLM Coding Plan usage limit|resource package|"code"\s*:\s*"?1113\b`)},
		{id: "glm.rate_1302", kind: Degraded, cooldown: 45 * time.Second, escalate: &Escalation{Count: 3, Within: 2 * time.Minute}, window: "5h",
			re: regexp.MustCompile(`(?i)"code"\s*:\s*"?1302\b|rate limit reached`)},
	}
	agyRules = []rule{
		{id: "agy.auth", kind: AuthError, re: regexp.MustCompile(`UNAUTHENTICATED|(?i)error\W{0,3}401\b`)},
		{id: "agy.quota_daily", kind: Exhausted, re: regexp.MustCompile(`(?i)quota exceeded.{0,80}per day|daily (quota|limit)`)},
		{id: "agy.quota_429", kind: Degraded, cooldown: time.Minute, escalate: &Escalation{Count: 2, Within: 5 * time.Minute},
			re: regexp.MustCompile(`RESOURCE_EXHAUSTED|(?i)error\W{0,3}429\b|quota exceeded`)},
	}
)

const maxExcerpt = 240

func Classify(f Family, line string, now time.Time) (Signal, bool) {
	text, ok := classifiable(line)
	if !ok {
		return Signal{}, false
	}
	for _, r := range rules(f) {
		if !r.re.MatchString(text) {
			continue
		}
		sig := Signal{Kind: r.kind, Pattern: r.id, Window: r.window, Excerpt: excerpt(line), Cooldown: r.cooldown, Escalate: r.escalate}
		if r.reset != nil {
			sig.ResetAt = r.reset(text, now)
		}
		return sig, true
	}
	return Signal{}, false
}

func classifiable(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}
	if !strings.HasPrefix(trimmed, "{") {
		return trimmed, true
	}
	var ev map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
		return trimmed, true
	}
	if _, ok := ev["error"]; ok {
		return trimmed, true
	}
	var typ string
	_ = json.Unmarshal(ev["type"], &typ)
	switch typ {
	case "error":
		return trimmed, true
	case "result":
		var isErr bool
		_ = json.Unmarshal(ev["is_error"], &isErr)
		return trimmed, isErr
	}
	return "", false
}

func excerpt(line string) string {
	s := strings.TrimSpace(line)
	if len(s) > maxExcerpt {
		s = s[:maxExcerpt]
	}
	return s
}

func SessionID(line string) string {
	if !strings.Contains(line, `"session_id"`) && !strings.Contains(line, `"sessionID"`) && !strings.Contains(line, `"conversation_id"`) {
		return ""
	}
	var ev struct {
		Snake        string `json:"session_id"`
		Camel        string `json:"sessionID"`
		Conversation string `json:"conversation_id"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(line)), &ev) != nil {
		return ""
	}
	return cmp.Or(ev.Snake, ev.Camel, ev.Conversation)
}
