package watch

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/KurobaneShin/hiraishin/internal/config"
	"github.com/KurobaneShin/hiraishin/internal/dispatch"
	"github.com/KurobaneShin/hiraishin/internal/engine"
	"github.com/KurobaneShin/hiraishin/internal/ledger"
	"github.com/KurobaneShin/hiraishin/internal/scheduler"
	"github.com/KurobaneShin/hiraishin/internal/sensor"
)

const maxReadPerTick = 1 << 20

type Watcher struct {
	engine         *engine.Engine
	probe          *sensor.Probe
	log            *slog.Logger
	grace          time.Duration
	tracker        *sensor.Tracker
	guard          *sensor.Guard
	tails          map[string]*tail
	pendingConfirm map[string]time.Time
	seenSeq        int64
}

type tail struct {
	offset       int64
	partial      []byte
	sessionSeen  bool
	degradedSeen bool
}

func New(e *engine.Engine, probe *sensor.Probe, log *slog.Logger, grace time.Duration) *Watcher {
	return &Watcher{engine: e, probe: probe, log: log, grace: grace, tracker: sensor.NewTracker(), guard: sensor.NewGuard(), tails: map[string]*tail{}, pendingConfirm: map[string]time.Time{}}
}

func (w *Watcher) Run(ctx context.Context, interval time.Duration, exitWhenIdle bool) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		active, err := w.Tick(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		if exitWhenIdle && active == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (w *Watcher) Tick(ctx context.Context) (int, error) {
	e := w.engine
	if reloaded, err := e.Reload(ctx); err != nil {
		w.log.Warn("preferences not reloaded, keeping the loaded ones", "err", err)
	} else if reloaded {
		w.log.Info("preferences reloaded", "path", e.Paths.Config)
	}
	if err := e.Maintain(ctx); err != nil {
		return 0, err
	}
	if err := w.confirmPending(ctx); err != nil {
		return 0, err
	}
	if err := w.Poll(ctx); err != nil {
		return 0, err
	}
	r := e.Ledger.Reader()
	running, err := r.Jobs(ctx, ledger.JobRunningState)
	if err != nil {
		return 0, err
	}
	latest, err := r.LatestAttempts(ctx, ledger.JobRunningState, ledger.JobFailingOverState, ledger.JobWaitingQuotaState, ledger.JobQueuedState)
	if err != nil {
		return 0, err
	}
	for _, job := range running {
		if att, ok := latest[job.ID]; ok && att.EndedAt.IsZero() {
			if err := w.check(ctx, job, att); err != nil {
				return 0, fmt.Errorf("job %s: %w", job.ID, err)
			}
		}
	}
	pending, err := r.Jobs(ctx, ledger.JobFailingOverState, ledger.JobWaitingQuotaState, ledger.JobQueuedState)
	if err != nil {
		return 0, err
	}
	seq, err := r.LastSeq(ctx)
	if err != nil {
		return 0, err
	}
	ledgerChanged := seq != w.seenSeq
	w.seenSeq = seq
	if len(pending) > 0 && ledgerChanged {
		if latest, err = r.LatestAttempts(ctx, ledger.JobFailingOverState, ledger.JobWaitingQuotaState, ledger.JobQueuedState); err != nil {
			return 0, err
		}
	}
	for _, job := range pending {
		if job.State == ledger.JobWaitingQuotaState && !ledgerChanged {
			continue
		}
		var att *ledger.AttemptRow
		if a, ok := latest[job.ID]; ok {
			att = &a
		}
		if err := w.reschedule(ctx, job, att); err != nil {
			return 0, fmt.Errorf("job %s: %w", job.ID, err)
		}
	}
	active, err := e.Ledger.Reader().Jobs(ctx, ledger.JobRunningState, ledger.JobFailingOverState)
	return len(active), err
}

func (w *Watcher) check(ctx context.Context, job ledger.JobRow, att ledger.AttemptRow) error {
	e := w.engine
	code, exited, err := dispatch.ExitCode(att.Dir)
	if err != nil {
		return err
	}
	lines, err := w.read(&att, exited)
	if err != nil {
		return err
	}
	prov := e.Cfg.Providers[att.ProviderID]
	fam := sensor.FamilyOf(prov)
	var evs []ledger.Event
	t := w.tails[att.ID]
	for _, line := range lines {
		if !t.sessionSeen {
			if id := sensor.SessionID(line); id != "" {
				t.sessionSeen = true
				if id != job.NativeSessionID {
					evs = append(evs, ledger.Event{JobID: job.ID, ProviderID: att.ProviderID, Source: ledger.SourceWatcher, Payload: ledger.SessionDetected{NativeSessionID: id}})
				}
			}
		}
		now := e.Now()
		sig, ok := sensor.Classify(fam, line, now)
		if !ok {
			continue
		}
		sig = w.tracker.Observe(att.ProviderID, sig, now)
		if sig.Kind == sensor.Degraded {
			t.degradedSeen = true
			if sig.Escalate != nil && sig.Escalate.After > 0 {
				if _, queued := w.pendingConfirm[att.ProviderID]; !queued {
					w.pendingConfirm[att.ProviderID] = now.Add(sig.Escalate.After)
				}
			}
			evs = append(evs, ledger.Event{ProviderID: att.ProviderID, Source: ledger.SourceWatcher, Payload: ledger.Degraded{MatchedPattern: sig.Pattern, Excerpt: sig.Excerpt, CooldownUntil: now.Add(sig.Cooldown)}})
			continue
		}
		if err := e.Ledger.Append(ctx, evs...); err != nil {
			return err
		}
		return w.failover(ctx, job, att, sig)
	}
	if !exited && (dispatch.Alive(att.Dir) || !started(att.Dir)) {
		return e.Ledger.Append(ctx, evs...)
	}
	if !exited {
		code = -1
	}
	cp, _ := dispatch.ReadCheckpoint(ctx, job.WorktreePath)
	degraded := t.degradedSeen
	delete(w.tails, att.ID)
	if code == 0 {
		if !degraded {
			w.tracker.Clear(att.ProviderID)
		}
		minutes := e.Now().Sub(att.StartedAt).Minutes()
		evs = append(evs, ledger.Event{JobID: job.ID, ProviderID: att.ProviderID, Source: ledger.SourceWatcher, Payload: ledger.JobCompleted{AttemptID: att.ID, EndCommit: cp.Head, DurationMin: minutes}})
		w.log.Info("job completed", "job", job.ID, "provider", att.ProviderID)
		if err := e.Ledger.Update(ctx, func(ctx context.Context, tx *ledger.Tx) ([]ledger.Event, error) {
			outcome, err := e.OutcomeEvent(ctx, tx, att.ProviderID, job.Role, job.ID, ledger.SourceWatcher, scheduler.Outcome{Success: true, DurationMin: minutes})
			return append(evs, outcome), err
		}); err != nil {
			return err
		}
		w.recordOutcome(ctx, job.ID)
		return nil
	}
	reason := fmt.Sprintf("agent exited with code %d", code)
	if code == -1 {
		reason = "agent process ended without an exit code"
	}
	evs = append(evs, ledger.Event{JobID: job.ID, ProviderID: att.ProviderID, Source: ledger.SourceWatcher, Payload: ledger.JobFailed{AttemptID: att.ID, EndCommit: cp.Head, ExitCode: code, Reason: reason}})
	w.log.Warn("job failed", "job", job.ID, "provider", att.ProviderID, "reason", reason)
	if err := e.Ledger.Append(ctx, evs...); err != nil {
		return err
	}
	w.recordOutcome(ctx, job.ID)
	return nil
}

func (w *Watcher) recordOutcome(ctx context.Context, jobID string) {
	if err := w.engine.RecordOutcome(ctx, jobID); err != nil {
		w.log.Debug("ai-memory outcome page skipped", "job", jobID, "err", err)
	}
}

func started(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, dispatch.PgidFile))
	return err == nil
}

func (w *Watcher) read(att *ledger.AttemptRow, final bool) ([]string, error) {
	t := w.tails[att.ID]
	if t == nil {
		t = &tail{}
		w.tails[att.ID] = t
	}
	f, err := os.Open(filepath.Join(att.Dir, dispatch.OutputFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, err
	}
	chunk, err := io.ReadAll(io.LimitReader(f, maxReadPerTick))
	if err != nil {
		return nil, err
	}
	t.offset += int64(len(chunk))
	buf := slices.Concat(t.partial, chunk)
	var lines []string
	for {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, string(buf[:i]))
		buf = buf[i+1:]
	}
	t.partial = slices.Clone(buf)
	if final && len(chunk) < maxReadPerTick && len(t.partial) > 0 {
		lines = append(lines, string(t.partial))
		t.partial = nil
	}
	return lines, nil
}

func windowReset(p config.Provider, name string, sig sensor.Signal, now time.Time) (string, time.Time) {
	for _, win := range p.Windows {
		if win.Name == name {
			if !sig.ResetAt.IsZero() {
				return win.Kind, sig.ResetAt
			}
			if win.Kind == "fixed_reset" && win.FixedResetTZ != "" {
				if at, ok := sensor.NextMidnightIn(win.FixedResetTZ, now); ok {
					return win.Kind, at
				}
			}
			return win.Kind, now.Add(time.Duration(win.DurationHours * float64(time.Hour)))
		}
	}
	return "", sig.ResetAt
}

func (w *Watcher) confirmPending(ctx context.Context) error {
	if len(w.pendingConfirm) == 0 {
		return nil
	}
	e := w.engine
	now := e.Now()
	var due []string
	for id, at := range w.pendingConfirm {
		if !at.After(now) {
			due = append(due, id)
		}
	}
	if len(due) == 0 {
		return nil
	}
	slices.Sort(due)
	var evs []ledger.Event
	for _, id := range due {
		delete(w.pendingConfirm, id)
		if w.probe == nil || w.probe.Run == nil {
			continue
		}
		prov, ok := e.Cfg.Providers[id]
		if !ok || sensor.FamilyOf(prov) != sensor.FamilyGemini {
			continue
		}
		sig, hit, err := w.probe.ProbeGeminiLimit(ctx, prov)
		if err != nil {
			evs = append(evs, ledger.Event{ProviderID: id, Source: ledger.SourceSensor, Payload: ledger.ProbeFailed{SensorTier: "confirm", Error: err.Error()}})
			continue
		}
		if !hit {
			w.tracker.Clear(id)
			continue
		}
		sig = w.tracker.Observe(id, sig, now)
		switch sig.Kind {
		case sensor.AuthError:
			evs = append(evs, ledger.Event{ProviderID: id, Source: ledger.SourceSensor, Payload: ledger.AuthFailed{MatchedPattern: sig.Pattern, Excerpt: sig.Excerpt}})
		case sensor.Exhausted:
			for _, member := range e.Cfg.PoolMembers(id) {
				kind, reset := windowReset(e.Cfg.Providers[member], sig.Window, sig, now)
				evs = append(evs, ledger.Event{ProviderID: member, Source: ledger.SourceSensor, Payload: ledger.LimitHit{WindowName: sig.Window, WindowKind: kind, MatchedPattern: sig.Pattern, Excerpt: sig.Excerpt, EstimatedResetAt: reset}})
			}
		}
	}
	return e.Ledger.Append(ctx, evs...)
}

func (w *Watcher) failover(ctx context.Context, job ledger.JobRow, att ledger.AttemptRow, sig sensor.Signal) error {
	e := w.engine
	if err := dispatch.Stop(ctx, att.Dir, w.grace); err != nil && !errors.Is(err, dispatch.ErrNoProcess) {
		return err
	}
	delete(w.tails, att.ID)
	cp, err := dispatch.ReadCheckpoint(ctx, job.WorktreePath)
	if err != nil {
		return err
	}
	now := e.Now()
	var evs []ledger.Event
	exitReason := "limit_hit"
	if sig.Kind == sensor.AuthError {
		exitReason = "auth_error"
		evs = append(evs, ledger.Event{ProviderID: att.ProviderID, Source: ledger.SourceWatcher, Payload: ledger.AuthFailed{MatchedPattern: sig.Pattern, Excerpt: sig.Excerpt}})
	} else {
		for _, id := range e.Cfg.PoolMembers(att.ProviderID) {
			kind, reset := windowReset(e.Cfg.Providers[id], sig.Window, sig, now)
			evs = append(evs, ledger.Event{ProviderID: id, Source: ledger.SourceWatcher, Payload: ledger.LimitHit{WindowName: sig.Window, WindowKind: kind, MatchedPattern: sig.Pattern, Excerpt: sig.Excerpt, EstimatedResetAt: reset}})
		}
	}
	evs = append(evs, ledger.Event{JobID: job.ID, ProviderID: att.ProviderID, Source: ledger.SourceWatcher, Payload: ledger.FailoverStarted{
		AttemptID: att.ID, FromProvider: att.ProviderID, Reason: sig.Excerpt, ExitReason: exitReason, HeadCommit: cp.Head, DirtyFiles: cp.Dirty,
	}})
	w.log.Warn("limit hit, failing over", "job", job.ID, "provider", att.ProviderID, "pattern", sig.Pattern, "dirty_files", len(cp.Dirty))
	return e.Ledger.Append(ctx, evs...)
}

func (w *Watcher) failoverReason(ctx context.Context, jobID string, att *ledger.AttemptRow) (string, error) {
	evs, err := w.engine.Ledger.Reader().JobEvents(ctx, jobID)
	if err != nil {
		return "", err
	}
	for _, ev := range slices.Backward(evs) {
		if ev.Kind == ledger.KindFailoverStarted {
			var p ledger.FailoverStarted
			if err := json.Unmarshal([]byte(ev.Payload), &p); err != nil {
				return "", err
			}
			return p.Reason, nil
		}
	}
	return att.ExitReason, nil
}

func (w *Watcher) reschedule(ctx context.Context, job ledger.JobRow, att *ledger.AttemptRow) error {
	e := w.engine
	trigger := map[string]string{ledger.JobFailingOverState: scheduler.TriggerFailover, ledger.JobWaitingQuotaState: scheduler.TriggerRetryAfterReset}[job.State]
	trigger = cmp.Or(trigger, scheduler.TriggerInitial)
	var exclude []string
	resumable := w.resumable(job, att)
	waitForReset := false
	if att != nil {
		switch {
		case job.NeverSwitch:
			job.PinnedProvider = cmp.Or(job.PinnedProvider, att.ProviderID)
		case job.PinnedProvider == "" && resumable:
			waitForReset = true
		case job.State == ledger.JobFailingOverState:
			exclude = e.Cfg.PoolMembers(att.ProviderID)
		}
	}
	var d scheduler.Decision
	err := e.Ledger.Update(ctx, func(ctx context.Context, tx *ledger.Tx) ([]ledger.Event, error) {
		var dec ledger.Event
		var err error
		if waitForReset {
			pinned := job
			pinned.PinnedProvider = att.ProviderID
			if d, dec, err = e.Decide(ctx, tx, pinned, trigger, nil); err != nil {
				return nil, err
			}
			if d.ChosenProvider == "" {
				reset, err := w.resetAt(ctx, tx, att.ProviderID)
				if err != nil {
					return nil, err
				}
				waitForReset = !reset.IsZero() && !reset.After(att.EndedAt.Add(e.Cfg.General.FailoverWaitMax()))
				d.EarliestResetAt = reset
			}
		}
		if !waitForReset {
			if job.State == ledger.JobFailingOverState && att != nil && !job.NeverSwitch {
				exclude = e.Cfg.PoolMembers(att.ProviderID)
			}
			if d, dec, err = e.Decide(ctx, tx, job, trigger, exclude); err != nil {
				return nil, err
			}
		}
		switch {
		case d.ChosenProvider != "":
			return []ledger.Event{dec}, nil
		case job.State == ledger.JobWaitingQuotaState:
			return nil, nil
		default:
			return []ledger.Event{dec, {JobID: job.ID, Source: ledger.SourceScheduler, Payload: ledger.WaitingQuota{EarliestResetAt: d.EarliestResetAt, BlockedProviders: d.BlockedProviders}}}, nil
		}
	})
	if err != nil || d.ChosenProvider == "" {
		if err == nil && job.State != ledger.JobWaitingQuotaState {
			w.log.Warn("no eligible provider, job waiting", "job", job.ID, "full", d.FullProviders, "earliest_reset", d.EarliestResetAt)
		}
		return err
	}
	session := ""
	if resumable && d.ChosenProvider == att.ProviderID {
		session = job.NativeSessionID
	}
	prompt, err := w.prompt(ctx, job, att, session != "")
	if err != nil {
		return err
	}
	ev := e.Launch(ctx, job, d.DecisionID, d.ChosenProvider, prompt, session)
	w.log.Info("dispatching", "job", job.ID, "provider", d.ChosenProvider, "trigger", trigger, "kind", ev.Payload.Kind())
	return e.Ledger.Append(ctx, ev)
}

func (w *Watcher) resumable(job ledger.JobRow, att *ledger.AttemptRow) bool {
	if att == nil || att.EndedAt.IsZero() || job.NativeSessionID == "" {
		return false
	}
	p, ok := w.engine.Cfg.Providers[att.ProviderID]
	return ok && dispatch.CanResume(p.Harness) && !w.engine.Now().After(att.EndedAt.Add(w.engine.Cfg.General.FailoverWaitMax()))
}

func (w *Watcher) resetAt(ctx context.Context, tx *ledger.Tx, providerID string) (time.Time, error) {
	providers, err := w.engine.Snapshot(ctx, tx)
	if err != nil {
		return time.Time{}, err
	}
	reserve := w.engine.Cfg.General.ReserveUsedPct
	var latest time.Time
	for _, p := range providers {
		if p.ID != providerID {
			continue
		}
		for _, win := range p.Windows {
			if win.UsedPct == nil || *win.UsedPct < reserve {
				continue
			}
			if win.ResetsAt.IsZero() {
				return time.Time{}, nil
			}
			if win.ResetsAt.After(latest) {
				latest = win.ResetsAt
			}
		}
	}
	return latest, nil
}

func (w *Watcher) prompt(ctx context.Context, job ledger.JobRow, att *ledger.AttemptRow, native bool) (string, error) {
	data := dispatch.PromptData{JobID: job.ID, JobName: job.Name, Worktree: job.WorktreePath, BriefPath: job.BriefPath, ReportPath: job.ReportPath}
	if att == nil {
		data.Enrichment = w.engine.Enrich(ctx, job, false)
		return dispatch.InitialPrompt(data)
	}
	reason, err := w.failoverReason(ctx, job.ID, att)
	if err != nil {
		return "", err
	}
	cp, err := dispatch.ReadCheckpoint(ctx, job.WorktreePath)
	if err != nil {
		return "", err
	}
	data.FromProvider, data.SessionID, data.Reason, data.Checkpoint = att.ProviderID, job.NativeSessionID, reason, cp
	if native {
		return dispatch.ContinuePrompt(data)
	}
	role, _, err := w.engine.Cfg.Role(job.Role)
	if err != nil {
		return "", err
	}
	data.Enrichment = w.engine.Enrich(ctx, job, true)
	return dispatch.ResumePrompt(data, role.ResumePromptTemplate)
}

func (w *Watcher) Due(ctx context.Context) ([]string, error) {
	if w.probe == nil {
		return nil, nil
	}
	e := w.engine
	now := e.Now()
	wins, err := e.Ledger.Reader().Windows(ctx)
	if err != nil {
		return nil, err
	}
	sampled := map[string]time.Time{}
	for _, win := range wins {
		if win.SampledAt.After(sampled[win.ProviderID]) {
			sampled[win.ProviderID] = win.SampledAt
		}
	}
	var due []string
	for _, id := range e.Cfg.ProviderIDs() {
		prov := e.Cfg.Providers[id]
		fresh := now.Sub(sampled[id]) < time.Duration(prov.Sensor.PollIntervalSec)*time.Second
		if prov.IsEnabled() && !fresh && w.guard.Allow(id, now) {
			due = append(due, id)
		}
	}
	return due, nil
}

func (w *Watcher) Poll(ctx context.Context) error {
	due, err := w.Due(ctx)
	if err != nil || len(due) == 0 {
		return err
	}
	e := w.engine
	now := e.Now()
	type result struct {
		id      string
		samples []sensor.Sample
		err     error
	}
	results := make([]result, len(due))
	var wg sync.WaitGroup
	for i, id := range due {
		wg.Go(func() {
			s, err := w.probe.Sample(ctx, e.Cfg.Providers[id])
			results[i] = result{id: id, samples: s, err: err}
		})
	}
	wg.Wait()
	var evs []ledger.Event
	for _, r := range results {
		prov := e.Cfg.Providers[r.id]
		w.guard.Done(r.id, now, time.Duration(prov.Sensor.PollIntervalSec)*time.Second, errors.Is(r.err, sensor.ErrRateLimited))
		if r.err != nil {
			evs = append(evs, ledger.Event{ProviderID: r.id, Source: ledger.SourceSensor, Payload: ledger.ProbeFailed{SensorTier: prov.Sensor.Mode, Error: r.err.Error()}})
			continue
		}
		for _, s := range r.samples {
			evs = append(evs, ledger.Event{ProviderID: r.id, Source: ledger.SourceSensor, Payload: ledger.QuotaSampled{WindowName: s.Window, WindowKind: s.Kind, UsedPct: s.UsedPct, ResetsAt: s.ResetsAt, Confidence: s.Confidence, RawTokens: s.RawTokens}})
		}
	}
	return e.Ledger.Append(ctx, evs...)
}
