package config

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/KurobaneShin/hiraishin/internal/forecast"
)

const SchemaVersion = 1

type Config struct {
	SchemaVersion int                 `toml:"schema_version"`
	General       General             `toml:"general"`
	Profiles      map[string]Weights  `toml:"profiles,omitempty"`
	Learning      Learning            `toml:"learning"`
	Providers     map[string]Provider `toml:"providers"`
	Roles         map[string]Role     `toml:"roles"`
	Integrations  Integrations        `toml:"integrations,omitempty"`
}

type General struct {
	DefaultTerminalAdapter string  `toml:"default_terminal_adapter"`
	ActiveProfile          string  `toml:"active_profile"`
	WarnUsedPct            float64 `toml:"warn_used_pct"`
	ReserveUsedPct         float64 `toml:"reserve_used_pct"`
	ReserveAllowFailover   *bool   `toml:"reserve_allow_failover,omitempty"`
	AllowMeteredFallback   bool    `toml:"allow_metered_fallback"`
	FailoverWaitMaxMin     *int    `toml:"failover_wait_max_min,omitempty"`
}

type Weights struct {
	Quality  float64 `toml:"quality" json:"quality"`
	Expiry   float64 `toml:"expiry" json:"expiry"`
	Scarcity float64 `toml:"scarcity" json:"scarcity"`
	Latency  float64 `toml:"latency" json:"latency"`
}

type Learning struct {
	Enabled             bool    `toml:"enabled"`
	Frozen              bool    `toml:"frozen"`
	MinSamples          int     `toml:"min_samples,omitempty"`
	MaxDelta            float64 `toml:"max_delta,omitempty"`
	EWMAAlpha           float64 `toml:"ewma_alpha,omitempty"`
	WeightCompletion    float64 `toml:"weight_completion,omitempty"`
	WeightReviewRounds  float64 `toml:"weight_review_rounds,omitempty"`
	WeightBuildFailures float64 `toml:"weight_build_failures,omitempty"`
	WeightDuration      float64 `toml:"weight_duration,omitempty"`
}

type Provider struct {
	Enabled         *bool    `toml:"enabled,omitempty"`
	Harness         string   `toml:"harness"`
	Model           string   `toml:"model,omitempty"`
	HarnessAgent    string   `toml:"harness_agent,omitempty"`
	Effort          string   `toml:"effort,omitempty"`
	CostClass       string   `toml:"cost_class"`
	QualityTier     int      `toml:"quality_tier"`
	ScarcityTier    int      `toml:"scarcity_tier"`
	SpeedTier       int      `toml:"speed_tier"`
	MaxConcurrent   int      `toml:"max_concurrent"`
	LaunchPrefix    []string `toml:"launch_prefix,omitempty"`
	SharedQuotaPool string   `toml:"shared_quota_pool,omitempty"`
	APIKeyEnv       string   `toml:"api_key_env,omitempty"`
	ConfigDir       string   `toml:"config_dir,omitempty"`
	Windows         []Window `toml:"windows,omitempty"`
	Sensor          Sensor   `toml:"sensor,omitempty"`
}

type Window struct {
	Name           string  `toml:"name"`
	Kind           string  `toml:"kind"`
	DurationHours  float64 `toml:"duration_hours,omitempty"`
	FixedResetCron string  `toml:"fixed_reset_cron,omitempty"`
	FixedResetTZ   string  `toml:"fixed_reset_tz,omitempty"`
}

type Sensor struct {
	Mode             string `toml:"mode,omitempty"`
	PollIntervalSec  int    `toml:"poll_interval_sec,omitempty"`
	QuotaAxiFallback bool   `toml:"quota_axi_fallback,omitempty"`
	Region           string `toml:"region,omitempty"`
}

type Role struct {
	AllowedProviders     []string `toml:"allowed_providers"`
	MinQualityTier       int      `toml:"min_quality_tier"`
	DenyProviders        []string `toml:"deny_providers,omitempty"`
	PinProvider          string   `toml:"pin_provider,omitempty"`
	NeverSwitchMidTask   bool     `toml:"never_switch_mid_task"`
	ProfileOverride      string   `toml:"profile_override,omitempty"`
	ResumePromptTemplate string   `toml:"resume_prompt_template,omitempty"`
}

type Integrations struct {
	AIMemory *AIMemory `toml:"ai_memory,omitempty"`
	QMD      *QMD      `toml:"qmd,omitempty"`
	Graphify *Graphify `toml:"graphify,omitempty"`
}

type AIMemory struct {
	Enabled        bool   `toml:"enabled"`
	Transport      string `toml:"transport,omitempty"`
	URL            string `toml:"url,omitempty"`
	RecordOutcomes bool   `toml:"record_outcomes"`
}

type QMD struct {
	Enabled     bool     `toml:"enabled"`
	Collections []string `toml:"collections,omitempty"`
	MaxSnippets int      `toml:"max_snippets,omitempty"`
}

type Graphify struct {
	Enabled          bool `toml:"enabled"`
	AutoSizeTier     bool `toml:"auto_size_tier"`
	MaxContextTokens int  `toml:"max_context_tokens,omitempty"`
}

const (
	HarnessClaudeCode = "claude-code"
	HarnessKimiNative = "kimi-native"
	HarnessGLMClaude  = "glm-claude"
	HarnessOpenCode   = "opencode"
	HarnessAgy        = "antigravity-agy"
	HarnessCodex      = "codex"
	HarnessGeminiCLI  = "gemini-cli"
)

var (
	harnesses   = []string{HarnessClaudeCode, HarnessKimiNative, HarnessGLMClaude, HarnessOpenCode, HarnessAgy, HarnessCodex, HarnessGeminiCLI}
	adapters    = []string{"process", "tmux", "orca"}
	costClasses = []string{"subscription", "free", "metered"}
	windowKinds = []string{"rolling", "fixed_reset", "credit_pool", "metered"}
	sensorModes = []string{"auto", "endpoint", "local_logs", "output_regex", "none"}
	envName     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

const MinPollSeconds = 120

var BuiltinProfiles = map[string]Weights{
	"quality-first":       {Quality: 0.60, Expiry: 0.15, Scarcity: 0.15, Latency: 0.10},
	"burn-expiring-first": {Quality: 0.20, Expiry: 0.50, Scarcity: 0.20, Latency: 0.10},
	"save-scarce":         {Quality: 0.20, Expiry: 0.25, Scarcity: 0.45, Latency: 0.10},
	"balanced":            {Quality: 0.35, Expiry: 0.30, Scarcity: 0.25, Latency: 0.10},
}

const CustomProfile = "custom"

func (p Provider) IsEnabled() bool { return p.Enabled == nil || *p.Enabled }

func (g General) AllowFailoverPastReserve() bool {
	return g.ReserveAllowFailover == nil || *g.ReserveAllowFailover
}

const DefaultFailoverWaitMaxMin = 50

const DefaultAIMemoryURL = "http://127.0.0.1:49374/mcp"

func (g General) FailoverWaitMax() time.Duration {
	if g.FailoverWaitMaxMin == nil {
		return DefaultFailoverWaitMaxMin * time.Minute
	}
	return time.Duration(*g.FailoverWaitMaxMin) * time.Minute
}

func (c *Config) ProfileWeights(name string) (Weights, error) {
	w, ok := BuiltinProfiles[name]
	if name == CustomProfile {
		w, ok = c.Profiles[CustomProfile]
	}
	if !ok {
		return Weights{}, fmt.Errorf("unknown profile %q", name)
	}
	sum := w.Quality + w.Expiry + w.Scarcity + w.Latency
	if sum <= 0 {
		return Weights{}, fmt.Errorf("profile %q: weights must sum to more than 0", name)
	}
	return Weights{Quality: w.Quality / sum, Expiry: w.Expiry / sum, Scarcity: w.Scarcity / sum, Latency: w.Latency / sum}, nil
}

func (c *Config) Role(name string) (Role, string, error) {
	if r, ok := c.Roles[name]; ok {
		return r, name, nil
	}
	if r, ok := c.Roles["default"]; ok {
		return r, "default", nil
	}
	return Role{}, "", fmt.Errorf("role %q is not configured and no [roles.default] exists", name)
}

func (c *Config) PoolMembers(providerID string) []string {
	pool := c.Providers[providerID].SharedQuotaPool
	if pool == "" {
		return []string{providerID}
	}
	var out []string
	for id, p := range c.Providers {
		if p.SharedQuotaPool == pool {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func (c *Config) ProviderIDs() []string {
	ids := make([]string, 0, len(c.Providers))
	for id := range c.Providers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func Load(path string) (*Config, error) {
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.General.DefaultTerminalAdapter == "" {
		c.General.DefaultTerminalAdapter = "process"
	}
	if c.General.ActiveProfile == "" {
		c.General.ActiveProfile = "balanced"
	}
	if c.General.WarnUsedPct == 0 {
		c.General.WarnUsedPct = 85
	}
	if c.General.ReserveUsedPct == 0 {
		c.General.ReserveUsedPct = 98
	}
	c.Learning.applyDefaults()
	for id, p := range c.Providers {
		if p.CostClass == "" {
			p.CostClass = "subscription"
		}
		if p.Sensor.Mode == "" {
			p.Sensor.Mode = "auto"
		}
		if p.Sensor.PollIntervalSec == 0 {
			p.Sensor.PollIntervalSec = MinPollSeconds
		}
		c.Providers[id] = p
	}
}

func (c *Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if c.SchemaVersion != SchemaVersion {
		add("schema_version = %d, want %d", c.SchemaVersion, SchemaVersion)
	}
	if !slices.Contains(adapters, c.General.DefaultTerminalAdapter) {
		add("general.default_terminal_adapter %q: want one of %v", c.General.DefaultTerminalAdapter, adapters)
	}
	if _, err := c.ProfileWeights(c.General.ActiveProfile); err != nil {
		add("general.active_profile: %v", err)
	}
	g := c.General
	if g.WarnUsedPct <= 0 || g.WarnUsedPct > 100 || g.ReserveUsedPct <= 0 || g.ReserveUsedPct > 100 || g.WarnUsedPct > g.ReserveUsedPct {
		add("general: want 0 < warn_used_pct <= reserve_used_pct <= 100, got %.1f and %.1f", g.WarnUsedPct, g.ReserveUsedPct)
	}
	if len(c.Providers) == 0 {
		add("no [providers] configured")
	}
	for _, id := range c.ProviderIDs() {
		errs = append(errs, c.validateProvider(id, c.Providers[id])...)
	}
	for name, r := range c.Roles {
		errs = append(errs, c.validateRole(name, r)...)
	}
	errs = append(errs, c.validateClaudeAccounts()...)
	errs = append(errs, c.Learning.validate()...)
	return errors.Join(errs...)
}

const MaxLearnDelta = 0.5

func (l *Learning) applyDefaults() {
	if l.MinSamples == 0 {
		l.MinSamples = 5
	}
	if l.MaxDelta == 0 {
		l.MaxDelta = 0.12
	}
	if l.EWMAAlpha == 0 {
		l.EWMAAlpha = 0.25
	}
	if l.WeightCompletion == 0 && l.WeightReviewRounds == 0 && l.WeightBuildFailures == 0 && l.WeightDuration == 0 {
		l.WeightCompletion, l.WeightReviewRounds, l.WeightBuildFailures, l.WeightDuration = 0.45, 0.25, 0.20, 0.10
	}
}

func (l Learning) validate() []error {
	var errs []error
	if l.MaxDelta < 0 || l.MaxDelta > MaxLearnDelta {
		errs = append(errs, fmt.Errorf("learning.max_delta = %g: want 0 <= max_delta <= %g", l.MaxDelta, MaxLearnDelta))
	}
	if l.EWMAAlpha < 0 || l.EWMAAlpha > 1 {
		errs = append(errs, fmt.Errorf("learning.ewma_alpha = %g: want 0 < ewma_alpha <= 1", l.EWMAAlpha))
	}
	if l.MinSamples < 0 {
		errs = append(errs, fmt.Errorf("learning.min_samples = %d: want >= 0", l.MinSamples))
	}
	if min(l.WeightCompletion, l.WeightReviewRounds, l.WeightBuildFailures, l.WeightDuration) < 0 {
		errs = append(errs, fmt.Errorf("learning weights must be >= 0"))
	}
	return errs
}

func ExpandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

func (c *Config) validateClaudeAccounts() []error {
	var errs []error
	owner := map[string]string{}
	for _, id := range c.ProviderIDs() {
		p := c.Providers[id]
		if p.Harness != HarnessClaudeCode {
			continue
		}
		dir := filepath.Clean(cmp.Or(p.ConfigDir, "~/.claude"))
		other, dup := owner[dir]
		if !dup {
			owner[dir] = id
			continue
		}
		if q := c.Providers[other]; q.SharedQuotaPool == "" || q.SharedQuotaPool != p.SharedQuotaPool {
			errs = append(errs, fmt.Errorf("providers.%s and providers.%s use the same Claude account (config_dir %s): set different config_dir values or the same shared_quota_pool", other, id, dir))
		}
	}
	return errs
}

func (c *Config) validateProvider(id string, p Provider) []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("providers.%s: "+format, append([]any{id}, args...)...))
	}
	if !slices.Contains(harnesses, p.Harness) {
		add("harness %q: want one of %v", p.Harness, harnesses)
	}
	if !slices.Contains(costClasses, p.CostClass) {
		add("cost_class %q: want one of %v", p.CostClass, costClasses)
	}
	for name, v := range map[string]int{"quality_tier": p.QualityTier, "scarcity_tier": p.ScarcityTier, "speed_tier": p.SpeedTier} {
		if v < 1 || v > 5 {
			add("%s = %d, want 1..5", name, v)
		}
	}
	if p.MaxConcurrent < 0 {
		add("max_concurrent = %d, want >= 0 (0 or unset: no fixed cap)", p.MaxConcurrent)
	}
	if p.APIKeyEnv != "" && !envName.MatchString(p.APIKeyEnv) {
		add("api_key_env must name an environment variable (A-Z, 0-9, _), never hold the key itself")
	}
	if p.ConfigDir != "" {
		if p.Harness != HarnessClaudeCode && p.Harness != HarnessGLMClaude {
			add("config_dir is only supported for claude-code and glm-claude")
		}
		if !filepath.IsAbs(p.ConfigDir) && p.ConfigDir != "~" && !strings.HasPrefix(p.ConfigDir, "~/") {
			add("config_dir %q must be an absolute path or start with ~/", p.ConfigDir)
		}
	}
	if p.Harness == HarnessGLMClaude && p.APIKeyEnv == "" {
		add("harness glm-claude needs api_key_env (the variable holding the Z.AI key)")
	}
	if !slices.Contains(sensorModes, p.Sensor.Mode) {
		add("sensor.mode %q: want one of %v", p.Sensor.Mode, sensorModes)
	}
	if p.Sensor.PollIntervalSec < MinPollSeconds {
		add("sensor.poll_interval_sec = %d, want >= %d", p.Sensor.PollIntervalSec, MinPollSeconds)
	}
	seen := map[string]bool{}
	for _, w := range p.Windows {
		if w.Name == "" || seen[w.Name] {
			add("window name %q is empty or repeated", w.Name)
		}
		seen[w.Name] = true
		if !slices.Contains(windowKinds, w.Kind) {
			add("window %q kind %q: want one of %v", w.Name, w.Kind, windowKinds)
		}
		if w.Kind == "rolling" && w.DurationHours <= 0 {
			add("window %q: rolling windows need duration_hours > 0", w.Name)
		}
		if w.FixedResetCron != "" {
			if _, err := forecast.NextReset(w.FixedResetCron, w.FixedResetTZ, time.Time{}); err != nil {
				add("window %q: %v", w.Name, err)
			}
		}
	}
	return errs
}

func (c *Config) validateRole(name string, r Role) []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("roles.%s: "+format, append([]any{name}, args...)...))
	}
	if len(r.AllowedProviders) == 0 {
		add("allowed_providers is empty")
	}
	for _, list := range [][]string{r.AllowedProviders, r.DenyProviders} {
		for _, id := range list {
			if _, ok := c.Providers[id]; !ok {
				add("unknown provider %q", id)
			}
		}
	}
	if r.PinProvider != "" {
		if _, ok := c.Providers[r.PinProvider]; !ok {
			add("pin_provider %q is not configured", r.PinProvider)
		}
	}
	if r.MinQualityTier < 0 || r.MinQualityTier > 5 {
		add("min_quality_tier = %d, want 0..5", r.MinQualityTier)
	}
	if r.ProfileOverride != "" {
		if _, err := c.ProfileWeights(r.ProfileOverride); err != nil {
			add("profile_override: %v", err)
		}
	}
	return errs
}

func Write(path string, c *Config, force bool) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("refusing to write an invalid config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		_ = f.Close()
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return f.Close()
}

type Paths struct {
	Config  string
	DataDir string
}

func ResolvePaths(getenv func(string) string) Paths {
	home := getenv("HOME")
	cfg := getenv("HIRAISHIN_CONFIG")
	if cfg == "" {
		base := getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		cfg = filepath.Join(base, "hiraishin", "preferences.toml")
	}
	data := getenv("HIRAISHIN_DATA_DIR")
	if data == "" {
		base := getenv("XDG_DATA_HOME")
		if base == "" {
			base = filepath.Join(home, ".local", "share")
		}
		data = filepath.Join(base, "hiraishin")
	}
	return Paths{Config: cfg, DataDir: data}
}

func (p Paths) Ledger() string { return filepath.Join(p.DataDir, "ledger.db") }
