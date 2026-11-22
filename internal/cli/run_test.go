package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/dispatch"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

func agentBin(t *testing.T, name, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/usr/bin/env bash\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func worktree(t *testing.T) (string, string) {
	t.Helper()
	wt := t.TempDir()
	if out, err := exec.CommandContext(context.Background(), "git", "-C", wt, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	briefDir := filepath.Join(t.TempDir(), "persist-all")
	if err := os.MkdirAll(briefDir, 0o755); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(briefDir, "brief.md")
	if err := os.WriteFile(brief, []byte("do it\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return wt, brief
}

var jobIDRe = regexp.MustCompile(`job_[0-9A-Z]+`)

func (h *harness) job(id string) (ledger.JobRow, []ledger.AttemptRow) {
	h.t.Helper()
	e := h.engine()
	defer func() { _ = e.Close() }()
	r := e.Ledger.Reader()
	j, err := r.Job(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	a, err := r.Attempts(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return j, a
}

func waitExit(t *testing.T, dir string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if code, done, _ := dispatch.ExitCode(dir); done {
			return code
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no exit file in %s", dir)
	return 0
}

func TestRunDispatchesInWorktree(t *testing.T) {
	agentBin(t, "kimi", `echo "$2" > seen-prompt.txt`)
	h := newHarness(t)
	h.now = time.Now().UTC()
	wt, brief := worktree(t)
	out := h.run(0, "run", "--brief", brief, "--worktree", wt, "--adapter", "process")
	id := jobIDRe.FindString(out)
	if id == "" || !strings.Contains(out, "on kimi via process") {
		t.Fatalf("run = %q", out)
	}
	job, attempts := h.job(id)
	if job.State != ledger.JobRunningState || job.Name != "persist-all" || job.ReportPath != filepath.Join(filepath.Dir(brief), "report.md") || len(attempts) != 1 {
		t.Fatalf("job = %+v attempts = %d", job, len(attempts))
	}
	if code := waitExit(t, attempts[0].Dir); code != 0 {
		t.Fatalf("agent exit = %d", code)
	}
	seen, _ := os.ReadFile(filepath.Join(wt, "seen-prompt.txt"))
	if !strings.Contains(string(seen), "Read the brief at "+brief) {
		t.Errorf("prompt = %q", seen)
	}
	h.run(2, "run", "--worktree", wt)
	h.run(2, "run", "--brief", brief, "--worktree", t.TempDir())
	h.run(2, "run", "--brief", brief, "--worktree", wt, "--adapter", "screen")
}

func TestRunWaitsForQuotaWhenAllPaused(t *testing.T) {
	h := newHarness(t)
	wt, brief := worktree(t)
	h.run(0, "pause", "kimi")
	h.run(0, "pause", "agy")
	out := h.run(0, "run", "--brief", brief, "--worktree", wt)
	job, attempts := h.job(jobIDRe.FindString(out))
	if job.State != ledger.JobWaitingQuotaState || len(attempts) != 0 || !strings.Contains(out, "WAITING_QUOTA") {
		t.Fatalf("out = %q job = %+v", out, job)
	}
}

func TestCancelStopsAgent(t *testing.T) {
	agentBin(t, "kimi", `exec sleep 30`)
	h := newHarness(t)
	h.now = time.Now().UTC()
	wt, brief := worktree(t)
	id := jobIDRe.FindString(h.run(0, "run", "--brief", brief, "--worktree", wt))
	_, attempts := h.job(id)
	dir := attempts[0].Dir
	deadline := time.Now().Add(5 * time.Second)
	for !dispatch.Alive(dir) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	h.run(0, "cancel", id)
	if dispatch.Alive(dir) {
		t.Fatal("agent still running after cancel")
	}
	job, attempts := h.job(id)
	if job.State != ledger.JobCancelledState || attempts[0].ExitReason != "user_stop" {
		t.Fatalf("job = %s attempt = %+v", job.State, attempts[0])
	}
	h.run(1, "cancel", id)
}

func TestRunDryRunExplainsWithoutWritingTheLedger(t *testing.T) {
	h := newHarness(t)
	h.run(0, "pause", "kimi")
	e := h.engine()
	before, err := e.Ledger.Reader().LastSeq(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := h.run(0, "run", "--dry-run", "--no-sensors")
	for _, want := range []string{"dry run", "not recorded", "✗ kimi", "✓ agy", "picked", "help routing"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out)
		}
	}
	after, err := e.Ledger.Reader().LastSeq(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("dry run wrote %d ledger events", after-before)
	}
	h.run(2, "run", "--dry-run", "--no-sensors", "--provider", "missing")
}
