package engine

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KurobaneShin/hiraishin/internal/dispatch"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
)

func (e *Engine) AttemptDir(jobID string, num int) string {
	return filepath.Join(e.Paths.DataDir, "jobs", jobID, fmt.Sprintf("attempt-%d", num))
}

func (e *Engine) Launch(ctx context.Context, job ledger.JobRow, decisionID, providerID, prompt, resumeSession string) ledger.Event {
	ev, err := e.launch(ctx, job, decisionID, providerID, prompt, resumeSession)
	if err != nil {
		return ledger.Event{JobID: job.ID, ProviderID: providerID, Source: ledger.SourceScheduler, Payload: ledger.JobSpawnFailed{DecisionID: decisionID, Error: err.Error()}}
	}
	return ev
}

func (e *Engine) launch(ctx context.Context, job ledger.JobRow, decisionID, providerID, prompt, resumeSession string) (ledger.Event, error) {
	prov, ok := e.Cfg.Providers[providerID]
	if !ok {
		return ledger.Event{}, fmt.Errorf("provider %q is not configured", providerID)
	}
	cmd, err := dispatch.Build(prov, job.WorktreePath, resumeSession)
	if err != nil {
		return ledger.Event{}, err
	}
	cp, err := dispatch.ReadCheckpoint(ctx, job.WorktreePath)
	if err != nil {
		return ledger.Event{}, err
	}
	attempts, err := e.Ledger.Reader().Attempts(ctx, job.ID)
	if err != nil {
		return ledger.Event{}, err
	}
	num := len(attempts) + 1
	dir := e.AttemptDir(job.ID, num)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ledger.Event{}, err
	}
	script := filepath.Join(dir, dispatch.ScriptFile)
	for name, f := range map[string]struct {
		body string
		mode os.FileMode
	}{dispatch.PromptFile: {prompt, 0o600}, dispatch.ScriptFile: {dispatch.Script(cmd, job.WorktreePath, dir), 0o700}} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(f.body), f.mode); err != nil {
			return ledger.Event{}, err
		}
	}
	adapterName := cmp.Or(job.TerminalAdapter, e.Cfg.General.DefaultTerminalAdapter)
	adapter, err := dispatch.NewAdapter(adapterName)
	if err != nil {
		return ledger.Event{}, err
	}
	handle, err := adapter.Spawn(ctx, dispatch.Launch{Title: job.Name + "@" + providerID, Worktree: job.WorktreePath, Script: script})
	if err != nil {
		return ledger.Event{}, err
	}
	return ledger.Event{JobID: job.ID, ProviderID: providerID, Source: ledger.SourceScheduler, Payload: ledger.JobDispatched{
		AttemptID: ledger.NewID("att"), AttemptNum: num, DecisionID: decisionID, ProviderID: providerID,
		TerminalAdapter: adapterName, TerminalHandle: handle, AttemptDir: dir, StartCommit: cp.Head,
	}}, nil
}

func (e *Engine) Queue(ctx context.Context, job ledger.JobRow) (scheduler.Decision, error) {
	return e.QueueFrom(ctx, job, ledger.SourceCLI)
}

func (e *Engine) QueueFrom(ctx context.Context, job ledger.JobRow, source string) (scheduler.Decision, error) {
	var d scheduler.Decision
	floor := e.TierFloor(ctx, job)
	err := e.Ledger.Update(ctx, func(ctx context.Context, tx *ledger.Tx) ([]ledger.Event, error) {
		queued := ledger.Event{JobID: job.ID, Source: source, Payload: ledger.JobQueued{
			Name: job.Name, Role: job.Role, WorktreePath: job.WorktreePath, Branch: job.Branch, BriefPath: job.BriefPath,
			ReportPath: job.ReportPath, TerminalAdapter: job.TerminalAdapter, PinnedProvider: job.PinnedProvider, NeverSwitch: job.NeverSwitch,
		}}
		var dec ledger.Event
		var err error
		d, dec, err = e.Decide(ctx, tx, job, scheduler.TriggerInitial, nil, floor)
		if err != nil {
			return nil, err
		}
		evs := []ledger.Event{queued, dec}
		if d.ChosenProvider == "" {
			evs = append(evs, ledger.Event{JobID: job.ID, Source: ledger.SourceScheduler, Payload: ledger.WaitingQuota{EarliestResetAt: d.EarliestResetAt, BlockedProviders: d.BlockedProviders}})
		}
		return evs, nil
	})
	return d, err
}
