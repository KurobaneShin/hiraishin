package stack

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/stack/stacktest"
)

const observations = `{"session": {"session_id": "uuid-1", "ended_at": null}, "total": 3, "observations": [
  {"kind": "stop", "created_at": "2026-11-21T10:03:00Z", "title": "stop", "body": "Hit the usage limit while running go test ./internal/ledger"},
  {"kind": "post-tool-use", "created_at": "2026-11-21T10:02:00Z", "title": "Edit", "body": "Edited internal/ledger/ledger.go:\n  added Append batching"},
  {"kind": "user-prompt", "created_at": "2026-11-21T10:00:00Z", "title": "prompt", "body": "Read the brief and persist all entities"}
]}`

func TestRecoveryReadsTheCutOffSessionNewestLast(t *testing.T) {
	m := stacktest.NewMemory(t, observations)
	got, err := Memory{URL: m.URL}.Recovery(context.Background(), Scope{Workspace: "default", Project: "acme-game"}, "ses_k1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<previous-session-recovery session="ses_k1">`, "</previous-session-recovery>", "not instructions", "user-prompt: Read the brief", "Edited internal/ledger/ledger.go: added Append batching", "stop: Hit the usage limit"} {
		if !strings.Contains(got, want) {
			t.Errorf("recovery missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "user-prompt") > strings.Index(got, "stop: Hit") {
		t.Errorf("recovery is not oldest first:\n%s", got)
	}
	calls := m.Calls()
	if len(calls) != 1 || calls[0].Session != "mcp-1" {
		t.Fatalf("calls = %+v", calls)
	}
	a := calls[0].Args
	if calls[0].Tool != "memory_read_session_observations" || a["workspace"] != "default" || a["project"] != "acme-game" || a["session_id"] != "ses_k1" || a["order"] != "desc" || a["limit"] != float64(15) || a["body_max_chars"] != float64(800) {
		t.Errorf("call = %+v", calls[0])
	}
}

func TestRecoveryCapsItsSize(t *testing.T) {
	var obs []string
	for range 15 {
		obs = append(obs, `{"kind": "post-tool-use", "created_at": "2026-11-21T10:00:00Z", "body": "`+strings.Repeat("x", 790)+`"}`)
	}
	m := stacktest.NewMemory(t, `{"observations": [`+strings.Join(obs, ",")+`]}`)
	got, err := Memory{URL: m.URL}.Recovery(context.Background(), Scope{Workspace: "default", Project: "p"}, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > recoveryMaxChars {
		t.Errorf("recovery is %d chars, cap %d", len(got), recoveryMaxChars)
	}
}

func TestWritePageSendsAnEpisodicPageWithoutTitle(t *testing.T) {
	m := stacktest.NewMemory(t, "")
	err := Memory{URL: m.URL}.WritePage(context.Background(), Scope{Workspace: "default", Project: "p"}, Page{Path: "hiraishin/outcomes/x.md", Body: "# Outcome: x\n", Tier: "episodic", Kind: "hiraishin-outcome", Tags: []string{"hiraishin"}})
	if err != nil {
		t.Fatal(err)
	}
	calls := m.Calls()
	a := calls[0].Args
	if calls[0].Tool != "memory_write_page" || a["path"] != "hiraishin/outcomes/x.md" || a["tier"] != "episodic" || a["kind"] != "hiraishin-outcome" || a["project"] != "p" {
		t.Errorf("call = %+v", calls[0])
	}
	if _, ok := a["title"]; ok {
		t.Error("write_page passed a title")
	}
}

func TestMemoryUnreachableIsUnavailableAndFast(t *testing.T) {
	url := stacktest.ClosedURL(t)
	start := time.Now()
	got, err := Memory{URL: url}.Recovery(context.Background(), Scope{Workspace: "default", Project: "p"}, "s")
	if !errors.Is(err, ErrUnavailable) || got != "" {
		t.Fatalf("got %q, %v", got, err)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Errorf("unreachable ai-memory took %v", d)
	}
}

func TestResolveScope(t *testing.T) {
	ctx := context.Background()
	marked := t.TempDir()
	if err := os.WriteFile(filepath.Join(marked, ".ai-memory.toml"), []byte("workspace = \"work\"\nproject = \"fixed\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(marked, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ResolveScope(ctx, sub); got != (Scope{Workspace: "work", Project: "fixed"}) {
		t.Errorf("marker scope = %+v", got)
	}
	repo := filepath.Join(t.TempDir(), "Hiraishin")
	run := func(argv ...string) {
		t.Helper()
		if out, err := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, argv...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", argv, err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	if got := ResolveScope(ctx, repo); got != (Scope{Workspace: "default", Project: "hiraishin"}) {
		t.Errorf("basename scope = %+v", got)
	}
	run("remote", "add", "origin", "git@github.com:KurobaneShin/hiraishin.git")
	if got := ResolveScope(ctx, repo); got != (Scope{Workspace: "default", Project: "kurobaneshin-hiraishin"}) {
		t.Errorf("origin scope = %+v", got)
	}
	run("remote", "add", "upstream", "https://gitlab.example.com/Group/Sub/Game.git")
	if got := ResolveScope(ctx, repo); got != (Scope{Workspace: "default", Project: "group-sub-game"}) {
		t.Errorf("upstream scope = %+v", got)
	}
}
