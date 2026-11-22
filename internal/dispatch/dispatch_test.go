package dispatch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
)

func TestBuild(t *testing.T) {
	tests := []struct {
		name    string
		p       config.Provider
		resume  string
		want    []string
		wantEnv string
		wantRef string
		wantErr bool
	}{
		{name: "claude", p: config.Provider{Harness: config.HarnessClaudeCode, Model: "m", Effort: "high"},
			want: []string{"claude", "-p", promptArg, "--output-format", "stream-json", "--verbose", "--permission-mode", "auto", "--add-dir", "/wt", "--model", "m", "--effort", "high"}},
		{name: "glm-claude", p: config.Provider{Harness: config.HarnessGLMClaude, APIKeyEnv: "ZAI_API_KEY"},
			want:    []string{"claude", "-p", promptArg, "--output-format", "stream-json", "--verbose", "--permission-mode", "auto", "--add-dir", "/wt"},
			wantEnv: "ANTHROPIC_BASE_URL=" + GLMBaseURL, wantRef: "ANTHROPIC_AUTH_TOKEN=ZAI_API_KEY"},
		{name: "kimi", p: config.Provider{Harness: config.HarnessKimiNative},
			want: []string{"kimi", "--prompt", promptArg, "--output-format", "stream-json"}},
		{name: "opencode", p: config.Provider{Harness: config.HarnessOpenCode, HarnessAgent: "kimi-worker", Model: "p/m"},
			want: []string{"opencode", "run", "--format", "json", "--auto", "--dir", "/wt", "--agent", "kimi-worker", "--model", "p/m", promptArg}},
		{name: "agy with prefix", p: config.Provider{Harness: config.HarnessAgy, Model: "g", LaunchPrefix: []string{"flock", "~/.agy.lock"}},
			want: []string{"flock", "~/.agy.lock", "agy", "--mode", "accept-edits", "--dangerously-skip-permissions", "--add-dir", "/wt", "--output-format", "stream-json", "--model", "g", "--print", promptArg}},
		{name: "claude resume", p: config.Provider{Harness: config.HarnessClaudeCode}, resume: "d0b2f679",
			want: []string{"claude", "-p", promptArg, "--output-format", "stream-json", "--verbose", "--permission-mode", "auto", "--add-dir", "/wt", "--resume", "d0b2f679"}},
		{name: "kimi resume", p: config.Provider{Harness: config.HarnessKimiNative}, resume: "session_6149",
			want: []string{"kimi", "--prompt", promptArg, "--output-format", "stream-json", "--session", "session_6149"}},
		{name: "opencode resume", p: config.Provider{Harness: config.HarnessOpenCode}, resume: "ses_dfef",
			want: []string{"opencode", "run", "--format", "json", "--auto", "--dir", "/wt", "--session", "ses_dfef", promptArg}},
		{name: "agy resume", p: config.Provider{Harness: config.HarnessAgy}, resume: "67c2df35",
			want: []string{"agy", "--mode", "accept-edits", "--dangerously-skip-permissions", "--add-dir", "/wt", "--output-format", "stream-json", "--conversation", "67c2df35", "--print", promptArg}},
		{name: "codex is v0.2", p: config.Provider{Harness: config.HarnessCodex}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Build(tt.p, "/wt", tt.resume)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if !slices.Equal(c.Argv, tt.want) {
				t.Errorf("argv = %q\nwant   %q", c.Argv, tt.want)
			}
			join := func(kv [][2]string) string {
				var s []string
				for _, p := range kv {
					s = append(s, p[0]+"="+p[1])
				}
				return strings.Join(s, ",")
			}
			if join(c.Env) != tt.wantEnv || join(c.EnvRef) != tt.wantRef {
				t.Errorf("env = %q ref = %q", join(c.Env), join(c.EnvRef))
			}
		})
	}
}

func fakeBin(t *testing.T, name, body string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/usr/bin/env bash\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func gitRepo(t *testing.T) string {
	t.Helper()
	wt := t.TempDir()
	for _, argv := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.CommandContext(context.Background(), "git", append([]string{"-C", wt}, argv...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", argv, err, out)
		}
	}
	return wt
}

func prepare(t *testing.T, p config.Provider, wt, prompt string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	c, err := Build(p, wt, "")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, ScriptFile)
	if err := os.WriteFile(filepath.Join(dir, PromptFile), []byte(prompt), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(Script(c, wt, dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, script
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestProcessAdapterRunsScript(t *testing.T) {
	fakeBin(t, "claude", `printf 'token=%s\n' "$ANTHROPIC_AUTH_TOKEN"; printf 'base=%s\n' "$ANTHROPIC_BASE_URL"; printf 'prompt=%s\n' "$2"; echo edit > file.txt; exit 3`)
	t.Setenv("ZAI_API_KEY", "sk-test-secret")
	wt := gitRepo(t)
	dir, script := prepare(t, config.Provider{Harness: config.HarnessGLMClaude, APIKeyEnv: "ZAI_API_KEY"}, wt, "it's \"quoted\" $HOME")
	if b, _ := os.ReadFile(script); strings.Contains(string(b), "sk-test-secret") {
		t.Fatal("script contains the secret value")
	}
	a, _ := NewAdapter(AdapterProcess)
	h, err := a.Spawn(context.Background(), Launch{Title: "t", Worktree: wt, Script: script})
	if err != nil || !strings.HasPrefix(h, "pid:") {
		t.Fatalf("spawn = %q %v", h, err)
	}
	var code int
	waitFor(t, "exit file", func() bool {
		var done bool
		code, done, _ = ExitCode(dir)
		return done
	})
	if code != 3 {
		t.Errorf("exit = %d", code)
	}
	log, _ := os.ReadFile(filepath.Join(dir, OutputFile))
	for _, want := range []string{"token=sk-test-secret", "base=" + GLMBaseURL, "prompt=it's \"quoted\" $HOME"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
	if _, err := os.Stat(filepath.Join(wt, "file.txt")); err != nil {
		t.Errorf("agent did not run in the worktree: %v", err)
	}
}

func TestClaudeConfigDirPerAccount(t *testing.T) {
	fakeBin(t, "claude", `printf '%s' "${CLAUDE_CONFIG_DIR-unset}" > .git/config-dir.txt`)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited/profile")
	for _, tt := range []struct {
		dir, want string
	}{{"~/profiles/b/home", filepath.Join(home, "profiles/b/home")}, {"/abs/acc", "/abs/acc"}, {"", "unset"}} {
		wt := gitRepo(t)
		dir, script := prepare(t, config.Provider{Harness: config.HarnessClaudeCode, ConfigDir: tt.dir}, wt, "p")
		a, _ := NewAdapter(AdapterProcess)
		if _, err := a.Spawn(context.Background(), Launch{Worktree: wt, Script: script}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "exit file", func() bool { _, done, _ := ExitCode(dir); return done })
		if got, _ := os.ReadFile(filepath.Join(wt, ".git", "config-dir.txt")); string(got) != tt.want {
			t.Errorf("config_dir %q: agent saw CLAUDE_CONFIG_DIR=%q, want %q", tt.dir, got, tt.want)
		}
	}
}

func TestStopKillsProcessGroup(t *testing.T) {
	fakeBin(t, "kimi", `trap 'echo got-int; exit 130' INT; sleep 30 & wait`)
	wt := gitRepo(t)
	dir, script := prepare(t, config.Provider{Harness: config.HarnessKimiNative}, wt, "p")
	a, _ := NewAdapter(AdapterProcess)
	if _, err := a.Spawn(context.Background(), Launch{Worktree: wt, Script: script}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "pgid", func() bool { return Alive(dir) })
	start := time.Now()
	if err := Stop(context.Background(), dir, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if Alive(dir) {
		t.Fatal("process group still alive")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("stop took %v", time.Since(start))
	}
	if err := Stop(context.Background(), t.TempDir(), time.Second); !errors.Is(err, ErrNoProcess) {
		t.Errorf("stop without pgid = %v", err)
	}
}

func TestTmuxAdapter(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("TMUX", "")
	t.Cleanup(func() { _ = exec.CommandContext(context.Background(), "tmux", "kill-server").Run() })
	fakeBin(t, "kimi", "echo from-tmux")
	wt := gitRepo(t)
	dir, script := prepare(t, config.Provider{Harness: config.HarnessKimiNative}, wt, "p")
	a, _ := NewAdapter(AdapterTmux)
	h, err := a.Spawn(context.Background(), Launch{Title: "job@kimi", Worktree: wt, Script: script})
	if err != nil || !strings.HasPrefix(h, "tmux:%") {
		t.Fatalf("spawn = %q %v", h, err)
	}
	waitFor(t, "exit file", func() bool { _, done, _ := ExitCode(dir); return done })
	if log, _ := os.ReadFile(filepath.Join(dir, OutputFile)); !strings.Contains(string(log), "from-tmux") {
		t.Errorf("log = %q", log)
	}
}

func TestOrcaAdapter(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	fakeBin(t, "orca-ide", `printf '%s\n' "$@" > `+quote(argsFile)+`; echo '{"ok":true,"result":{"terminal":{"handle":"term_7"}}}'`)
	a, _ := NewAdapter(AdapterOrca)
	h, err := a.Spawn(context.Background(), Launch{Title: "job@agy", Worktree: "/wt", Script: "/d/run.sh"})
	if err != nil || h != "orca:term_7" {
		t.Fatalf("spawn = %q %v", h, err)
	}
	got, _ := os.ReadFile(argsFile)
	want := "terminal\ncreate\n--worktree\npath:/wt\n--title\njob@agy\n--command\nbash '/d/run.sh'\n--json\n"
	if string(got) != want {
		t.Errorf("args = %q", got)
	}
	if _, err := NewAdapter("screen"); err == nil {
		t.Error("unknown adapter accepted")
	}
}

func TestCheckpoint(t *testing.T) {
	wt := gitRepo(t)
	if _, err := ReadCheckpoint(context.Background(), t.TempDir()); err == nil {
		t.Error("non-git dir accepted")
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(wt, "a.go"), []byte("package a\n"), 0o644))
	must(exec.CommandContext(context.Background(), "git", "-C", wt, "add", "a.go").Run())
	must(exec.CommandContext(context.Background(), "git", "-C", wt, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "a").Run())
	must(os.WriteFile(filepath.Join(wt, "a.go"), []byte("package a\n\nvar X = 1\n"), 0o644))
	must(os.MkdirAll(filepath.Join(wt, "new"), 0o755))
	must(os.WriteFile(filepath.Join(wt, "new", "b.go"), []byte("package b\n"), 0o644))
	cp, err := ReadCheckpoint(context.Background(), wt)
	must(err)
	if cp.Branch != "main" || len(cp.Head) != 40 {
		t.Errorf("branch/head = %q %q", cp.Branch, cp.Head)
	}
	if !slices.Equal(cp.Dirty, []string{"a.go", "new/b.go"}) {
		t.Errorf("dirty = %q", cp.Dirty)
	}
	if !strings.Contains(cp.DiffStat, "a.go") {
		t.Errorf("diffstat = %q", cp.DiffStat)
	}
}

func TestResumePrompt(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report.md")
	if err := os.WriteFile(report, []byte("- [x] step 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := PromptData{JobID: "job_1", JobName: "persist-all", Worktree: "/wt", BriefPath: "/b.md", ReportPath: report, FromProvider: "kimi", SessionID: "ses_k1", Reason: "429 You've reached your 5-hour usage limit",
		Checkpoint: Checkpoint{Head: "abc", Status: " M a.go\n?? new/b.go", DiffStat: " a.go | 2 +-"}}
	got, err := ResumePrompt(d, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"resuming job persist-all (job_1)", "previous agent (kimi) stopped: 429 You've reached your 5-hour usage limit", " M a.go\n?? new/b.go", " a.go | 2 +-", "- [x] step 1", "Do not reset", "session id was ses_k1", "memory_read_session_observations (session_id ses_k1, order desc)"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
	tmpl := filepath.Join(dir, "t.tmpl")
	if err := os.WriteFile(tmpl, []byte("{{.JobID}} from {{.FromProvider}}: {{len .Checkpoint.Status}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ResumePrompt(d, tmpl); err != nil || got != "job_1 from kimi: 19" {
		t.Errorf("custom = %q %v", got, err)
	}
	cont, err := ContinuePrompt(d)
	if err != nil || !strings.Contains(cont, "quota has reset") || !strings.Contains(cont, "Continue the brief at /b.md") {
		t.Errorf("continue = %q %v", cont, err)
	}
	d.SessionID = ""
	if got, _ := ResumePrompt(d, ""); strings.Contains(got, "ai-memory") {
		t.Errorf("prompt without a session mentions ai-memory:\n%s", got)
	}
	initial, err := InitialPrompt(d)
	if err != nil || !strings.Contains(initial, "Read the brief at /b.md") {
		t.Errorf("initial = %q %v", initial, err)
	}
}
