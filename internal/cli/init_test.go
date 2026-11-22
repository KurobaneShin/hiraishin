package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

const fakeSecret = "zai-test-secret-value-0000"

func testEnv(t *testing.T, bins ...string) (Env, *bytes.Buffer, *bytes.Buffer, map[string]string) {
	t.Helper()
	home := t.TempDir()
	vars := map[string]string{"HOME": home, "ZAI_API_KEY": fakeSecret}
	opencode := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(opencode), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"agent":{"kimi-worker":{"model":"kimi-code-plan-cn/k3.1-pro"},"glm-worker":{"model":"zai-coding-plan/glm-5.3-flash"},"explore":{"model":"kimi-code-plan-cn/x"},"build":{}}}`
	if err := os.WriteFile(opencode, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	env := Env{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut,
		Getenv: func(k string) string { return vars[k] },
		LookPath: func(name string) (string, error) {
			if slices.Contains(bins, name) {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		ReadFile: os.ReadFile,
		Now:      func() time.Time { return time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC) },
	}
	return env, &out, &errOut, vars
}

func TestInitWritesValidConfigWithoutSecrets(t *testing.T) {
	env, out, errOut, vars := testEnv(t, "claude", "kimi", "opencode", "agy")
	if code := Run(context.Background(), []string{"init", "--yes"}, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	path := config.ResolvePaths(env.Getenv).Config
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("generated config invalid: %v", err)
	}
	want := []string{"agy", "claude", "glm-claude", "kimi-native", "opencode-glm-worker", "opencode-kimi-worker"}
	if got := strings.Join(cfg.ProviderIDs(), ","); got != strings.Join(want, ",") {
		t.Errorf("providers = %s, want %s", got, strings.Join(want, ","))
	}
	if cfg.Providers["glm-claude"].APIKeyEnv != "ZAI_API_KEY" {
		t.Errorf("glm-claude api_key_env = %q", cfg.Providers["glm-claude"].APIKeyEnv)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"file": string(raw), "stdout": out.String(), "stderr": errOut.String()} {
		if strings.Contains(text, vars["ZAI_API_KEY"]) {
			t.Errorf("secret leaked into %s", name)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("config perms = %v, %v", info.Mode().Perm(), err)
	}
}

func TestInitRefusesOverwriteWithoutForce(t *testing.T) {
	env, _, errOut, _ := testEnv(t, "claude")
	if code := Run(context.Background(), []string{"init", "--yes"}, env); code != 0 {
		t.Fatalf("first init: %s", errOut.String())
	}
	if code := Run(context.Background(), []string{"init", "--yes"}, env); code == 0 || !strings.Contains(errOut.String(), "exists") {
		t.Fatalf("second init code=%d err=%s", code, errOut.String())
	}
	if code := Run(context.Background(), []string{"init", "--yes", "--force"}, env); code != 0 {
		t.Fatalf("forced init: %s", errOut.String())
	}
}

func TestInitInteractiveSkipsAndEditsProviders(t *testing.T) {
	env, _, errOut, _ := testEnv(t, "claude", "agy")
	env.Interactive = true
	env.Stdin = strings.NewReader("n\ny\n5\n\n3\nn\n")
	if code := Run(context.Background(), []string{"init"}, env); code != 0 {
		t.Fatalf("exit: %s", errOut.String())
	}
	cfg, err := config.Load(config.ResolvePaths(env.Getenv).Config)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ProviderIDs(); len(got) != 1 || got[0] != "glm-claude" {
		t.Fatalf("providers = %v", got)
	}
	p := cfg.Providers["glm-claude"]
	if p.QualityTier != 5 || p.MaxConcurrent != 3 {
		t.Errorf("edited provider = %+v", p)
	}
}

func TestInitNoCLIs(t *testing.T) {
	env, _, errOut, _ := testEnv(t)
	if code := Run(context.Background(), []string{"init", "--yes"}, env); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if _, err := os.Stat(config.ResolvePaths(env.Getenv).Config); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("config written without providers")
	}
	if !strings.Contains(errOut.String(), "none of claude") {
		t.Errorf("stderr = %s", errOut.String())
	}
}

func TestInitDetectsCodexAndGeminiFakeBinariesOnPATH(t *testing.T) {
	binDir := t.TempDir()
	for _, name := range []string{"codex", "gemini"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)
	home := t.TempDir()
	vars := map[string]string{"HOME": home}
	var out, errOut bytes.Buffer
	env := Env{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut,
		Getenv:   func(k string) string { return vars[k] },
		LookPath: exec.LookPath,
		ReadFile: os.ReadFile,
		Now:      func() time.Time { return time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC) },
	}
	if code := Run(context.Background(), []string{"init", "--yes"}, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	cfg, err := config.Load(config.ResolvePaths(env.Getenv).Config)
	if err != nil {
		t.Fatalf("generated config invalid: %v", err)
	}
	want := []string{"codex", "gemini"}
	if got := strings.Join(cfg.ProviderIDs(), ","); got != strings.Join(want, ",") {
		t.Fatalf("providers = %s, want %s", got, strings.Join(want, ","))
	}
	if cfg.Providers["codex"].Harness != config.HarnessCodex {
		t.Errorf("codex harness = %q", cfg.Providers["codex"].Harness)
	}
	if cfg.Providers["gemini"].Harness != config.HarnessGeminiCLI {
		t.Errorf("gemini harness = %q", cfg.Providers["gemini"].Harness)
	}
}
