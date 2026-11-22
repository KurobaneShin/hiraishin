package cli

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/*.golden from the current output")

func topFixture(width, height int) topModel {
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	pct := func(v float64) *float64 { return &v }
	zai := []statusWindow{
		{Name: "5h", UsedPct: pct(88), ResetsAt: now.Add(72 * time.Minute), Confidence: "exact"},
		{Name: "weekly", UsedPct: pct(41), ResetsAt: now.Add(77 * time.Hour), Confidence: "exact"},
	}
	doc := statusDoc{Profile: "balanced", WatchRunning: true, Providers: []statusProvider{
		{ID: "glm-a", Pool: "zai", State: "DEGRADED", ActiveJobs: 1, MaxConcurrent: 2, Windows: zai},
		{ID: "glm-b", Pool: "zai", State: "AVAILABLE", MaxConcurrent: 2, Windows: zai},
		{ID: "kimi", State: "EXHAUSTED", MaxConcurrent: 2, CooldownUntil: now.Add(40 * time.Minute), LastError: "rate limit: weekly quota exhausted for this account, retry after the window resets at 15:40 UTC",
			Windows: []statusWindow{{Name: "5h", UsedPct: pct(100), ResetsAt: now.Add(40 * time.Minute), Confidence: "exact"}}},
		{ID: "claude", State: "AVAILABLE", ActiveJobs: 2, MaxConcurrent: 3, Windows: []statusWindow{{Name: "5h", UsedPct: pct(23), ResetsAt: now.Add(190 * time.Minute), Confidence: "estimated"}}},
		{ID: "agy", State: "PAUSED", MaxConcurrent: 1, Windows: []statusWindow{{Name: "5h", Confidence: "estimated"}}},
	}}
	jobs := []ledger.JobRow{
		{ID: "job_1", Name: "persist-all", State: ledger.JobRunningState, WorktreePath: "/home/u/orca/workspaces/hiraishin/v03-tui", CurrentProvider: "claude", UpdatedAt: now.Add(-12 * time.Minute)},
		{ID: "job_2", Name: "golden-tests", State: ledger.JobRunningState, WorktreePath: "/home/u/orca/workspaces/hiraishin/v03-tui", CurrentProvider: "glm-a", UpdatedAt: now.Add(-3 * time.Minute)},
		{ID: "job_3", Name: "policy-simulator-replay-engine", State: ledger.JobWaitingQuotaState, WorktreePath: "/home/u/orca/workspaces/hiraishin/v03-simulator", UpdatedAt: now.Add(-95 * time.Minute)},
		{ID: "job_4", Name: "doctor", State: ledger.JobRunningState, WorktreePath: "/home/u/orca/workspaces/hiraishin/v03-doctor", CurrentProvider: "claude", UpdatedAt: now.Add(-1 * time.Minute)},
	}
	failovers := []ledger.FailoverRow{
		{TS: now.Add(-8 * time.Minute), JobID: "job_2", JobName: "golden-tests", FromProvider: "kimi", Reason: "limit_hit"},
		{TS: now.Add(-50 * time.Minute), JobID: "job_3", JobName: "policy-simulator-replay-engine", FromProvider: "agy", Reason: "auth_error"},
	}
	var samples []ledger.SampleRow
	for i := range 24 {
		at := now.Add(-time.Duration(24-i) * 15 * time.Minute)
		samples = append(samples,
			ledger.SampleRow{At: at, ProviderID: "glm-a", WindowName: "5h", UsedPct: float64(i) * 4},
			ledger.SampleRow{At: at, ProviderID: "claude", WindowName: "5h", UsedPct: float64(i % 6 * 4)})
	}
	m := topModel{now: func() time.Time { return now }, warn: 85, reserve: 98, width: width, height: height,
		clock: now, cursor: 2, note: "glm-b: PAUSED",
		data: topData{seq: 1, status: doc, jobs: jobs, failovers: failovers, trend: trendByWindow(samples)}}
	m.order = providerOrder(doc)
	return m
}

func TestTopGoldenViews(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {160, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			got := topFixture(size[0], size[1]).render()
			lines := strings.Split(got, "\n")
			if len(lines) > size[1] {
				t.Errorf("%d lines exceed height %d", len(lines), size[1])
			}
			for i, l := range lines {
				if n := utf8.RuneCountInString(l); n > size[0] {
					t.Errorf("line %d is %d cells wide, max %d: %q", i, n, size[0], l)
				}
			}
			path := filepath.Join("testdata", fmt.Sprintf("top_%dx%d.golden", size[0], size[1]))
			if *updateGolden {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("view differs from %s (rerun with -update to accept):\n%s", path, got)
			}
		})
	}
}

func TestTopColorsStayInsideTheWidth(t *testing.T) {
	m := topFixture(80, 24)
	m.pal = palette{on: true}
	out := m.render()
	if !strings.Contains(out, "\x1b[31m") {
		t.Fatalf("want red for the exhausted window:\n%q", out)
	}
	for i, l := range strings.Split(out, "\n") {
		if n := utf8.RuneCountInString(stripANSI(l)); n > 80 {
			t.Errorf("colored line %d is %d cells wide: %q", i, n, l)
		}
	}
}

type observedTop struct {
	tea.Model
	views chan<- string
}

func (o observedTop) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := o.Model.Update(msg)
	o.Model = m
	select {
	case o.views <- m.View().Content:
	default:
	}
	return o, cmd
}

func waitForView(t *testing.T, views <-chan string, want string, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	deadline := time.After(within)
	for {
		select {
		case v := <-views:
			if strings.Contains(v, want) {
				return time.Since(start)
			}
		case <-deadline:
			t.Fatalf("view did not show %q within %s", want, within)
		}
	}
}

func TestTopShowsNewLedgerEventsWithinOneSecond(t *testing.T) {
	h := newHarness(t)
	e := h.engine()
	views := make(chan string, 1024)
	p := tea.NewProgram(observedTop{newTopModel(context.Background(), e, h.env.Now, palette{}), views},
		tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithWindowSize(80, 24), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()
	waitForView(t, views, "kimi", 2*time.Second)
	used := 73.0
	if err := e.Ledger.Append(context.Background(), ledger.Event{ProviderID: "kimi", Source: ledger.SourceSensor, Payload: ledger.QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: &used, Confidence: ledger.ConfidenceExact}}); err != nil {
		t.Fatal(err)
	}
	took := waitForView(t, views, "73%", time.Second)
	t.Logf("ledger event visible after %s", took)
	p.Quit()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func press(t *testing.T, m topModel, key tea.KeyPressMsg) topModel {
	t.Helper()
	next, cmd := m.Update(key)
	m = next.(topModel)
	if cmd != nil {
		next, _ = m.Update(cmd())
		m = next.(topModel)
	}
	return m
}

func selectProvider(t *testing.T, m topModel, id string) topModel {
	t.Helper()
	for range len(m.order) {
		m = press(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	}
	for range len(m.order) {
		if m.order[m.cursor] == id {
			return m
		}
		m = press(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	t.Fatalf("provider %s not in %v", id, m.order)
	return m
}

func cliEvents(t *testing.T, h *harness) []string {
	t.Helper()
	db, err := sql.Open("sqlite", config.ResolvePaths(h.env.Getenv).Ledger())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(context.Background(), `SELECT kind, COALESCE(provider_id, ''), COALESCE(job_id, ''), source, payload_json FROM events WHERE kind != 'provider.configured' ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var kind, provider, job, source, payload string
		if err := rows.Scan(&kind, &provider, &job, &source, &payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Join([]string{kind, provider, job, source, payload}, " "))
	}
	return out
}

func TestTopKeysAppendTheSameEventsAsTheCLI(t *testing.T) {
	viaCLI := newHarness(t)
	viaCLI.run(0, "pause", "kimi")
	viaCLI.run(0, "drain", "agy")
	viaCLI.run(0, "resume", "kimi")
	viaCLI.run(0, "profile", "set", "save-scarce")

	viaTUI := newHarness(t)
	m := newTopModel(context.Background(), viaTUI.engine(), viaTUI.env.Now, palette{})
	next, _ := m.Update(m.poll())
	m = next.(topModel)
	m = press(t, selectProvider(t, m, "kimi"), tea.KeyPressMsg{Code: 'p', Text: "p"})
	if m.note != "kimi: PAUSED" {
		t.Errorf("note after pause = %q", m.note)
	}
	m = press(t, selectProvider(t, m, "agy"), tea.KeyPressMsg{Code: 'd', Text: "d"})
	m = press(t, selectProvider(t, m, "kimi"), tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.note != "profile: save-scarce" {
		t.Errorf("note after profile toggle = %q", m.note)
	}

	want, got := cliEvents(t, viaCLI), cliEvents(t, viaTUI)
	if len(want) != 4 || strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("TUI events differ from CLI events\nCLI:\n%s\nTUI:\n%s", strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func TestTopProfileToggleSkipsUnconfiguredCustom(t *testing.T) {
	h := newHarness(t)
	h.run(0, "profile", "set", "balanced")
	m := newTopModel(context.Background(), h.engine(), h.env.Now, palette{})
	next, _ := m.Update(m.poll())
	press(t, next.(topModel), tea.KeyPressMsg{Code: tea.KeyTab})
	if out := h.run(0, "profile"); strings.TrimSpace(out) != "quality-first" {
		t.Fatalf("profile after toggle from balanced = %q", out)
	}
}

func TestTopNeedsATerminal(t *testing.T) {
	h := newHarness(t)
	h.run(1, "top")
	if !strings.Contains(h.err.String(), "terminal") {
		t.Fatalf("stderr = %q", h.err.String())
	}
}
