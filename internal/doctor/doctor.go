package doctor

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/dispatch"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

type Deps struct {
	Now          func() time.Time
	LookPath     func(string) (string, error)
	ReadFile     func(string) ([]byte, error)
	Stat         func(string) (fs.FileInfo, error)
	Getenv       func(string) string
	HTTPGet      func(ctx context.Context, url string) error
	Exec         func(ctx context.Context, name string, args ...string) error
	WatchRunning func() bool
}

func DefaultDeps() Deps {
	return Deps{
		Now:      time.Now,
		LookPath: exec.LookPath,
		ReadFile: os.ReadFile,
		Stat:     os.Stat,
		Getenv:   os.Getenv,
		HTTPGet: func(ctx context.Context, url string) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			_ = resp.Body.Close()
			if resp.StatusCode >= 400 && resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusNotAcceptable {
				return fmt.Errorf("HTTP %d", resp.StatusCode)
			}
			return nil
		},
		Exec: func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		},
	}
}

type Finding struct {
	Category string `json:"category"`
	Provider string `json:"provider,omitempty"`
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail,omitempty"`
}

type Report struct {
	Healthy  bool      `json:"healthy"`
	Findings []Finding `json:"findings"`
}

func Check(ctx context.Context, cfg *config.Config, r *ledger.Tx, deps Deps) (Report, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	rows, err := r.Providers(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("load providers: %w", err)
	}
	state := map[string]ledger.ProviderRow{}
	for _, p := range rows {
		state[p.ID] = p
	}
	windows, err := r.Windows(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("load windows: %w", err)
	}
	sampled := map[string]time.Time{}
	for _, w := range windows {
		if w.SampledAt.After(sampled[w.ProviderID]) {
			sampled[w.ProviderID] = w.SampledAt
		}
	}
	jobs, err := r.Jobs(ctx, ledger.JobRunningState, ledger.JobDispatchingState, ledger.JobFailingOverState)
	if err != nil {
		return Report{}, fmt.Errorf("load jobs: %w", err)
	}
	checks := []Finding{}
	checks = append(checks, binaryFindings(cfg, deps.LookPath)...)
	checks = append(checks, authFindings(cfg, state, deps)...)
	checks = append(checks, sensorFindings(cfg, sampled, deps.Now)...)
	if deps.WatchRunning != nil {
		checks = append(checks, watchFinding(deps.WatchRunning()))
	}
	checks = append(checks, worktreeFindings(ctx, jobs)...)
	checks = append(checks, integrationFindings(ctx, cfg, jobs, deps)...)
	healthy := true
	for _, c := range checks {
		if !c.OK {
			healthy = false
			break
		}
	}
	return Report{Healthy: healthy, Findings: checks}, nil
}

func binaryFindings(cfg *config.Config, lookPath func(string) (string, error)) []Finding {
	var out []Finding
	for _, id := range cfg.ProviderIDs() {
		p := cfg.Providers[id]
		if !p.IsEnabled() {
			continue
		}
		bin := binaryForHarness(p.Harness)
		_, err := lookPath(bin)
		c := Finding{Category: "binary", Provider: id, Name: bin}
		if err != nil {
			c.Detail = "not found in PATH"
		} else {
			c.OK = true
			c.Detail = "found"
		}
		out = append(out, c)
	}
	return out
}

func binaryForHarness(harness string) string {
	switch harness {
	case config.HarnessClaudeCode, config.HarnessGLMClaude:
		return "claude"
	case config.HarnessKimiNative:
		return "kimi"
	case config.HarnessOpenCode:
		return "opencode"
	case config.HarnessAgy:
		return "agy"
	case config.HarnessCodex:
		return "codex"
	case config.HarnessGeminiCLI:
		return "gemini"
	}
	return harness
}

func authFindings(cfg *config.Config, state map[string]ledger.ProviderRow, deps Deps) []Finding {
	var out []Finding
	for _, id := range cfg.ProviderIDs() {
		p := cfg.Providers[id]
		if !p.IsEnabled() {
			continue
		}
		row, hasRow := state[id]
		if hasRow && row.State == ledger.StateAuthError {
			out = append(out, Finding{Category: "auth", Provider: id, Name: "provider state", Detail: row.LastError})
		}
		if p.Harness == config.HarnessClaudeCode || p.Harness == config.HarnessGLMClaude {
			out = append(out, claudeTokenFinding(id, p, deps))
		}
		if p.Harness == config.HarnessGLMClaude && p.APIKeyEnv != "" {
			out = append(out, envFinding(id, p.APIKeyEnv, deps.Getenv))
		}
		if p.APIKeyEnv != "" && p.Harness != config.HarnessGLMClaude {
			out = append(out, envFinding(id, p.APIKeyEnv, deps.Getenv))
		}
	}
	return out
}

func claudeTokenFinding(id string, p config.Provider, deps Deps) Finding {
	dir := claudeDir(p, deps.Getenv)
	valid, expiresAt, refreshable, err := readClaudeToken(dir, deps.Now(), deps.ReadFile)
	c := Finding{Category: "auth", Provider: id, Name: "claude token"}
	switch {
	case err != nil:
		c.Detail = err.Error()
	case refreshable && (!valid || (!expiresAt.IsZero() && deps.Now().After(expiresAt.Add(-time.Hour)))):
		c.OK = true
		c.Detail = "access token expired or expiring; Claude Code refreshes it on next use"
	case !valid:
		c.Detail = "missing or expired"
	case !expiresAt.IsZero() && deps.Now().After(expiresAt.Add(-time.Hour)):
		c.Detail = fmt.Sprintf("expires at %s", expiresAt.UTC().Format(time.RFC3339))
	default:
		c.OK = true
		if expiresAt.IsZero() {
			c.Detail = "valid, no expiry recorded"
		} else {
			c.Detail = fmt.Sprintf("valid until %s", expiresAt.UTC().Format(time.RFC3339))
		}
	}
	return c
}

func claudeDir(p config.Provider, getenv func(string) string) string {
	home := getenv("HOME")
	if p.ConfigDir == "" {
		return filepath.Join(home, ".claude")
	}
	return config.ExpandHome(p.ConfigDir, home)
}

func readClaudeToken(dir string, now time.Time, readFile func(string) ([]byte, error)) (bool, time.Time, bool, error) {
	b, err := readFile(filepath.Join(dir, ".credentials.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, time.Time{}, false, nil
	}
	if err != nil {
		return false, time.Time{}, false, err
	}
	var doc struct {
		OAuth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return false, time.Time{}, false, fmt.Errorf("parse credentials: %w", err)
	}
	refreshable := doc.OAuth.RefreshToken != ""
	if doc.OAuth.AccessToken == "" {
		return false, time.Time{}, refreshable, nil
	}
	if doc.OAuth.ExpiresAt > 0 && !now.Before(time.UnixMilli(doc.OAuth.ExpiresAt)) {
		return false, time.Time{}, refreshable, nil
	}
	var at time.Time
	if doc.OAuth.ExpiresAt > 0 {
		at = time.UnixMilli(doc.OAuth.ExpiresAt)
	}
	return true, at, refreshable, nil
}

func envFinding(id, name string, getenv func(string) string) Finding {
	c := Finding{Category: "auth", Provider: id, Name: name}
	if getenv(name) == "" {
		c.Detail = "not set"
	} else {
		c.OK = true
		c.Detail = "set"
	}
	return c
}

func sensorFindings(cfg *config.Config, sampled map[string]time.Time, now func() time.Time) []Finding {
	var out []Finding
	for _, id := range cfg.ProviderIDs() {
		p := cfg.Providers[id]
		if !p.IsEnabled() {
			continue
		}
		interval := time.Duration(p.Sensor.PollIntervalSec) * time.Second
		at, ok := sampled[id]
		c := Finding{Category: "sensor", Provider: id, Name: "freshness"}
		switch {
		case !ok:
			c.Detail = "no sample recorded"
		case now().Sub(at) > 2*interval:
			c.Detail = fmt.Sprintf("stale: last sample %s ago", now().Sub(at).Round(time.Second))
		default:
			c.OK = true
			c.Detail = fmt.Sprintf("last sample %s ago", now().Sub(at).Round(time.Second))
		}
		out = append(out, c)
	}
	return out
}

func watchFinding(running bool) Finding {
	if running {
		return Finding{Category: "watch", Name: "running", OK: true, Detail: "hiraishin watch holds the watch lock"}
	}
	return Finding{Category: "watch", Name: "running", Detail: "not running: no usage sampling and no failover; run it as a service: hiraishin watch --install"}
}

func worktreeFindings(ctx context.Context, jobs []ledger.JobRow) []Finding {
	var out []Finding
	for _, j := range jobs {
		c := Finding{Category: "worktree", Provider: j.CurrentProvider, Name: j.ID}
		cp, err := dispatch.ReadCheckpoint(ctx, j.WorktreePath)
		if err != nil {
			c.Detail = err.Error()
			out = append(out, c)
			continue
		}
		if j.Branch != "" && cp.Branch != j.Branch {
			c.Detail = fmt.Sprintf("branch %s, want %s", cp.Branch, j.Branch)
			out = append(out, c)
			continue
		}
		c.OK = true
		c.Detail = fmt.Sprintf("HEAD %s branch %s dirty=%d", shortSHA(cp.Head), cp.Branch, len(cp.Dirty))
		out = append(out, c)
	}
	return out
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func integrationFindings(ctx context.Context, cfg *config.Config, jobs []ledger.JobRow, deps Deps) []Finding {
	var out []Finding
	if cfg.Integrations.AIMemory != nil && cfg.Integrations.AIMemory.Enabled {
		out = append(out, aiMemoryFinding(ctx, cfg.Integrations.AIMemory, deps))
	}
	if cfg.Integrations.QMD != nil && cfg.Integrations.QMD.Enabled {
		out = append(out, qmdFinding(ctx, deps))
	}
	if cfg.Integrations.Graphify != nil && cfg.Integrations.Graphify.Enabled {
		out = append(out, graphifyFinding(deps, jobs))
	}
	return out
}

func aiMemoryFinding(ctx context.Context, m *config.AIMemory, deps Deps) Finding {
	c := Finding{Category: "integration", Name: "ai-memory"}
	if m.Transport == "" || m.Transport == "http" {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := deps.HTTPGet(ctx, cmp.Or(m.URL, config.DefaultAIMemoryURL)); err != nil {
			c.Detail = err.Error()
			return c
		}
		c.OK = true
		c.Detail = "reachable"
		return c
	}
	if _, err := deps.LookPath("ai-memory"); err != nil {
		c.Detail = "ai-memory not on PATH"
		return c
	}
	c.OK = true
	c.Detail = "binary on PATH"
	return c
}

func qmdFinding(ctx context.Context, deps Deps) Finding {
	c := Finding{Category: "integration", Name: "qmd"}
	if _, err := deps.LookPath("qmd"); err != nil {
		c.Detail = "qmd not on PATH"
		return c
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := deps.Exec(ctx, "qmd", "status"); err != nil {
		c.Detail = err.Error()
		return c
	}
	c.OK = true
	c.Detail = "ok"
	return c
}

func graphifyFinding(deps Deps, jobs []ledger.JobRow) Finding {
	c := Finding{Category: "integration", Name: "graphify"}
	if _, err := deps.LookPath("graphify"); err == nil {
		c.OK = true
		c.Detail = "binary on PATH"
		return c
	}
	for _, j := range jobs {
		root := filepath.Join(j.WorktreePath, "graphify-out", "graph.json")
		if _, err := deps.Stat(root); err == nil {
			c.OK = true
			c.Detail = "graph.json present"
			return c
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if _, err := deps.Stat(filepath.Join(cwd, "graphify-out", "graph.json")); err == nil {
			c.OK = true
			c.Detail = "graph.json present"
			return c
		}
	}
	c.Detail = "graphify not on PATH and graphify-out/graph.json not found"
	return c
}

func RenderText(out *strings.Builder, r Report) {
	for _, c := range r.Findings {
		status := "FAIL"
		if c.OK {
			status = "ok"
		}
		line := fmt.Sprintf("[%s] %s", status, c.Category)
		if c.Provider != "" {
			line += ":" + c.Provider
		}
		line += "/" + c.Name
		if c.Detail != "" {
			line += " - " + c.Detail
		}
		out.WriteString(line + "\n")
	}
	if r.Healthy {
		out.WriteString("doctor: healthy\n")
	} else {
		out.WriteString("doctor: problems detected\n")
	}
}
