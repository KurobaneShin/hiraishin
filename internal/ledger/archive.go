package ledger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (l *Ledger) Archive(ctx context.Context, before time.Time, archivePath string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o700); err != nil {
		return 0, fmt.Errorf("create archive dir: %w", err)
	}
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire conn: %w", err)
	}
	defer func() { _ = conn.Close() }()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin archive: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	cutoff := formatTime(before)
	if _, err := tx.ExecContext(ctx, `ATTACH DATABASE ? AS archive`, archivePath); err != nil {
		return 0, fmt.Errorf("attach archive: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS archive.events (
		seq          INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id     TEXT NOT NULL UNIQUE,
		ts           TEXT NOT NULL,
		kind         TEXT NOT NULL,
		provider_id  TEXT,
		job_id       TEXT,
		source       TEXT NOT NULL,
		payload_json TEXT NOT NULL
	)`); err != nil {
		return 0, fmt.Errorf("create archive events table: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO archive.events
		SELECT * FROM events
		WHERE ts < ?
		AND (job_id IS NULL OR job_id IN (
			SELECT job_id FROM jobs
			WHERE state IN (?, ?, ?) AND updated_at < ?
		))`,
		cutoff, JobCompletedState, JobFailedState, JobCancelledState, cutoff)
	if err != nil {
		return 0, fmt.Errorf("copy events: %w", err)
	}
	copied, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("copied rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM events
		WHERE ts < ?
		AND (job_id IS NULL OR job_id IN (
			SELECT job_id FROM jobs
			WHERE state IN (?, ?, ?) AND updated_at < ?
		))`,
		cutoff, JobCompletedState, JobFailedState, JobCancelledState, cutoff); err != nil {
		return 0, fmt.Errorf("delete events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit archive: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `DETACH DATABASE archive`); err != nil {
		return 0, fmt.Errorf("detach archive: %w", err)
	}
	return copied, nil
}
