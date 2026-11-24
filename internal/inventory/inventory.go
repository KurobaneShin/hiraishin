package inventory

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

type System struct {
	LookPath func(string) (string, error)
	ReadFile func(string) ([]byte, error)
	Getenv   func(string) string
	Stat     func(string) (fs.FileInfo, error)
	Glob     func(string) ([]string, error)
}

type Found struct {
	ID       string
	Binary   string
	Provider config.Provider
}

func Detect(sys System, claudeDirs []string) ([]Found, error) {
	var found []Found
	if path, err := sys.LookPath("claude"); err == nil {
		accounts, err := claudeAccounts(sys, claudeDirs)
		if err != nil {
			return nil, err
		}
		for _, acc := range accounts {
			p := provider(config.HarnessClaudeCode, "", "", 5, 4, 3, 2, acc.pool, "")
			p.ConfigDir = acc.dir
			found = append(found, Found{ID: acc.id, Binary: path, Provider: p})
		}
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
	if path, err := sys.LookPath("codex"); err == nil {
		found = append(found, Found{ID: "codex", Binary: path, Provider: provider(config.HarnessCodex, "", "", 4, 3, 4, 2, "openai-codex", "")})
	}
	if path, err := sys.LookPath("gemini"); err == nil {
		p := provider(config.HarnessGeminiCLI, "", "", 4, 2, 4, 2, "google-gemini-cli", "")
		p.Windows = []config.Window{{Name: "daily_rpd", Kind: "fixed_reset", DurationHours: 24, FixedResetCron: "0 0 * * *", FixedResetTZ: "America/Los_Angeles"}}
		found = append(found, Found{ID: "gemini", Binary: path, Provider: p})
	}
	return found, nil
}

type claudeAccount struct {
	id, dir, pool string
}

var labelChars = regexp.MustCompile(`[^a-z0-9]+`)

func claudeLabel(dir string) string {
	base := filepath.Base(dir)
	if slices.Contains([]string{"home", ".claude", "claude", ".config"}, base) {
		base = filepath.Base(filepath.Dir(dir))
	}
	label := strings.Trim(labelChars.ReplaceAllString(strings.ToLower(base), "-"), "-")
	return cmp.Or(label[:min(len(label), 8)], "alt")
}

func (sys System) isClaudeDir(dir string) bool {
	if fi, err := sys.Stat(dir); err != nil || !fi.IsDir() {
		return false
	}
	for _, marker := range []string{".credentials.json", ".claude.json", "projects"} {
		if _, err := sys.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

func (sys System) claudeAccountID(dir, def, home string) string {
	state := filepath.Join(dir, ".claude.json")
	if dir == def {
		state = filepath.Join(home, ".claude.json")
	}
	raw, err := sys.ReadFile(state)
	if err != nil {
		return ""
	}
	var doc struct {
		OAuthAccount struct {
			AccountUUID string `json:"accountUuid"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	return doc.OAuthAccount.AccountUUID
}

func claudeAccounts(sys System, patterns []string) ([]claudeAccount, error) {
	home := sys.Getenv("HOME")
	def := filepath.Join(home, ".claude")
	candidates := []string{def}
	if env := sys.Getenv("CLAUDE_CONFIG_DIR"); env != "" {
		candidates = append(candidates, env)
	}
	for _, pat := range patterns {
		matches, err := sys.Glob(config.ExpandHome(pat, home))
		if err != nil {
			return nil, fmt.Errorf("--claude-config-dir %q: %w", pat, err)
		}
		candidates = append(candidates, matches...)
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	var out []claudeAccount
	for _, dir := range candidates {
		dir = filepath.Clean(dir)
		if seen[dir] || !sys.isClaudeDir(dir) {
			continue
		}
		seen[dir] = true
		if acct := sys.claudeAccountID(dir, def, home); acct != "" {
			if seen["account:"+acct] {
				continue
			}
			seen["account:"+acct] = true
		}
		if dir == def {
			out = append(out, claudeAccount{id: "claude", pool: "claude-max"})
			ids["claude"] = true
			continue
		}
		label := claudeLabel(dir)
		id := "claude-" + label
		for n := 2; ids[id]; n++ {
			id = fmt.Sprintf("claude-%s-%d", label, n)
		}
		ids[id] = true
		stored := dir
		if rel, ok := strings.CutPrefix(dir, home+string(filepath.Separator)); ok && home != "" {
			stored = "~/" + rel
		}
		out = append(out, claudeAccount{id: id, dir: stored, pool: "claude-max-" + strings.TrimPrefix(id, "claude-")})
	}
	if len(out) == 0 {
		out = append(out, claudeAccount{id: "claude", pool: "claude-max"})
	}
	return out, nil
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
	windows := []config.Window{{Name: "5h", Kind: "rolling", DurationHours: 5}, {Name: "weekly", Kind: "fixed_reset", DurationHours: 168}}
	return config.Provider{
		Enabled: &enabled, Harness: harness, Model: model, HarnessAgent: agent, CostClass: "subscription",
		QualityTier: quality, ScarcityTier: scarcity, SpeedTier: speed, MaxConcurrent: maxConcurrent,
		SharedQuotaPool: pool, APIKeyEnv: keyEnv, Windows: windows,
		Sensor: config.Sensor{Mode: "auto", PollIntervalSec: config.MinPollSeconds},
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
