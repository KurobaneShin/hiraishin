package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ProviderRow struct {
	ID            string
	Harness       string
	State         string
	ActiveJobs    int
	MaxConcurrent int
	CooldownUntil time.Time
	LastError     string
	UpdatedAt     time.Time
}

type WindowRow struct {
	ProviderID string
	Name       string
	Kind       string
	UsedPct    *float64
	ResetsAt   time.Time
	Confidence string
	SampledAt  time.Time
}

type JobRow struct {
	ID              string
	Name            string
	Role            string
	State           string
	WorktreePath    string
	Branch          string
	BriefPath       string
	ReportPath      string
	CurrentProvider string
	TerminalAdapter string
	TerminalHandle  string
	NativeSessionID string
	PinnedProvider  string
	NeverSwitch     bool
	FailoverCount   int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type DecisionRow struct {
	ID              string
	JobID           string
	Trigger         string
	Profile         string
	ChosenProvider  string
	Role            string
	MinQualityTier  int
	WeightsJSON     string
	CandidatesJSON  string
	Summary         string
	EarliestResetAt time.Time
	BlockedJSON     string
	CreatedAt       time.Time
}

type AttemptRow struct {
	ID          string
	JobID       string
	Num         int
	ProviderID  string
	DecisionID  string
	Dir         string
	StartCommit string
	EndCommit   string
	ExitReason  string
	StartedAt   time.Time
	EndedAt     time.Time
}

type PreferRow struct {
	ProviderID string
	Role       string
	ExpiresAt  time.Time
}

type EventRow struct {
	Seq        int64
	ID         string
	TS         time.Time
	Kind       Kind
	ProviderID string
	JobID      string
	Source     string
	Payload    string
}

func (t *Tx) Providers(ctx context.Context) ([]ProviderRow, error) {
	rows, err := t.q.QueryContext(ctx, `SELECT provider_id, harness, state, active_jobs, max_concurrent, cooldown_until, last_error, updated_at FROM providers ORDER BY provider_id`)
	if err != nil {
		return nil, fmt.Errorf("query providers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ProviderRow
	for rows.Next() {
		var p ProviderRow
		var cooldown, lastErr, updated sql.NullString
		if err := rows.Scan(&p.ID, &p.Harness, &p.State, &p.ActiveJobs, &p.MaxConcurrent, &cooldown, &lastErr, &updated); err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		p.CooldownUntil, p.LastError, p.UpdatedAt = parseTime(cooldown), lastErr.String, parseTime(updated)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (t *Tx) Provider(ctx context.Context, id string) (ProviderRow, error) {
	all, err := t.Providers(ctx)
	if err != nil {
		return ProviderRow{}, err
	}
	for _, p := range all {
		if p.ID == id {
			return p, nil
		}
	}
	return ProviderRow{}, fmt.Errorf("provider %q: %w", id, ErrNotFound)
}

func (t *Tx) Windows(ctx context.Context) ([]WindowRow, error) {
	rows, err := t.q.QueryContext(ctx, `SELECT provider_id, window_name, window_kind, used_pct, resets_at, confidence, sampled_at FROM provider_windows ORDER BY provider_id, window_name`)
	if err != nil {
		return nil, fmt.Errorf("query windows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []WindowRow
	for rows.Next() {
		var w WindowRow
		var used sql.NullFloat64
		var resets, sampled sql.NullString
		if err := rows.Scan(&w.ProviderID, &w.Name, &w.Kind, &used, &resets, &w.Confidence, &sampled); err != nil {
			return nil, fmt.Errorf("scan window: %w", err)
		}
		if used.Valid {
			v := used.Float64
			w.UsedPct = &v
		}
		w.ResetsAt, w.SampledAt = parseTime(resets), parseTime(sampled)
		out = append(out, w)
	}
	return out, rows.Err()
}

const jobColumns = `job_id, name, role, state, worktree_path, branch, brief_path, report_path, current_provider, terminal_adapter, terminal_handle, native_session_id, pinned_provider, never_switch, failover_count, created_at, updated_at`

func scanJob(s interface{ Scan(...any) error }) (JobRow, error) {
	var j JobRow
	var report, current, handle, session, pinned, created, updated sql.NullString
	var never int
	err := s.Scan(&j.ID, &j.Name, &j.Role, &j.State, &j.WorktreePath, &j.Branch, &j.BriefPath, &report, &current, &j.TerminalAdapter, &handle, &session, &pinned, &never, &j.FailoverCount, &created, &updated)
	if err != nil {
		return JobRow{}, err
	}
	j.ReportPath, j.CurrentProvider, j.TerminalHandle, j.NativeSessionID, j.PinnedProvider = report.String, current.String, handle.String, session.String, pinned.String
	j.NeverSwitch = never != 0
	j.CreatedAt, j.UpdatedAt = parseTime(created), parseTime(updated)
	return j, nil
}

func (t *Tx) Job(ctx context.Context, id string) (JobRow, error) {
	j, err := scanJob(t.q.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE job_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return JobRow{}, fmt.Errorf("job %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return JobRow{}, fmt.Errorf("load job %q: %w", id, err)
	}
	return j, nil
}

func (t *Tx) Jobs(ctx context.Context, states ...string) ([]JobRow, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs`
	args := make([]any, len(states))
	if len(states) > 0 {
		query += ` WHERE state IN (?` + strings.Repeat(", ?", len(states)-1) + `)`
		for i, s := range states {
			args[i] = s
		}
	}
	rows, err := t.q.QueryContext(ctx, query+` ORDER BY created_at, job_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("query jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []JobRow
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

const decisionColumns = `decision_id, job_id, trigger, profile, chosen_provider, role, min_quality_tier, weights_json, candidates_json, summary, earliest_reset_at, blocked_json, created_at`

func scanDecision(s interface{ Scan(...any) error }) (DecisionRow, error) {
	var d DecisionRow
	var chosen, earliest, created sql.NullString
	if err := s.Scan(&d.ID, &d.JobID, &d.Trigger, &d.Profile, &chosen, &d.Role, &d.MinQualityTier, &d.WeightsJSON, &d.CandidatesJSON, &d.Summary, &earliest, &d.BlockedJSON, &created); err != nil {
		return DecisionRow{}, err
	}
	d.ChosenProvider, d.EarliestResetAt, d.CreatedAt = chosen.String, parseTime(earliest), parseTime(created)
	return d, nil
}

func (t *Tx) Decision(ctx context.Context, id string) (DecisionRow, error) {
	d, err := scanDecision(t.q.QueryRowContext(ctx, `SELECT `+decisionColumns+` FROM decisions WHERE decision_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return DecisionRow{}, fmt.Errorf("decision %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return DecisionRow{}, fmt.Errorf("load decision %q: %w", id, err)
	}
	return d, nil
}

func (t *Tx) Decisions(ctx context.Context, jobID string) ([]DecisionRow, error) {
	rows, err := t.q.QueryContext(ctx, `SELECT `+decisionColumns+` FROM decisions WHERE job_id = ? ORDER BY created_at, decision_id`, jobID)
	if err != nil {
		return nil, fmt.Errorf("query decisions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DecisionRow
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, fmt.Errorf("scan decision: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (t *Tx) Attempts(ctx context.Context, jobID string) ([]AttemptRow, error) {
	rows, err := t.q.QueryContext(ctx, `SELECT attempt_id, job_id, attempt_num, provider_id, decision_id, attempt_dir, start_commit, end_commit, exit_reason, started_at, ended_at
		FROM job_attempts WHERE job_id = ? ORDER BY attempt_num`, jobID)
	if err != nil {
		return nil, fmt.Errorf("query attempts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AttemptRow
	for rows.Next() {
		var a AttemptRow
		var start, end, reason, started, ended sql.NullString
		if err := rows.Scan(&a.ID, &a.JobID, &a.Num, &a.ProviderID, &a.DecisionID, &a.Dir, &start, &end, &reason, &started, &ended); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		a.StartCommit, a.EndCommit, a.ExitReason = start.String, end.String, reason.String
		a.StartedAt, a.EndedAt = parseTime(started), parseTime(ended)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (t *Tx) Profile(ctx context.Context) (string, error) {
	var v string
	err := t.q.QueryRowContext(ctx, `SELECT value FROM runtime_state WHERE key = 'profile'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load profile: %w", err)
	}
	return v, nil
}

func (t *Tx) Prefers(ctx context.Context, now time.Time) ([]PreferRow, error) {
	rows, err := t.q.QueryContext(ctx, `SELECT provider_id, role, expires_at FROM prefers WHERE expires_at IS NULL OR expires_at > ? ORDER BY provider_id, role`, formatTime(now))
	if err != nil {
		return nil, fmt.Errorf("query prefers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []PreferRow
	for rows.Next() {
		var p PreferRow
		var exp sql.NullString
		if err := rows.Scan(&p.ProviderID, &p.Role, &exp); err != nil {
			return nil, fmt.Errorf("scan prefer: %w", err)
		}
		p.ExpiresAt = parseTime(exp)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (t *Tx) JobEvents(ctx context.Context, jobID string) ([]EventRow, error) {
	rows, err := t.q.QueryContext(ctx, `SELECT seq, event_id, ts, kind, provider_id, job_id, source, payload_json FROM events WHERE job_id = ? ORDER BY seq`, jobID)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		var ts, provider, job sql.NullString
		var kind string
		if err := rows.Scan(&e.Seq, &e.ID, &ts, &kind, &provider, &job, &e.Source, &e.Payload); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		e.TS, e.Kind, e.ProviderID, e.JobID = parseTime(ts), Kind(kind), provider.String, job.String
		out = append(out, e)
	}
	return out, rows.Err()
}
