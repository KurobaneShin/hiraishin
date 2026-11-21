package ledger

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/oklog/ulid/v2"

	_ "modernc.org/sqlite"
)

const TimeLayout = "2006-01-02T15:04:05.000000000Z"

//go:embed migrations/*.sql
var migrations embed.FS

type Ledger struct {
	db  *sql.DB
	now func() time.Time
}

func Open(ctx context.Context, path string, now func() time.Time) (*Ledger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create ledger dir: %w", err)
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	l := &Ledger{db: db, now: now}
	if err := l.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return l, nil
}

func (l *Ledger) Close() error { return l.db.Close() }

func (l *Ledger) migrate(ctx context.Context) error {
	if _, err := l.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := l.applyMigration(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

func (l *Ledger) applyMigration(ctx context.Context, name string) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback() }()
	var done int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&done); err != nil {
		return fmt.Errorf("check migration %s: %w", name, err)
	}
	if done > 0 {
		return nil
	}
	body, err := migrations.ReadFile(name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(name, applied_at) VALUES (?, ?)`, name, formatTime(l.now())); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	return tx.Commit()
}

func (l *Ledger) Append(ctx context.Context, events ...Event) error {
	return l.Update(ctx, func(context.Context, *Tx) ([]Event, error) { return events, nil })
}

func (l *Ledger) Update(ctx context.Context, fn func(context.Context, *Tx) ([]Event, error)) error {
	sqlTx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = sqlTx.Rollback() }()
	tx := &Tx{q: sqlTx, now: l.now}
	events, err := fn(ctx, tx)
	if err != nil {
		return err
	}
	for i := range events {
		if err := tx.append(ctx, &events[i]); err != nil {
			return err
		}
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (l *Ledger) Reader() *Tx { return &Tx{q: l.db, now: l.now} }

func NewID(prefix string) string { return prefix + "_" + ulid.Make().String() }

var ErrNotFound = errors.New("not found")

func formatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

func parseTime(s sql.NullString) time.Time {
	if !s.Valid || s.String == "" {
		return time.Time{}
	}
	t, err := time.Parse(TimeLayout, s.String)
	if err != nil {
		return time.Time{}
	}
	return t
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t)
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
