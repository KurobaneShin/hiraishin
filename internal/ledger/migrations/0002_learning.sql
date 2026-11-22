CREATE TABLE learning_stats (
    provider_id        TEXT NOT NULL,
    role               TEXT NOT NULL,
    samples            INTEGER NOT NULL DEFAULT 0,
    completed_ok       INTEGER NOT NULL DEFAULT 0,
    avg_review_rounds  REAL NOT NULL DEFAULT 0.0,
    avg_build_failures REAL NOT NULL DEFAULT 0.0,
    avg_duration_min   REAL NOT NULL DEFAULT 0.0,
    ewma_quality       REAL NOT NULL DEFAULT 0.0,
    updated_at         TEXT NOT NULL,
    PRIMARY KEY (provider_id, role)
);
