package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "preferences.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDocExamplesValidate(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "preferences.md"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```toml\n(.*?)```").FindAllStringSubmatch(string(doc), -1)
	n := 0
	for _, b := range blocks {
		if strings.Contains(b[1], "<provider_id>") {
			continue
		}
		n++
		if _, err := Load(writeFile(t, b[1])); err != nil {
			t.Errorf("example %d: %v", n, err)
		}
	}
	if n != 3 {
		t.Fatalf("found %d complete examples, want 3", n)
	}
}

const minimal = `
schema_version = 1
[providers.a]
harness = "kimi-native"
quality_tier = 4
scarcity_tier = 2
speed_tier = 4
[[providers.a.windows]]
name = "5h"
kind = "rolling"
duration_hours = 5
[roles.default]
allowed_providers = ["a"]
min_quality_tier = 2
`

func TestLoadDefaults(t *testing.T) {
	c, err := Load(writeFile(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	p := c.Providers["a"]
	if !p.IsEnabled() || p.MaxConcurrent != 1 || p.CostClass != "subscription" || p.Sensor.PollIntervalSec != MinPollSeconds {
		t.Errorf("defaults not applied: %+v", p)
	}
	if c.General.ActiveProfile != "balanced" || c.General.ReserveUsedPct != 98 || !c.General.AllowFailoverPastReserve() {
		t.Errorf("general defaults: %+v", c.General)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(string) string
		wantErr string
	}{
		{"unknown key", func(s string) string { return s + "\n[general]\nbogus = 1\n" }, "unknown keys: general.bogus"},
		{"bad schema", func(s string) string { return strings.Replace(s, "schema_version = 1", "schema_version = 2", 1) }, "schema_version = 2"},
		{"bad harness", func(s string) string { return strings.Replace(s, "kimi-native", "kimi-cloud", 1) }, "harness \"kimi-cloud\""},
		{"tier range", func(s string) string { return strings.Replace(s, "quality_tier = 4", "quality_tier = 9", 1) }, "quality_tier = 9"},
		{"unknown role provider", func(s string) string { return strings.Replace(s, `["a"]`, `["a", "zz"]`, 1) }, "unknown provider \"zz\""},
		{"secret in api_key_env", func(s string) string {
			return strings.Replace(s, "speed_tier = 4", "speed_tier = 4\napi_key_env = \"sk-live-1234\"", 1)
		}, "api_key_env must name an environment variable"},
		{"poll too fast", func(s string) string {
			return strings.Replace(s, "[[providers.a.windows]]", "[providers.a.sensor]\npoll_interval_sec = 10\n[[providers.a.windows]]", 1)
		}, "poll_interval_sec = 10"},
		{"rolling without duration", func(s string) string { return strings.Replace(s, "duration_hours = 5", "", 1) }, "duration_hours > 0"},
		{"unknown profile", func(s string) string { return s + "\n[general]\nactive_profile = \"yolo\"\n" }, "unknown profile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeFile(t, tt.edit(minimal)))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestProfileWeightsNormalized(t *testing.T) {
	c := &Config{Profiles: map[string]Weights{CustomProfile: {Quality: 2, Expiry: 2, Scarcity: 0, Latency: 0}}}
	w, err := c.ProfileWeights(CustomProfile)
	if err != nil {
		t.Fatal(err)
	}
	if w.Quality != 0.5 || w.Expiry != 0.5 {
		t.Errorf("weights = %+v", w)
	}
}

func TestRoleFallsBackToDefault(t *testing.T) {
	c := &Config{Roles: map[string]Role{"default": {AllowedProviders: []string{"a"}}}}
	_, name, err := c.Role("gameplay")
	if err != nil || name != "default" {
		t.Fatalf("Role = %q, %v", name, err)
	}
	c.Roles = nil
	if _, _, err := c.Role("gameplay"); err == nil {
		t.Fatal("want error without default role")
	}
}

func TestResolvePaths(t *testing.T) {
	env := map[string]string{"HOME": "/h"}
	get := func(k string) string { return env[k] }
	p := ResolvePaths(get)
	if p.Config != "/h/.config/hiraishin/preferences.toml" || p.Ledger() != "/h/.local/share/hiraishin/ledger.db" {
		t.Errorf("paths = %+v", p)
	}
	env["XDG_CONFIG_HOME"], env["HIRAISHIN_DATA_DIR"] = "/x", "/d"
	p = ResolvePaths(get)
	if p.Config != "/x/hiraishin/preferences.toml" || p.DataDir != "/d" {
		t.Errorf("paths = %+v", p)
	}
}
