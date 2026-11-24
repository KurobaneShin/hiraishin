package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const (
	StateAvailable   = "AVAILABLE"
	StateDegraded    = "DEGRADED"
	StateDraining    = "DRAINING"
	StatePaused      = "PAUSED"
	StateExhausted   = "EXHAUSTED"
	StateAuthError   = "AUTH_ERROR"
	StateUnavailable = "UNAVAILABLE"
)

const (
	JobQueuedState       = "QUEUED"
	JobDispatchingState  = "DISPATCHING"
	JobRunningState      = "RUNNING"
	JobFailingOverState  = "FAILING_OVER"
	JobWaitingQuotaState = "WAITING_QUOTA"
	JobCompletedState    = "COMPLETED"
	JobFailedState       = "FAILED"
	JobCancelledState    = "CANCELLED"
)

const (
	ConfidenceExact     = "exact"
	ConfidenceEstimated = "estimated"
	ConfidenceInferred  = "inferred"
)

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Tx struct {
	q   querier
	now func() time.Time
}

func (t *Tx) Now() time.Time { return t.now() }

func (t *Tx) append(ctx context.Context, ev *Event) error {
	if ev.Payload == nil {
		return fmt.Errorf("append: event without payload")
	}
	if ev.ID == "" {
		ev.ID = NewID("evt")
	}
	if ev.TS.IsZero() {
		ev.TS = t.now()
	}
	body, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", ev.Payload.Kind(), err)
	}
	if _, err := t.q.ExecContext(ctx,
		`INSERT INTO events(event_id, ts, kind, provider_id, job_id, source, payload_json) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ev.ID, formatTime(ev.TS), string(ev.Payload.Kind()), nullString(ev.ProviderID), nullString(ev.JobID), ev.Source, string(body),
	); err != nil {
		return fmt.Errorf("insert %s: %w", ev.Payload.Kind(), err)
	}
	if err := t.project(ctx, ev); err != nil {
		return fmt.Errorf("project %s: %w", ev.Payload.Kind(), err)
	}
	return nil
}

func (t *Tx) exec(ctx context.Context, query string, args ...any) error {
	_, err := t.q.ExecContext(ctx, query, args...)
	return err
}

func (t *Tx) project(ctx context.Context, ev *Event) error {
	ts := formatTime(ev.TS)
	switch p := ev.Payload.(type) {
	case ProviderConfigured:
		return t.exec(ctx, `INSERT INTO providers(provider_id, harness, state, max_concurrent, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(provider_id) DO UPDATE SET harness = excluded.harness, max_concurrent = excluded.max_concurrent, updated_at = excluded.updated_at`,
			ev.ProviderID, p.Harness, StateAvailable, p.MaxConcurrent, ts)
	case QuotaSampled:
		return t.upsertWindow(ctx, ev.ProviderID, p.WindowName, p.WindowKind, p.UsedPct, p.ResetsAt, p.Confidence, ts)
	case LimitHit:
		full := 100.0
		if err := t.upsertWindow(ctx, ev.ProviderID, p.WindowName, p.WindowKind, &full, p.EstimatedResetAt, ConfidenceInferred, ts); err != nil {
			return err
		}
		return t.exec(ctx, `UPDATE providers SET state = CASE WHEN state IN (?, ?) THEN state ELSE ? END, last_error = ?, updated_at = ? WHERE provider_id = ?`,
			StatePaused, StateDraining, StateExhausted, p.Excerpt, ts, ev.ProviderID)
	case AuthFailed:
		return t.exec(ctx, `UPDATE providers SET state = CASE WHEN state IN (?, ?) THEN state ELSE ? END, last_error = ?, updated_at = ? WHERE provider_id = ?`,
			StatePaused, StateDraining, StateAuthError, p.Excerpt, ts, ev.ProviderID)
	case Degraded:
		return t.exec(ctx, `UPDATE providers SET state = CASE WHEN state IN (?, ?) THEN ? ELSE state END, cooldown_until = ?, last_error = ?, updated_at = ? WHERE provider_id = ?`,
			StateAvailable, StateDegraded, StateDegraded, nullTime(p.CooldownUntil), p.Excerpt, ts, ev.ProviderID)
	case Recovered:
		return t.exec(ctx, `UPDATE providers SET state = CASE WHEN state = ? THEN ? ELSE state END, cooldown_until = NULL, updated_at = ? WHERE provider_id = ?`,
			StateDegraded, StateAvailable, ts, ev.ProviderID)
	case WindowReset:
		zero := 0.0
		if err := t.upsertWindow(ctx, ev.ProviderID, p.WindowName, "", &zero, p.ResetsAtNext, ConfidenceInferred, ts); err != nil {
			return err
		}
		return t.exec(ctx, `UPDATE providers SET state = ?, updated_at = ? WHERE provider_id = ? AND state = ?
			AND NOT EXISTS (SELECT 1 FROM provider_windows w WHERE w.provider_id = providers.provider_id AND w.used_pct >= 100 AND (w.resets_at IS NULL OR w.resets_at > ?))`,
			StateAvailable, ts, ev.ProviderID, StateExhausted, ts)
	case Paused:
		return t.exec(ctx, `UPDATE providers SET state = ?, updated_at = ? WHERE provider_id = ?`, StatePaused, ts, ev.ProviderID)
	case Draining:
		return t.exec(ctx, `UPDATE providers SET state = CASE WHEN active_jobs = 0 THEN ? ELSE ? END, updated_at = ? WHERE provider_id = ?`,
			StatePaused, StateDraining, ts, ev.ProviderID)
	case Resumed:
		return t.exec(ctx, `UPDATE providers SET cooldown_until = NULL, updated_at = ?, state = CASE WHEN EXISTS (
			SELECT 1 FROM provider_windows w WHERE w.provider_id = providers.provider_id AND w.used_pct >= 100 AND (w.resets_at IS NULL OR w.resets_at > ?))
			THEN ? ELSE ? END WHERE provider_id = ?`, ts, ts, StateExhausted, StateAvailable, ev.ProviderID)
	case ProfileSet:
		return t.exec(ctx, `INSERT INTO runtime_state(key, value, updated_at) VALUES ('profile', ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, p.Profile, ts)
	case PreferSet:
		return t.exec(ctx, `INSERT INTO prefers(provider_id, role, expires_at, created_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(provider_id, role) DO UPDATE SET expires_at = excluded.expires_at, created_at = excluded.created_at`,
			ev.ProviderID, p.Role, nullTime(p.ExpiresAt), ts)
	case PreferCleared:
		return t.exec(ctx, `DELETE FROM prefers WHERE provider_id = ? AND role = ?`, ev.ProviderID, p.Role)
	case DecisionMade:
		return t.projectDecision(ctx, ev, p, ts)
	case JobQueued:
		return t.exec(ctx, `INSERT INTO jobs(job_id, name, role, state, worktree_path, branch, brief_path, report_path, terminal_adapter, pinned_provider, never_switch, backlog, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ev.JobID, p.Name, p.Role, JobQueuedState, p.WorktreePath, p.Branch, p.BriefPath, nullString(p.ReportPath), p.TerminalAdapter, nullString(p.PinnedProvider), boolInt(p.NeverSwitch), boolInt(p.Backlog), ts, ts)
	case JobDispatched:
		if err := t.exec(ctx, `INSERT INTO job_attempts(attempt_id, job_id, attempt_num, provider_id, decision_id, attempt_dir, start_commit, started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			p.AttemptID, ev.JobID, p.AttemptNum, p.ProviderID, p.DecisionID, p.AttemptDir, nullString(p.StartCommit), ts); err != nil {
			return err
		}
		return t.exec(ctx, `UPDATE jobs SET state = ?, current_provider = ?, terminal_adapter = ?, terminal_handle = ?, native_session_id = NULL, updated_at = ? WHERE job_id = ?`,
			JobRunningState, p.ProviderID, p.TerminalAdapter, p.TerminalHandle, ts, ev.JobID)
	case JobSpawnFailed:
		next := JobFailedState
		if p.Retryable {
			next = JobQueuedState
		}
		return t.releaseAndSet(ctx, ev.JobID, next, "", "", "", ts)
	case SessionDetected:
		return t.exec(ctx, `UPDATE jobs SET native_session_id = ?, updated_at = ? WHERE job_id = ?`, p.NativeSessionID, ts, ev.JobID)
	case FailoverStarted:
		if err := t.releaseAndSet(ctx, ev.JobID, JobFailingOverState, p.AttemptID, p.ExitReason, p.HeadCommit, ts); err != nil {
			return err
		}
		return t.exec(ctx, `UPDATE jobs SET failover_count = failover_count + 1 WHERE job_id = ?`, ev.JobID)
	case WaitingQuota:
		return t.releaseAndSet(ctx, ev.JobID, JobWaitingQuotaState, p.AttemptID, p.ExitReason, p.EndCommit, ts)
	case JobCompleted:
		return t.releaseAndSet(ctx, ev.JobID, JobCompletedState, p.AttemptID, "completed", p.EndCommit, ts)
	case JobFailed:
		return t.releaseAndSet(ctx, ev.JobID, JobFailedState, p.AttemptID, "process_error", p.EndCommit, ts)
	case JobCancelled:
		return t.releaseAndSet(ctx, ev.JobID, JobCancelledState, p.AttemptID, "user_stop", "", ts)
	case JobPinned:
		return t.exec(ctx, `UPDATE jobs SET pinned_provider = ?, never_switch = 1, updated_at = ? WHERE job_id = ?`, p.Provider, ts, ev.JobID)
	case JobUnpinned:
		return t.exec(ctx, `UPDATE jobs SET pinned_provider = NULL, never_switch = ?, updated_at = ? WHERE job_id = ?`, boolInt(p.NeverSwitch), ts, ev.JobID)
	case OutcomeRecorded:
		return t.projectOutcome(ctx, ev.ProviderID, p, ts)
	case LearnFrozen:
		return t.exec(ctx, `INSERT INTO runtime_state(key, value, updated_at) VALUES ('learning_frozen', '1', ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, ts)
	case LearnUnfrozen:
		return t.exec(ctx, `DELETE FROM runtime_state WHERE key = 'learning_frozen'`)
	case LearnReset:
		return t.exec(ctx, `DELETE FROM learning_stats`)
	case ProbeFailed, JobBurnMeasured:
		return nil
	default:
		return fmt.Errorf("no projection for %T", p)
	}
}

func (t *Tx) projectDecision(ctx context.Context, ev *Event, p DecisionMade, ts string) error {
	blocked, err := json.Marshal(p.BlockedProviders)
	if err != nil {
		return err
	}
	weights := string(p.Weights)
	if weights == "" {
		weights = "{}"
	}
	if err := t.exec(ctx, `INSERT INTO decisions(decision_id, job_id, trigger, profile, chosen_provider, role, min_quality_tier, weights_json, candidates_json, summary, earliest_reset_at, blocked_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.DecisionID, ev.JobID, p.Trigger, p.Profile, nullString(p.ChosenProvider), p.Role, p.MinQualityTier, weights, string(p.Candidates), p.Summary, nullTime(p.EarliestResetAt), string(blocked), ts); err != nil {
		return err
	}
	if p.ChosenProvider == "" || p.Trigger == "dry_run" {
		return nil
	}
	if err := t.exec(ctx, `UPDATE providers SET active_jobs = active_jobs + 1, updated_at = ? WHERE provider_id = ?`, ts, p.ChosenProvider); err != nil {
		return err
	}
	return t.exec(ctx, `UPDATE jobs SET state = ?, current_provider = ?, updated_at = ? WHERE job_id = ?`, JobDispatchingState, p.ChosenProvider, ts, ev.JobID)
}

func (t *Tx) projectOutcome(ctx context.Context, providerID string, p OutcomeRecorded, ts string) error {
	if p.Frozen {
		return nil
	}
	a := p.Alpha
	return t.exec(ctx, `INSERT INTO learning_stats(provider_id, role, samples, completed_ok, avg_review_rounds, avg_build_failures, avg_duration_min, ewma_quality, updated_at)
		VALUES (?, ?, 1, ?, ?, ?, MAX(?, 0), ?, ?)
		ON CONFLICT(provider_id, role) DO UPDATE SET
			samples = samples + 1,
			completed_ok = completed_ok + excluded.completed_ok,
			avg_review_rounds = (1 - ?) * avg_review_rounds + ? * excluded.avg_review_rounds,
			avg_build_failures = (1 - ?) * avg_build_failures + ? * excluded.avg_build_failures,
			avg_duration_min = CASE WHEN excluded.avg_duration_min <= 0 THEN avg_duration_min
				WHEN avg_duration_min <= 0 THEN excluded.avg_duration_min
				ELSE (1 - ?) * avg_duration_min + ? * excluded.avg_duration_min END,
			ewma_quality = (1 - ?) * ewma_quality + ? * excluded.ewma_quality,
			updated_at = excluded.updated_at`,
		providerID, p.Role, boolInt(p.Success), p.ReviewRounds, p.BuildFailures, p.DurationMin, p.Quality, ts, a, a, a, a, a, a, a, a)
}

func (t *Tx) releaseAndSet(ctx context.Context, jobID, next, attemptID, exitReason, endCommit, ts string) error {
	var state string
	var provider sql.NullString
	err := t.q.QueryRowContext(ctx, `SELECT state, current_provider FROM jobs WHERE job_id = ?`, jobID).Scan(&state, &provider)
	if err != nil {
		return fmt.Errorf("load job %s: %w", jobID, err)
	}
	if provider.Valid && (state == JobDispatchingState || state == JobRunningState) {
		if err := t.exec(ctx, `UPDATE providers SET active_jobs = MAX(active_jobs - 1, 0), updated_at = ? WHERE provider_id = ?`, ts, provider.String); err != nil {
			return err
		}
		if err := t.exec(ctx, `UPDATE providers SET state = ? WHERE provider_id = ? AND state = ? AND active_jobs = 0`, StatePaused, provider.String, StateDraining); err != nil {
			return err
		}
	}
	if attemptID != "" {
		if err := t.exec(ctx, `UPDATE job_attempts SET ended_at = ?, exit_reason = ?, end_commit = ? WHERE attempt_id = ? AND ended_at IS NULL`,
			ts, exitReason, nullString(endCommit), attemptID); err != nil {
			return err
		}
	}
	return t.exec(ctx, `UPDATE jobs SET state = ?, terminal_handle = CASE WHEN ? IN (?, ?) THEN terminal_handle ELSE NULL END, updated_at = ? WHERE job_id = ?`,
		next, next, JobDispatchingState, JobRunningState, ts, jobID)
}

func (t *Tx) upsertWindow(ctx context.Context, providerID, name, kind string, used *float64, resetsAt time.Time, confidence, ts string) error {
	var remaining any
	if used != nil {
		remaining = 100 - *used
	}
	var usedArg any
	if used != nil {
		usedArg = *used
	}
	return t.exec(ctx, `INSERT INTO provider_windows(provider_id, window_name, window_kind, used_pct, remaining_pct, resets_at, confidence, sampled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_id, window_name) DO UPDATE SET
			window_kind = CASE WHEN excluded.window_kind = '' THEN provider_windows.window_kind ELSE excluded.window_kind END,
			used_pct = excluded.used_pct, remaining_pct = excluded.remaining_pct, resets_at = excluded.resets_at,
			confidence = excluded.confidence, sampled_at = excluded.sampled_at`,
		providerID, name, kind, usedArg, remaining, nullTime(resetsAt), confidence, ts)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
