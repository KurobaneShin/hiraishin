package ledger

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func open(t *testing.T) (*Ledger, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)}
	l, err := Open(context.Background(), filepath.Join(t.TempDir(), "ledger.db"), c.now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, c
}

func mustAppend(t *testing.T, l *Ledger, evs ...Event) {
	t.Helper()
	if err := l.Append(context.Background(), evs...); err != nil {
		t.Fatal(err)
	}
}

func provider(t *testing.T, l *Ledger, id string) ProviderRow {
	t.Helper()
	p, err := l.Reader().Provider(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func configure(t *testing.T, l *Ledger, ids ...string) {
	t.Helper()
	for _, id := range ids {
		mustAppend(t, l, Event{ProviderID: id, Source: SourceConfig, Payload: ProviderConfigured{Harness: "kimi-native", MaxConcurrent: 2}})
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for range 2 {
		l, err := Open(context.Background(), filepath.Join(dir, "ledger.db"), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		_ = l.Close()
	}
}

func TestProviderStateTransitions(t *testing.T) {
	reset := time.Date(2026, 11, 21, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		events []Payload
		want   string
	}{
		{"configured", nil, StateAvailable},
		{"limit hit", []Payload{LimitHit{WindowName: "5h", EstimatedResetAt: reset}}, StateExhausted},
		{"auth", []Payload{AuthFailed{Excerpt: "401"}}, StateAuthError},
		{"degraded", []Payload{Degraded{CooldownUntil: reset}}, StateDegraded},
		{"degraded then recovered", []Payload{Degraded{CooldownUntil: reset}, Recovered{}}, StateAvailable},
		{"pause", []Payload{Paused{By: "cli"}}, StatePaused},
		{"pause survives limit", []Payload{Paused{By: "cli"}, LimitHit{WindowName: "5h", EstimatedResetAt: reset}}, StatePaused},
		{"resume while window empty", []Payload{Paused{By: "cli"}, LimitHit{WindowName: "5h", EstimatedResetAt: reset}, Resumed{By: "cli"}}, StateExhausted},
		{"resume", []Payload{Paused{By: "cli"}, Resumed{By: "cli"}}, StateAvailable},
		{"drain idle goes paused", []Payload{Draining{By: "cli"}}, StatePaused},
		{"window reset", []Payload{LimitHit{WindowName: "5h", EstimatedResetAt: reset}, WindowReset{WindowName: "5h"}}, StateAvailable},
		{"reset of one window keeps other exhausted", []Payload{
			LimitHit{WindowName: "5h", EstimatedResetAt: reset},
			LimitHit{WindowName: "weekly", EstimatedResetAt: reset.Add(72 * time.Hour)},
			WindowReset{WindowName: "5h"},
		}, StateExhausted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, _ := open(t)
			configure(t, l, "p")
			for _, p := range tt.events {
				mustAppend(t, l, Event{ProviderID: "p", Source: SourceCLI, Payload: p})
			}
			if got := provider(t, l, "p").State; got != tt.want {
				t.Errorf("state = %s, want %s", got, tt.want)
			}
		})
	}
}

func queue(t *testing.T, l *Ledger, job string) {
	t.Helper()
	mustAppend(t, l, Event{JobID: job, Source: SourceCLI, Payload: JobQueued{Name: job, Role: "default", WorktreePath: "/wt", Branch: "b", BriefPath: "/brief.md", TerminalAdapter: "process"}})
}

func decide(t *testing.T, l *Ledger, job, providerID string) string {
	t.Helper()
	id := NewID("dec")
	mustAppend(t, l, Event{JobID: job, Source: SourceScheduler, Payload: DecisionMade{DecisionID: id, Trigger: "initial_dispatch", Profile: "balanced", ChosenProvider: providerID, Role: "default", Candidates: json.RawMessage("[]"), Summary: "s"}})
	return id
}

func dispatch(t *testing.T, l *Ledger, job, providerID, decision string, n int) string {
	t.Helper()
	att := NewID("att")
	mustAppend(t, l, Event{JobID: job, ProviderID: providerID, Source: SourceCLI, Payload: JobDispatched{AttemptID: att, AttemptNum: n, DecisionID: decision, ProviderID: providerID, TerminalAdapter: "process", TerminalHandle: "123", AttemptDir: "/a"}})
	return att
}

func TestJobLifecycleAndSlots(t *testing.T) {
	l, _ := open(t)
	ctx := context.Background()
	configure(t, l, "a", "b")
	queue(t, l, "job_1")
	d := decide(t, l, "job_1", "a")
	if got := provider(t, l, "a").ActiveJobs; got != 1 {
		t.Fatalf("slot after decision = %d, want 1", got)
	}
	att := dispatch(t, l, "job_1", "a", d, 1)
	mustAppend(t, l, Event{JobID: "job_1", ProviderID: "a", Source: SourceWatcher, Payload: FailoverStarted{AttemptID: att, FromProvider: "a", ExitReason: "quota_exhausted"}})
	if got := provider(t, l, "a").ActiveJobs; got != 0 {
		t.Fatalf("slot after failover = %d, want 0", got)
	}
	d2 := decide(t, l, "job_1", "b")
	att2 := dispatch(t, l, "job_1", "b", d2, 2)
	mustAppend(t, l, Event{JobID: "job_1", Source: SourceWatcher, Payload: JobCompleted{AttemptID: att2, EndCommit: "abc"}})

	job, err := l.Reader().Job(ctx, "job_1")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != JobCompletedState || job.CurrentProvider != "b" || job.FailoverCount != 1 {
		t.Errorf("job = %+v", job)
	}
	atts, err := l.Reader().Attempts(ctx, "job_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 2 || atts[0].ExitReason != "quota_exhausted" || atts[1].ExitReason != "completed" || atts[1].EndCommit != "abc" {
		t.Errorf("attempts = %+v", atts)
	}
	if got := provider(t, l, "b").ActiveJobs; got != 0 {
		t.Errorf("slot b = %d, want 0", got)
	}
}

func TestDrainingSettlesWhenLastJobEnds(t *testing.T) {
	l, _ := open(t)
	configure(t, l, "a")
	queue(t, l, "job_1")
	d := decide(t, l, "job_1", "a")
	att := dispatch(t, l, "job_1", "a", d, 1)
	mustAppend(t, l, Event{ProviderID: "a", Source: SourceCLI, Payload: Draining{By: "cli"}})
	if got := provider(t, l, "a").State; got != StateDraining {
		t.Fatalf("state = %s, want DRAINING", got)
	}
	mustAppend(t, l, Event{JobID: "job_1", Source: SourceWatcher, Payload: JobCompleted{AttemptID: att}})
	if got := provider(t, l, "a").State; got != StatePaused {
		t.Errorf("state = %s, want PAUSED", got)
	}
}

func TestUpdateRollsBackOnError(t *testing.T) {
	l, _ := open(t)
	configure(t, l, "a")
	err := l.Update(context.Background(), func(context.Context, *Tx) ([]Event, error) {
		return []Event{
			{ProviderID: "a", Source: SourceCLI, Payload: Paused{By: "cli"}},
			{JobID: "missing", Source: SourceCLI, Payload: JobCompleted{}},
		}, nil
	})
	if err == nil {
		t.Fatal("want error for unknown job")
	}
	if got := provider(t, l, "a").State; got != StateAvailable {
		t.Errorf("state = %s, want rollback to AVAILABLE", got)
	}
}

func TestConcurrentUpdatesSerialize(t *testing.T) {
	l, _ := open(t)
	configure(t, l, "a")
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			job := "job_" + string(rune('a'+i))
			errs <- l.Update(context.Background(), func(ctx context.Context, tx *Tx) ([]Event, error) {
				p, err := tx.Provider(ctx, "a")
				if err != nil {
					return nil, err
				}
				evs := []Event{{JobID: job, Source: SourceCLI, Payload: JobQueued{Name: job, Role: "r", WorktreePath: "/w", Branch: "b", BriefPath: "/b", TerminalAdapter: "process"}}}
				if p.ActiveJobs < p.MaxConcurrent {
					evs = append(evs, Event{JobID: job, Source: SourceScheduler, Payload: DecisionMade{DecisionID: NewID("dec"), Trigger: "initial_dispatch", Profile: "x", ChosenProvider: "a", Candidates: json.RawMessage("[]"), Summary: "s"}})
				}
				return evs, nil
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := provider(t, l, "a").ActiveJobs; got != 2 {
		t.Errorf("active = %d, want exactly max_concurrent 2", got)
	}
}

func TestPrefersExpire(t *testing.T) {
	l, c := open(t)
	mustAppend(t, l, Event{ProviderID: "a", Source: SourceCLI, Payload: PreferSet{Role: "", ExpiresAt: c.now().Add(time.Hour)}})
	ctx := context.Background()
	got, err := l.Reader().Prefers(ctx, c.now())
	if err != nil || len(got) != 1 {
		t.Fatalf("prefers = %v, %v", got, err)
	}
	c.add(2 * time.Hour)
	got, err = l.Reader().Prefers(ctx, c.now())
	if err != nil || len(got) != 0 {
		t.Fatalf("expired prefers = %v, %v", got, err)
	}
}

func TestLatestAttemptsOnePerJobAndLastSeq(t *testing.T) {
	l, _ := open(t)
	configure(t, l, "kimi", "agy")
	ctx := context.Background()
	if seq, err := l.Reader().LastSeq(ctx); err != nil || seq == 0 {
		t.Fatalf("seq = %d %v", seq, err)
	}
	queue(t, l, "job_a")
	a1 := dispatch(t, l, "job_a", "kimi", decide(t, l, "job_a", "kimi"), 1)
	mustAppend(t, l, Event{JobID: "job_a", ProviderID: "kimi", Source: SourceWatcher, Payload: FailoverStarted{AttemptID: a1, FromProvider: "kimi", ExitReason: "limit_hit"}})
	a2 := dispatch(t, l, "job_a", "agy", decide(t, l, "job_a", "agy"), 2)
	queue(t, l, "job_b")
	b1 := dispatch(t, l, "job_b", "agy", decide(t, l, "job_b", "agy"), 1)
	mustAppend(t, l, Event{JobID: "job_b", ProviderID: "agy", Source: SourceWatcher, Payload: JobCompleted{AttemptID: b1}})
	before, _ := l.Reader().LastSeq(ctx)
	got, err := l.Reader().LatestAttempts(ctx, JobRunningState)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["job_a"].ID != a2 || got["job_a"].Num != 2 {
		t.Fatalf("running latest = %+v", got)
	}
	all, _ := l.Reader().LatestAttempts(ctx, JobRunningState, JobCompletedState)
	if len(all) != 2 || all["job_b"].ID != b1 {
		t.Fatalf("latest = %+v", all)
	}
	queue(t, l, "job_c")
	if after, _ := l.Reader().LastSeq(ctx); after != before+1 {
		t.Errorf("seq %d -> %d", before, after)
	}
}

func TestQuotaSamplesSinceInOrderWithUsage(t *testing.T) {
	l, c := open(t)
	configure(t, l, "kimi", "agy")
	used := func(v float64) *float64 { return &v }
	start := c.now()
	reset := start.Add(5 * time.Hour)
	mustAppend(t, l, Event{ProviderID: "kimi", Source: SourceSensor, Payload: QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: used(10), ResetsAt: reset, Confidence: ConfidenceExact}})
	c.add(time.Hour)
	since := c.now()
	mustAppend(t, l,
		Event{ProviderID: "kimi", Source: SourceSensor, Payload: QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: used(20), ResetsAt: reset, Confidence: ConfidenceExact}},
		Event{ProviderID: "agy", Source: SourceSensor, Payload: QuotaSampled{WindowName: "daily", WindowKind: "fixed_reset", Confidence: ConfidenceInferred}},
		Event{ProviderID: "agy", Source: SourceSensor, Payload: ProbeFailed{SensorTier: "endpoint", Error: "boom"}},
	)
	c.add(10 * time.Minute)
	mustAppend(t, l, Event{ProviderID: "kimi", Source: SourceSensor, Payload: QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: used(22), ResetsAt: reset, Confidence: ConfidenceExact}})
	got, err := l.Reader().QuotaSamples(context.Background(), since)
	if err != nil {
		t.Fatal(err)
	}
	want := []SampleRow{
		{ProviderID: "kimi", WindowName: "5h", At: since, UsedPct: 20, ResetsAt: reset},
		{ProviderID: "kimi", WindowName: "5h", At: since.Add(10 * time.Minute), UsedPct: 22, ResetsAt: reset},
	}
	if len(got) != len(want) {
		t.Fatalf("samples = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].ProviderID != want[i].ProviderID || got[i].WindowName != want[i].WindowName || !got[i].At.Equal(want[i].At) || got[i].UsedPct != want[i].UsedPct || !got[i].ResetsAt.Equal(want[i].ResetsAt) {
			t.Errorf("sample %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func outcome(provider, role string, quality float64, frozen bool) Event {
	return Event{ProviderID: provider, Source: SourceCLI, Payload: OutcomeRecorded{Role: role, Success: quality > 0.5, ReviewRounds: 2, BuildFailures: 1, DurationMin: 30, Quality: quality, Alpha: 0.5, Frozen: frozen}}
}

func learningStats(t *testing.T, l *Ledger, role string) []LearningRow {
	t.Helper()
	rows, err := l.Reader().LearningStats(context.Background(), role)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestOutcomesUpdateLearningStatsAsEWMA(t *testing.T) {
	l, _ := open(t)
	mustAppend(t, l, outcome("kimi", "gameplay", 0.8, false), outcome("kimi", "gameplay", 0.4, false), outcome("agy", "gameplay", 0.9, false), outcome("kimi", "docs", 0.1, false))
	rows := learningStats(t, l, "gameplay")
	if len(rows) != 2 || rows[0].ProviderID != "agy" || rows[1].ProviderID != "kimi" {
		t.Fatalf("rows = %+v", rows)
	}
	k := rows[1]
	if k.Role != "gameplay" || k.Samples != 2 || k.CompletedOK != 1 || math.Abs(k.EWMAQuality-0.6) > 1e-9 || k.AvgReviewRounds != 2 || k.AvgBuildFailures != 1 || k.AvgDurationMin != 30 {
		t.Fatalf("kimi = %+v", k)
	}
	if all := learningStats(t, l, ""); len(all) != 3 {
		t.Fatalf("all roles = %+v", all)
	}
}

func TestOutcomeWithoutDurationKeepsDurationAverage(t *testing.T) {
	l, _ := open(t)
	first := outcome("kimi", "gameplay", 0.5, false)
	p := first.Payload.(OutcomeRecorded)
	p.DurationMin = 0
	first.Payload = p
	mustAppend(t, l, first, outcome("kimi", "gameplay", 0.5, false), first)
	if k := learningStats(t, l, "gameplay")[0]; k.AvgDurationMin != 30 || k.Samples != 3 {
		t.Fatalf("kimi = %+v", k)
	}
}

func TestFrozenOutcomeIsLoggedButLeavesStatsUntouched(t *testing.T) {
	l, _ := open(t)
	mustAppend(t, l, outcome("kimi", "gameplay", 0.8, false))
	before := learningStats(t, l, "gameplay")
	mustAppend(t, l, outcome("kimi", "gameplay", 0.0, true), outcome("agy", "gameplay", 0.0, true))
	if after := learningStats(t, l, "gameplay"); len(after) != 1 || after[0] != before[0] {
		t.Fatalf("frozen outcome changed stats: %+v -> %+v", before, after)
	}
	var n int
	if err := l.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM events WHERE kind = ?`, string(KindOutcomeRecorded)).Scan(&n); err != nil || n != 3 {
		t.Fatalf("outcome events = %d, %v", n, err)
	}
}

func TestLearnFreezeUnfreezeAndReset(t *testing.T) {
	l, _ := open(t)
	frozen := func() bool {
		f, err := l.Reader().LearningFrozen(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	if frozen() {
		t.Fatal("frozen before any freeze")
	}
	mustAppend(t, l, Event{Source: SourceCLI, Payload: LearnFrozen{By: "cli"}})
	if !frozen() {
		t.Fatal("not frozen after learning.frozen")
	}
	mustAppend(t, l, Event{Source: SourceCLI, Payload: LearnUnfrozen{By: "cli"}})
	if frozen() {
		t.Fatal("still frozen after learning.unfrozen")
	}
	mustAppend(t, l, outcome("kimi", "gameplay", 0.8, false), outcome("agy", "docs", 0.8, false), Event{Source: SourceCLI, Payload: LearnReset{By: "cli"}})
	if rows := learningStats(t, l, ""); len(rows) != 0 {
		t.Fatalf("stats after reset = %+v", rows)
	}
}
