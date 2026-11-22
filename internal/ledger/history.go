package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type FailoverRow struct {
	TS           time.Time
	JobID        string
	JobName      string
	FromProvider string
	Reason       string
}

type QuotaSampleRow struct {
	TS         time.Time
	ProviderID string
	WindowName string
	UsedPct    float64
}

func (t *Tx) RecentFailovers(ctx context.Context, limit int) ([]FailoverRow, error) {
	rows, err := t.q.QueryContext(ctx, `SELECT e.ts, e.job_id, j.name, e.payload_json FROM events e JOIN jobs j ON j.job_id = e.job_id
		WHERE e.kind = ? ORDER BY e.ts DESC, e.seq DESC LIMIT ?`, string(KindFailoverStarted), limit)
	if err != nil {
		return nil, fmt.Errorf("query failovers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []FailoverRow
	for rows.Next() {
		var f FailoverRow
		var ts sql.NullString
		var body string
		if err := rows.Scan(&ts, &f.JobID, &f.JobName, &body); err != nil {
			return nil, fmt.Errorf("scan failover: %w", err)
		}
		var p FailoverStarted
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			return nil, fmt.Errorf("decode failover: %w", err)
		}
		f.TS, f.FromProvider, f.Reason = parseTime(ts), p.FromProvider, p.Reason
		out = append(out, f)
	}
	return out, rows.Err()
}

func (t *Tx) QuotaSamples(ctx context.Context, since time.Time) ([]QuotaSampleRow, error) {
	rows, err := t.q.QueryContext(ctx, `SELECT ts, provider_id, json_extract(payload_json, '$.window_name'), json_extract(payload_json, '$.used_pct') FROM events
		WHERE kind = ? AND ts >= ? AND json_extract(payload_json, '$.used_pct') IS NOT NULL ORDER BY ts, seq`, string(KindQuotaSampled), formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("query quota samples: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []QuotaSampleRow
	for rows.Next() {
		var s QuotaSampleRow
		var ts sql.NullString
		if err := rows.Scan(&ts, &s.ProviderID, &s.WindowName, &s.UsedPct); err != nil {
			return nil, fmt.Errorf("scan quota sample: %w", err)
		}
		s.TS = parseTime(ts)
		out = append(out, s)
	}
	return out, rows.Err()
}
