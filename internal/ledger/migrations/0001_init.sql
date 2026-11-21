CREATE TABLE events (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id     TEXT NOT NULL UNIQUE,
    ts           TEXT NOT NULL,
    kind         TEXT NOT NULL,
    provider_id  TEXT,
    job_id       TEXT,
    source       TEXT NOT NULL,
    payload_json TEXT NOT NULL
);
CREATE INDEX idx_events_ts ON events(ts);
CREATE INDEX idx_events_provider_ts ON events(provider_id, ts);
CREATE INDEX idx_events_job_ts ON events(job_id, ts);
CREATE INDEX idx_events_kind_ts ON events(kind, ts);

CREATE TABLE providers (
    provider_id    TEXT PRIMARY KEY,
    harness        TEXT NOT NULL,
    state          TEXT NOT NULL,
    active_jobs    INTEGER NOT NULL DEFAULT 0,
    max_concurrent INTEGER NOT NULL,
    cooldown_until TEXT,
    last_error     TEXT,
    updated_at     TEXT NOT NULL
);

CREATE TABLE provider_windows (
    provider_id      TEXT NOT NULL REFERENCES providers(provider_id),
    window_name      TEXT NOT NULL,
    window_kind      TEXT NOT NULL,
    used_pct         REAL,
    remaining_pct    REAL,
    resets_at        TEXT,
    burn_rate_pct_hr REAL,
    confidence       TEXT NOT NULL,
    sampled_at       TEXT NOT NULL,
    PRIMARY KEY (provider_id, window_name)
);

CREATE TABLE jobs (
    job_id            TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    role              TEXT NOT NULL,
    state             TEXT NOT NULL,
    worktree_path     TEXT NOT NULL,
    branch            TEXT NOT NULL,
    brief_path        TEXT NOT NULL,
    report_path       TEXT,
    current_provider  TEXT REFERENCES providers(provider_id),
    terminal_adapter  TEXT NOT NULL,
    terminal_handle   TEXT,
    native_session_id TEXT,
    pinned_provider   TEXT,
    never_switch      INTEGER NOT NULL DEFAULT 0,
    failover_count    INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);
CREATE INDEX idx_jobs_state_created ON jobs(state, created_at);

CREATE TABLE decisions (
    decision_id     TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL REFERENCES jobs(job_id),
    trigger         TEXT NOT NULL,
    profile         TEXT NOT NULL,
    chosen_provider TEXT,
    candidates_json TEXT NOT NULL,
    summary         TEXT NOT NULL,
    created_at      TEXT NOT NULL
);
CREATE INDEX idx_decisions_job_created ON decisions(job_id, created_at);

CREATE TABLE job_attempts (
    attempt_id   TEXT PRIMARY KEY,
    job_id       TEXT NOT NULL REFERENCES jobs(job_id),
    attempt_num  INTEGER NOT NULL,
    provider_id  TEXT NOT NULL,
    decision_id  TEXT NOT NULL REFERENCES decisions(decision_id),
    attempt_dir  TEXT NOT NULL,
    start_commit TEXT,
    end_commit   TEXT,
    exit_reason  TEXT,
    started_at   TEXT NOT NULL,
    ended_at     TEXT
);
CREATE INDEX idx_attempts_job_num ON job_attempts(job_id, attempt_num);

CREATE TABLE runtime_state (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE prefers (
    provider_id TEXT NOT NULL,
    role        TEXT NOT NULL,
    expires_at  TEXT,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (provider_id, role)
);
