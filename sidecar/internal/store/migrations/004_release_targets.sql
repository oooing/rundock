-- 004_release_targets.sql: 每次发布所选目标及其独立执行状态

CREATE TABLE IF NOT EXISTS release_target_runs (
    release_run_id TEXT NOT NULL,
    target_id      TEXT NOT NULL,
    build          INTEGER NOT NULL DEFAULT 0,
    package        INTEGER NOT NULL DEFAULT 0,
    publish        INTEGER NOT NULL DEFAULT 0,
    deploy         INTEGER NOT NULL DEFAULT 0,
    check_done     INTEGER NOT NULL DEFAULT 0,
    build_done     INTEGER NOT NULL DEFAULT 0,
    package_done   INTEGER NOT NULL DEFAULT 0,
    publish_done   INTEGER NOT NULL DEFAULT 0,
    deploy_done    INTEGER NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT 'queued',
    stage          TEXT NOT NULL DEFAULT 'waiting',
    error_code     TEXT NOT NULL DEFAULT '',
    error_message  TEXT NOT NULL DEFAULT '',
    started_at     TEXT,
    finished_at    TEXT,
    PRIMARY KEY (release_run_id, target_id),
    FOREIGN KEY (release_run_id) REFERENCES release_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_release_target_runs_status
ON release_target_runs(release_run_id, status);

CREATE TABLE IF NOT EXISTS release_artifacts (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    release_run_id TEXT NOT NULL,
    target_id      TEXT NOT NULL,
    path           TEXT NOT NULL,
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    sha256         TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (release_run_id, target_id, path),
    FOREIGN KEY (release_run_id) REFERENCES release_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_release_artifacts_run
ON release_artifacts(release_run_id, target_id);
