package ledger

import (
	"context"
	"testing"
	"time"
)

func TestRecentFailoversJoinJobNameNewestFirst(t *testing.T) {
	l, c := open(t)
	queue := func(id, name string) Event {
		return Event{JobID: id, Source: SourceCLI, Payload: JobQueued{Name: name, Role: "default", WorktreePath: "/wt/" + id, Branch: id, BriefPath: "/b.md", TerminalAdapter: "process"}}
	}
	mustAppend(t, l, queue("job_a", "persist-all"), queue("job_b", "ui-polish"))
	mustAppend(t, l, Event{JobID: "job_a", Source: SourceScheduler, Payload: FailoverStarted{FromProvider: "kimi", Reason: "limit_hit"}})
	c.add(time.Minute)
	mustAppend(t, l, Event{JobID: "job_b", Source: SourceScheduler, Payload: FailoverStarted{FromProvider: "agy", Reason: "auth_error"}})
	got, err := l.Reader().RecentFailovers(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].JobID != "job_b" || got[0].FromProvider != "agy" || got[0].JobName != "ui-polish" ||
		got[1].JobName != "persist-all" || got[1].FromProvider != "kimi" || got[1].Reason != "limit_hit" || !got[1].TS.Equal(c.now().Add(-time.Minute)) {
		t.Fatalf("failovers = %+v", got)
	}
	if got, _ := l.Reader().RecentFailovers(context.Background(), 1); len(got) != 1 {
		t.Fatalf("limit ignored: %+v", got)
	}
}

func TestQuotaSamplesSinceSkipsUnknownUsage(t *testing.T) {
	l, c := open(t)
	sample := func(provider string, used *float64) Event {
		return Event{ProviderID: provider, Source: SourceSensor, Payload: QuotaSampled{WindowName: "5h", WindowKind: "rolling", UsedPct: used, Confidence: ConfidenceExact}}
	}
	old, mid, last := 10.0, 20.0, 30.0
	mustAppend(t, l, Event{ProviderID: "kimi", Source: SourceConfig, Payload: ProviderConfigured{Harness: "kimi-native", MaxConcurrent: 1}},
		Event{ProviderID: "agy", Source: SourceConfig, Payload: ProviderConfigured{Harness: "antigravity-agy", MaxConcurrent: 1}})
	mustAppend(t, l, sample("kimi", &old))
	c.add(time.Hour)
	since := c.now()
	mustAppend(t, l, sample("kimi", &mid), sample("kimi", nil), sample("agy", &last))
	got, err := l.Reader().QuotaSamples(context.Background(), since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ProviderID != "kimi" || got[0].WindowName != "5h" || got[0].UsedPct != 20 || got[1].ProviderID != "agy" || !got[1].At.Equal(since) {
		t.Fatalf("samples = %+v", got)
	}
}
