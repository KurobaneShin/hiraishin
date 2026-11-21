package inventory

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

type System struct {
	LookPath func(string) (string, error)
	ReadFile func(string) ([]byte, error)
	Getenv   func(string) string
}

type Found struct {
	ID       string
	Binary   string
	Provider config.Provider
}

func Detect(sys System) ([]Found, error) {
	var found []Found
	if path, err := sys.LookPath("claude"); err == nil {
		found = append(found, Found{ID: "claude", Binary: path, Provider: provider(config.HarnessClaudeCode, "", "", 5, 4, 3, 2, "claude-max", "")})
		if sys.Getenv("ZAI_API_KEY") != "" {
			found = append(found, Found{ID: "glm-claude", Binary: path, Provider: provider(config.HarnessGLMClaude, "", "", 3, 2, 4, 2, "zai-coding-plan", "ZAI_API_KEY")})
		}
	}
	if path, err := sys.LookPath("kimi"); err == nil {
		found = append(found, Found{ID: "kimi-native", Binary: path, Provider: provider(config.HarnessKimiNative, "", "", 4, 2, 4, 2, "kimi-code", "")})
	}
	if path, err := sys.LookPath("opencode"); err == nil {
		agents, err := openCodeAgents(sys)
		if err != nil {
			return nil, err
		}
		for _, a := range agents {
			a.Binary = path
			found = append(found, a)
		}
	}
	if path, err := sys.LookPath("agy"); err == nil {
		found = append(found, Found{ID: "agy", Binary: path, Provider: provider(config.HarnessAgy, "", "", 4, 3, 4, 1, "google-antigravity", "")})
	}
	return found, nil
}

func openCodeAgents(sys System) ([]Found, error) {
	base := sys.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(sys.Getenv("HOME"), ".config")
	}
	raw, err := sys.ReadFile(filepath.Join(base, "opencode", "opencode.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Agent map[string]struct {
			Model string `json:"model"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(doc.Agent))
	for name := range doc.Agent {
		names = append(names, name)
	}
	slices.Sort(names)
	var out []Found
	for _, name := range names {
		model := doc.Agent[name].Model
		fast := strings.Contains(name, "fast") || strings.Contains(model, "flash") || strings.Contains(model, "highspeed")
		switch {
		case strings.Contains(name, "kimi") && strings.Contains(model, "kimi"):
			q := 4
			if fast {
				q = 3
			}
			out = append(out, Found{ID: "opencode-" + name, Provider: provider(config.HarnessOpenCode, model, name, q, 2, 4, 2, "kimi-code", "")})
		case strings.Contains(name, "glm") && (strings.Contains(model, "glm") || strings.Contains(model, "zai")):
			q := 3
			if fast {
				q = 2
			}
			out = append(out, Found{ID: "opencode-" + name, Provider: provider(config.HarnessOpenCode, model, name, q, 2, 4, 2, "zai-coding-plan", "")})
		}
	}
	return out, nil
}

func provider(harness, model, agent string, quality, scarcity, speed, maxConcurrent int, pool, keyEnv string) config.Provider {
	enabled := true
	windows := []config.Window{{Name: "5h", Kind: "rolling", DurationHours: 5}}
	if harness != config.HarnessAgy {
		windows = append(windows, config.Window{Name: "weekly", Kind: "fixed_reset", DurationHours: 168})
	}
	mode := "output_regex"
	if keyEnv != "" {
		mode = "auto"
	}
	return config.Provider{
		Enabled: &enabled, Harness: harness, Model: model, HarnessAgent: agent, CostClass: "subscription",
		QualityTier: quality, ScarcityTier: scarcity, SpeedTier: speed, MaxConcurrent: maxConcurrent,
		SharedQuotaPool: pool, APIKeyEnv: keyEnv, Windows: windows,
		Sensor: config.Sensor{Mode: mode, PollIntervalSec: config.MinPollSeconds},
	}
}

func Build(found []Found, adapter string) *config.Config {
	c := &config.Config{
		SchemaVersion: config.SchemaVersion,
		General: config.General{
			DefaultTerminalAdapter: adapter, ActiveProfile: "burn-expiring-first",
			WarnUsedPct: 85, ReserveUsedPct: 98,
		},
		Providers: map[string]config.Provider{},
		Roles:     map[string]config.Role{},
	}
	ids := make([]string, 0, len(found))
	for _, f := range found {
		if !f.Provider.IsEnabled() {
			continue
		}
		c.Providers[f.ID] = f.Provider
		ids = append(ids, f.ID)
	}
	if len(ids) > 0 {
		c.Roles["default"] = config.Role{AllowedProviders: ids, MinQualityTier: 2}
	}
	return c
}
