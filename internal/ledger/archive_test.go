package ledger_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/ledger"
)

func TestArchiveMovesOldTerminalJobEvents(t *testing.T) {
	l, now := openArchiveLedger(t)
	ctx := context.Background()
	configureArchive(t, l, "p")
	jobOld := queueArchiveJob(t, l, "job_old", now().Add(-48*time.Hour))
	completeJob(t, l, "job_old", now().Add(-47*time.Hour))
	jobLive := queueArchiveJob(t, l, "job_live", now().Add(-1*time.Hour))
	decideArchive(t, l, "job_live", "p", now().Add(-1*time.Hour))

	before := now().Add(-24 * time.Hour)
	archive := filepath.Join(t.TempDir(), "archive.db")
	copied, err := l.Archive(ctx, before, archive)
	if err != nil {
		t.Fatal(err)
	}
	if copied == 0 {
		t.Fatal("want some events archived")
	}

	live, err := l.Reader().JobEvents(ctx, jobOld)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("old job events still live: %d", len(live))
	}
	liveLive, err := l.Reader().JobEvents(ctx, jobLive)
	if err != nil {
		t.Fatal(err)
	}
	if len(liveLive) == 0 {
		t.Fatal("live job events were archived")
	}
	row, err := l.Reader().Job(ctx, jobLive)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != ledger.JobDispatchingState {
		t.Fatalf("live job state changed: %s", row.State)
	}
	decs, err := l.Reader().Decisions(ctx, jobLive)
	if err != nil {
		t.Fatal(err)
	}
	if len(decs) != 1 {
		t.Fatalf("live job decisions missing: %d", len(decs))
	}
}

func TestArchiveSafeWhileProviderStateChanges(t *testing.T) {
	l, now := openArchiveLedger(t)
	ctx := context.Background()
	configureArchive(t, l, "p")
	old := now().Add(-100 * time.Hour)
	mustAppendArchive(t, l, ledger.Event{ProviderID: "p", Source: ledger.SourceSensor, Payload: ledger.QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: ptrF(10), Confidence: ledger.ConfidenceExact}, TS: old})
	mustAppendArchive(t, l, ledger.Event{ProviderID: "p", Source: ledger.SourceCLI, Payload: ledger.Paused{By: "cli"}, TS: old})

	before := now().Add(-24 * time.Hour)
	archive := filepath.Join(t.TempDir(), "archive.db")
	copied, err := l.Archive(ctx, before, archive)
	if err != nil {
		t.Fatal(err)
	}
	if copied == 0 {
		t.Fatal("want provider events archived")
	}
	p, err := l.Reader().Provider(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if p.State != ledger.StatePaused {
		t.Fatalf("provider projection changed: %s", p.State)
	}
}

func openArchiveLedger(t *testing.T) (*ledger.Ledger, func() time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	l, err := ledger.Open(context.Background(), filepath.Join(t.TempDir(), "ledger.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, func() time.Time { return now }
}

func configureArchive(t *testing.T, l *ledger.Ledger, ids ...string) {
	t.Helper()
	for _, id := range ids {
		mustAppendArchive(t, l, ledger.Event{ProviderID: id, Source: ledger.SourceConfig, Payload: ledger.ProviderConfigured{Harness: "kimi-native", MaxConcurrent: 1}})
	}
}

func queueArchiveJob(t *testing.T, l *ledger.Ledger, job string, ts time.Time) string {
	t.Helper()
	if job == "" {
		job = ledger.NewID("job")
	}
	mustAppendArchive(t, l, ledger.Event{JobID: job, Source: ledger.SourceCLI, Payload: ledger.JobQueued{Name: job, Role: "default", WorktreePath: "/wt", Branch: "b", BriefPath: "/b", TerminalAdapter: "process"}, TS: ts})
	return job
}

func decideArchive(t *testing.T, l *ledger.Ledger, job, provider string, ts time.Time) string {
	t.Helper()
	id := ledger.NewID("dec")
	mustAppendArchive(t, l, ledger.Event{JobID: job, Source: ledger.SourceScheduler, Payload: ledger.DecisionMade{DecisionID: id, Trigger: "initial_dispatch", Profile: "balanced", ChosenProvider: provider, Role: "default", Candidates: json.RawMessage("[]"), Summary: "s"}, TS: ts})
	return id
}

func completeJob(t *testing.T, l *ledger.Ledger, job string, ts time.Time) {
	t.Helper()
	mustAppendArchive(t, l, ledger.Event{JobID: job, Source: ledger.SourceWatcher, Payload: ledger.JobCompleted{}, TS: ts})
}

func mustAppendArchive(t *testing.T, l *ledger.Ledger, ev ledger.Event) {
	t.Helper()
	if err := l.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
}

func ptrF(v float64) *float64 { return &v }

func TestArchiveDetachesAfterAFailedCopy(t *testing.T) {
	l, now := openArchiveLedger(t)
	ctx := context.Background()
	configureArchive(t, l, "p")
	queueArchiveJob(t, l, "job_old", now().Add(-48*time.Hour))
	completeJob(t, l, "job_old", now().Add(-47*time.Hour))
	broken := filepath.Join(t.TempDir(), "broken.db")
	db, err := sql.Open("sqlite", broken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE events (only_column TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := l.Archive(ctx, now().Add(-24*time.Hour), broken); err == nil {
		t.Fatal("copy into an incompatible archive table succeeded")
	}
	if _, err := l.Archive(ctx, now().Add(-24*time.Hour), filepath.Join(t.TempDir(), "archive.db")); err != nil {
		t.Fatalf("archive after a failed run: %v", err)
	}
}
