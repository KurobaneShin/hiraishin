package inventory

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func touch(t *testing.T, parts ...string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(parts...), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func system(env map[string]string) System {
	return System{
		LookPath: func(name string) (string, error) {
			if name == "claude" {
				return "/bin/claude", nil
			}
			return "", errors.New("not found")
		},
		ReadFile: os.ReadFile,
		Getenv:   func(k string) string { return env[k] },
		Stat:     os.Stat,
		Glob:     filepath.Glob,
	}
}

func TestClaudeAccounts(t *testing.T) {
	home := t.TempDir()
	mkdir(t, home, ".claude", "projects")
	profiles := mkdir(t, home, ".local", "share", "tool", "claude-profiles")
	a := mkdir(t, profiles, "4385d02a-4109", "home")
	touch(t, a, ".credentials.json")
	b := mkdir(t, profiles, "69716dc7-9802", "home")
	touch(t, b, ".claude.json")
	mkdir(t, profiles, "empty", "home")
	outside := mkdir(t, t.TempDir(), "work-claude")
	mkdir(t, outside, "projects")

	tests := []struct {
		name     string
		env      map[string]string
		patterns []string
		want     map[string]string
	}{
		{name: "default only", env: map[string]string{"HOME": home}, want: map[string]string{"claude": ""}},
		{name: "env profile and glob, deduplicated", env: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": b},
			patterns: []string{"~/.local/share/tool/claude-profiles/*/home"},
			want:     map[string]string{"claude": "", "claude-69716dc7": "~/.local/share/tool/claude-profiles/69716dc7-9802/home", "claude-4385d02a": "~/.local/share/tool/claude-profiles/4385d02a-4109/home"}},
		{name: "absolute path outside home", env: map[string]string{"HOME": home}, patterns: []string{outside},
			want: map[string]string{"claude": "", "claude-work-cla": outside}},
		{name: "no config dir yet", env: map[string]string{"HOME": t.TempDir()}, want: map[string]string{"claude": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, err := Detect(system(tt.env), tt.patterns)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			pools := map[string]bool{}
			for _, f := range found {
				got[f.ID] = f.Provider.ConfigDir
				if pools[f.Provider.SharedQuotaPool] {
					t.Errorf("accounts share pool %q", f.Provider.SharedQuotaPool)
				}
				pools[f.Provider.SharedQuotaPool] = true
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for id, dir := range tt.want {
				if g, ok := got[id]; !ok || g != dir {
					t.Errorf("%s: got %q (present=%v), want %q; all %v", id, g, ok, dir, got)
				}
			}
			cfg := Build(found, "process")
			if err := cfg.Validate(); err != nil {
				t.Errorf("generated config invalid: %v", err)
			}
		})
	}
}

func TestSameAccountInTwoDirsIsDetectedOnce(t *testing.T) {
	home := t.TempDir()
	mkdir(t, home, ".claude", "projects")
	write := func(path, uuid string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(`{"oauthAccount":{"accountUuid":"`+uuid+`","emailAddress":"x@example.invalid"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".claude.json"), "acct-1")
	same := mkdir(t, home, "profiles", "same0001", "home")
	write(filepath.Join(same, ".claude.json"), "acct-1")
	other := mkdir(t, home, "profiles", "other002", "home")
	write(filepath.Join(other, ".claude.json"), "acct-2")
	found, err := Detect(system(map[string]string{"HOME": home}), []string{"~/profiles/*/home"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range found {
		ids = append(ids, f.ID)
	}
	if len(ids) != 2 || ids[0] != "claude" || ids[1] != "claude-other002" {
		t.Fatalf("ids = %v, want [claude claude-other002]", ids)
	}
}

func TestClaudeLabelCollision(t *testing.T) {
	home := t.TempDir()
	x := mkdir(t, home, "a", "same", "home")
	y := mkdir(t, home, "b", "same", "home")
	touch(t, x, ".claude.json")
	touch(t, y, ".claude.json")
	found, err := Detect(system(map[string]string{"HOME": home}), []string{x, y})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range found {
		ids = append(ids, f.ID)
	}
	if len(ids) != 2 || ids[0] != "claude-same" || ids[1] != "claude-same-2" {
		t.Fatalf("ids = %v", ids)
	}
}
