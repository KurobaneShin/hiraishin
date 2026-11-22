package sensor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

type Sample struct {
	Window     string
	Kind       string
	UsedPct    *float64
	ResetsAt   time.Time
	Confidence string
	RawTokens  *int64
	Tier       string
}

var ErrRateLimited = errors.New("usage endpoint rate limited")

const (
	KimiUsagesURL   = "https://api.kimi.com/coding/v1/usages"
	ZAIQuotaURL     = "https://api.z.ai/api/monitor/usage/quota/limit"
	ZhipuQuotaURL   = "https://open.bigmodel.cn/api/monitor/usage/quota/limit"
	ClaudeUsageURL  = "https://api.anthropic.com/api/oauth/usage"
	claudeUsageBeta = "oauth-2025-04-20"
	agyUsageTimeout = 30 * time.Second
	maxEndpointBody = 1 << 20
)

func fetchJSON(ctx context.Context, client *http.Client, url, key string, extra ...[2]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	for _, h := range extra {
		req.Header.Set(h[0], h[1])
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hiraishin")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET usage endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEndpointBody))
	if err != nil {
		return nil, fmt.Errorf("read usage endpoint: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("usage endpoint auth failed: HTTP %d", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("usage endpoint: HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func pct(v float64) *float64 {
	v = min(max(v, 0), 100)
	return &v
}

func ParseKimiUsages(body []byte) ([]Sample, error) {
	var doc struct {
		Usages map[string]struct {
			UsedRatio *float64 `json:"used_ratio"`
			ResetTime string   `json:"reset_time"`
			ResetAlt  string   `json:"resetTime"`
		} `json:"usages"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse kimi usages: %w", err)
	}
	var out []Sample
	for key, name := range map[string]string{"limit_5h": "5h", "limit_7d": "weekly"} {
		u, ok := doc.Usages[key]
		if !ok || u.UsedRatio == nil {
			continue
		}
		kind := "rolling"
		if name == "weekly" {
			kind = "fixed_reset"
		}
		reset := u.ResetTime
		if reset == "" {
			reset = u.ResetAlt
		}
		at, _ := time.Parse(time.RFC3339Nano, reset)
		out = append(out, Sample{Window: name, Kind: kind, UsedPct: pct(*u.UsedRatio * 100), ResetsAt: at.UTC(), Confidence: ledger.ConfidenceExact, Tier: "endpoint"})
	}
	if len(out) == 0 {
		return nil, errors.New("parse kimi usages: no limit_5h or limit_7d window")
	}
	sortSamples(out)
	return out, nil
}

func ParseZAIQuota(body []byte) ([]Sample, error) {
	var doc struct {
		Data struct {
			Limits []struct {
				Type          string   `json:"type"`
				Unit          int      `json:"unit"`
				Number        int      `json:"number"`
				Usage         float64  `json:"usage"`
				CurrentValue  float64  `json:"currentValue"`
				Percentage    *float64 `json:"percentage"`
				NextResetTime int64    `json:"nextResetTime"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse z.ai quota: %w", err)
	}
	var out []Sample
	for _, l := range doc.Data.Limits {
		if l.Type != "TOKENS_LIMIT" && l.Type != "CREDIT_LIMIT" {
			continue
		}
		var name, kind string
		switch {
		case l.Unit == 3 && l.Number == 5:
			name, kind = "5h", "rolling"
		case l.Unit == 6 && l.Number == 1:
			name, kind = "weekly", "fixed_reset"
		default:
			continue
		}
		var used *float64
		switch {
		case l.Percentage != nil:
			used = pct(*l.Percentage)
		case l.Usage > 0:
			used = pct(l.CurrentValue / l.Usage * 100)
		}
		var reset time.Time
		if l.NextResetTime > 0 {
			reset = time.UnixMilli(l.NextResetTime).UTC()
		}
		out = append(out, Sample{Window: name, Kind: kind, UsedPct: used, ResetsAt: reset, Confidence: ledger.ConfidenceExact, Tier: "endpoint"})
	}
	if len(out) == 0 {
		return nil, errors.New("parse z.ai quota: no 5h or weekly token window")
	}
	sortSamples(out)
	return out, nil
}

func ParseClaudeUsage(body []byte) ([]Sample, error) {
	type window struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	}
	var doc struct {
		FiveHour *window `json:"five_hour"`
		SevenDay *window `json:"seven_day"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse claude usage: %w", err)
	}
	if doc.FiveHour == nil && doc.SevenDay == nil {
		return nil, errors.New("parse claude usage: no five_hour or seven_day window")
	}
	var out []Sample
	for _, w := range []struct {
		name, kind string
		win        *window
	}{{"5h", "rolling", doc.FiveHour}, {"weekly", "fixed_reset", doc.SevenDay}} {
		s := Sample{Window: w.name, Kind: w.kind, UsedPct: pct(0), Confidence: ledger.ConfidenceExact, Tier: "endpoint"}
		if w.win != nil && w.win.Utilization != nil {
			s.UsedPct = pct(*w.win.Utilization)
			at, _ := time.Parse(time.RFC3339Nano, w.win.ResetsAt)
			s.ResetsAt = at.UTC()
		}
		out = append(out, s)
	}
	return out, nil
}

func ParseAgyUsage(out []byte) ([]Sample, error) {
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var doc struct {
			Status  string `json:"status"`
			Command struct {
				Name string `json:"name"`
				Data struct {
					Groups []struct {
						Buckets []struct {
							Window            string   `json:"window"`
							RemainingFraction *float64 `json:"remaining_fraction"`
							ResetTime         string   `json:"reset_time"`
							Disabled          bool     `json:"disabled"`
						} `json:"buckets"`
					} `json:"groups"`
				} `json:"data"`
			} `json:"command"`
		}
		if json.Unmarshal([]byte(line), &doc) != nil || doc.Status != "SUCCESS" || doc.Command.Name != "usage" {
			continue
		}
		worst := map[string]Sample{}
		for _, g := range doc.Command.Data.Groups {
			for _, b := range g.Buckets {
				kind, ok := map[string]string{"5h": "rolling", "weekly": "fixed_reset"}[b.Window]
				if !ok || b.Disabled || b.RemainingFraction == nil {
					continue
				}
				used := pct((1 - *b.RemainingFraction) * 100)
				if cur, seen := worst[b.Window]; seen && *cur.UsedPct >= *used {
					continue
				}
				at, _ := time.Parse(time.RFC3339Nano, b.ResetTime)
				worst[b.Window] = Sample{Window: b.Window, Kind: kind, UsedPct: used, ResetsAt: at.UTC(), Confidence: ledger.ConfidenceExact, Tier: "cli"}
			}
		}
		var samples []Sample
		for _, w := range worst {
			samples = append(samples, w)
		}
		if len(samples) == 0 {
			return nil, errors.New("parse agy usage: no 5h or weekly bucket")
		}
		sortSamples(samples)
		return samples, nil
	}
	return nil, errors.New("parse agy usage: no usage JSON in agy output")
}

func ClaudeAccessToken(configDir string, now time.Time) (string, error) {
	b, err := os.ReadFile(filepath.Join(configDir, ".credentials.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var doc struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", fmt.Errorf("parse claude credentials: %w", err)
	}
	if doc.OAuth.ExpiresAt > 0 && !now.Before(time.UnixMilli(doc.OAuth.ExpiresAt)) {
		return "", nil
	}
	return doc.OAuth.AccessToken, nil
}

var openCodeUsageFamily = map[string]Family{
	"kimi-code-plan-cn": FamilyKimi,
	"zai-coding-plan":   FamilyGLM,
}

func OpenCodeKey(dataHome, providerID string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dataHome, "opencode", "auth.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var doc map[string]struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", fmt.Errorf("parse opencode auth: %w", err)
	}
	if e := doc[providerID]; e.Type == "api" {
		return e.Key, nil
	}
	return "", nil
}

func sortSamples(s []Sample) {
	if len(s) == 2 && s[0].Window > s[1].Window {
		s[0], s[1] = s[1], s[0]
	}
}

type Guard struct {
	mu   sync.Mutex
	next map[string]time.Time
	fail map[string]int
}

func NewGuard() *Guard { return &Guard{next: map[string]time.Time{}, fail: map[string]int{}} }

func (g *Guard) Allow(id string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !now.Before(g.next[id])
}

func (g *Guard) Done(id string, now time.Time, interval time.Duration, rateLimited bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	interval = max(interval, time.Duration(config.MinPollSeconds)*time.Second)
	if !rateLimited {
		g.fail[id] = 0
		g.next[id] = now.Add(interval)
		return
	}
	g.fail[id]++
	backoff := interval << min(g.fail[id], 5)
	g.next[id] = now.Add(backoff)
}

type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

type Probe struct {
	Client  *http.Client
	Getenv  func(string) string
	Home    string
	Run     Runner
	Now     func() time.Time
	URLs    map[Family]string
	Timeout time.Duration
}

func (p *Probe) endpointURL(f Family, prov config.Provider) string {
	if u, ok := p.URLs[f]; ok {
		return u
	}
	switch f {
	case FamilyClaude:
		return ClaudeUsageURL
	case FamilyKimi:
		return KimiUsagesURL
	case FamilyGLM:
		if prov.Sensor.Region == "cn" {
			return ZhipuQuotaURL
		}
		return ZAIQuotaURL
	}
	return ""
}

func (p *Probe) Sample(ctx context.Context, prov config.Provider) ([]Sample, error) {
	f := FamilyOf(prov)
	mode := prov.Sensor.Mode
	var errs []error
	if mode == "auto" || mode == "endpoint" {
		s, err := p.endpoint(ctx, f, prov)
		if err == nil && s != nil {
			return s, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if mode == "auto" || mode == "local_logs" {
		s, err := p.localLogs(f, prov)
		if err == nil && s != nil {
			return s, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if prov.Sensor.QuotaAxiFallback && p.Run != nil {
		s, err := p.quotaAxi(ctx, f)
		if err == nil {
			return s, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

func (p *Probe) endpoint(ctx context.Context, f Family, prov config.Provider) ([]Sample, error) {
	var key string
	var err error
	var extra [][2]string
	var parse func([]byte) ([]Sample, error)
	switch {
	case f == FamilyAgy && prov.Harness == config.HarnessAgy:
		return p.agyUsage(ctx)
	case f == FamilyClaude && prov.Harness == config.HarnessClaudeCode:
		key, err = ClaudeAccessToken(ClaudeDir(prov, p.Home), p.Now())
		extra, parse = [][2]string{{"anthropic-beta", claudeUsageBeta}}, ParseClaudeUsage
	case f == FamilyKimi:
		key, err = p.apiKey(prov)
		parse = ParseKimiUsages
	case f == FamilyGLM:
		key, err = p.apiKey(prov)
		parse = ParseZAIQuota
	}
	if err != nil || key == "" {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	body, err := fetchJSON(ctx, p.Client, p.endpointURL(f, prov), key, extra...)
	if err != nil {
		return nil, err
	}
	return parse(body)
}

func (p *Probe) agyUsage(ctx context.Context) ([]Sample, error) {
	if p.Run == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, agyUsageTimeout)
	defer cancel()
	out, err := p.Run(ctx, "env", "AGY_CLI_DISABLE_AUTO_UPDATE=true", "agy", "-p", "/usage", "--output-format", "json", "--print-timeout", "20s")
	if err != nil {
		return nil, fmt.Errorf("agy /usage: %w", err)
	}
	return ParseAgyUsage(out)
}

func (p *Probe) apiKey(prov config.Provider) (string, error) {
	if prov.APIKeyEnv != "" {
		return p.Getenv(prov.APIKeyEnv), nil
	}
	providerID, _, ok := strings.Cut(prov.Model, "/")
	if prov.Harness != config.HarnessOpenCode || !ok || openCodeUsageFamily[providerID] != FamilyOf(prov) {
		return "", nil
	}
	dataHome := ""
	if p.Getenv != nil {
		dataHome = p.Getenv("XDG_DATA_HOME")
	}
	if dataHome == "" {
		dataHome = filepath.Join(p.Home, ".local", "share")
	}
	return OpenCodeKey(dataHome, providerID)
}

func (p *Probe) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 10 * time.Second
}

func (p *Probe) localLogs(f Family, prov config.Provider) ([]Sample, error) {
	window := shortestWindow(prov)
	since := p.Now().Add(-time.Duration(window.DurationHours * float64(time.Hour)))
	var n int64
	var err error
	switch f {
	case FamilyClaude:
		if prov.Harness != config.HarnessClaudeCode {
			return nil, nil
		}
		dir := ClaudeDir(prov, p.Home)
		if fi, err := os.Lstat(filepath.Join(dir, "projects")); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return nil, nil
		}
		n, err = ClaudeTokens(dir, since)
	case FamilyAgy:
		n, err = AgyTurns(p.Home, since)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []Sample{{Window: window.Name, Kind: window.Kind, RawTokens: &n, Confidence: ledger.ConfidenceEstimated, Tier: "local_logs"}}, nil
}

func shortestWindow(prov config.Provider) config.Window {
	best := config.Window{Name: "5h", Kind: "rolling", DurationHours: 5}
	found := false
	for _, w := range prov.Windows {
		if w.DurationHours > 0 && (!found || w.DurationHours < best.DurationHours) {
			best, found = w, true
		}
	}
	return best
}

func (p *Probe) quotaAxi(ctx context.Context, f Family) ([]Sample, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	out, err := p.Run(ctx, "quota-axi", "--json")
	if err != nil {
		return nil, fmt.Errorf("quota-axi: %w", err)
	}
	return ParseQuotaAxi(out, f)
}

func ParseQuotaAxi(body []byte, f Family) ([]Sample, error) {
	var doc struct {
		Providers []struct {
			Provider string `json:"provider"`
			Windows  []struct {
				Kind        string   `json:"kind"`
				PercentUsed *float64 `json:"percentUsed"`
				ResetsAt    string   `json:"resetsAt"`
			} `json:"windows"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse quota-axi: %w", err)
	}
	want := map[Family]string{FamilyClaude: "claude", FamilyKimi: "kimi", FamilyGLM: "zai", FamilyAgy: "agy"}[f]
	var out []Sample
	for _, prov := range doc.Providers {
		if prov.Provider != want {
			continue
		}
		for _, w := range prov.Windows {
			nk, ok := quotaAxiWindows[w.Kind]
			if !ok || w.PercentUsed == nil {
				continue
			}
			at, _ := time.Parse(time.RFC3339Nano, w.ResetsAt)
			out = append(out, Sample{Window: nk[0], Kind: nk[1], UsedPct: pct(*w.PercentUsed), ResetsAt: at.UTC(), Confidence: ledger.ConfidenceExact, Tier: "quota-axi"})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("quota-axi: no windows for %s", want)
	}
	sortSamples(out)
	return out, nil
}

var quotaAxiWindows = map[string][2]string{"session": {"5h", "rolling"}, "weekly": {"weekly", "fixed_reset"}}
