-- 003_releases.sql: Git 版本发布配置、运行记录与日志

CREATE TABLE IF NOT EXISTS release_profiles (
    app_id               TEXT PRIMARY KEY,
    remote_name          TEXT NOT NULL DEFAULT 'origin',
    version_strategy     TEXT NOT NULL DEFAULT 'auto',
    pre_release_command  TEXT NOT NULL DEFAULT '',
    create_tag           INTEGER NOT NULL DEFAULT 1,
    version_mode         TEXT NOT NULL DEFAULT 'auto',
    updated_at           TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS release_runs (
    id                   TEXT PRIMARY KEY,
    app_id               TEXT NOT NULL,
    repo_root            TEXT NOT NULL,
    branch               TEXT NOT NULL,
    remote_name          TEXT NOT NULL,
    target_version       TEXT NOT NULL,
    tag_name             TEXT NOT NULL,
    create_tag           INTEGER NOT NULL DEFAULT 1,
    selected_targets_json TEXT NOT NULL DEFAULT '[]',
    execution_plan_json  TEXT NOT NULL DEFAULT '[]',
    status               TEXT NOT NULL DEFAULT 'queued',
    stage                TEXT NOT NULL DEFAULT 'preparing',
    commit_sha           TEXT NOT NULL DEFAULT '',
    status_fingerprint   TEXT NOT NULL DEFAULT '',
    error_code           TEXT NOT NULL DEFAULT '',
    error_message        TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    finished_at          TEXT,
    FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_release_runs_app ON release_runs(app_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_release_runs_status ON release_runs(status);

CREATE TABLE IF NOT EXISTS release_logs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    release_run_id  TEXT NOT NULL,
    ts              TEXT NOT NULL DEFAULT (datetime('now')),
    stream          TEXT NOT NULL DEFAULT 'event',
    text            TEXT NOT NULL,
    FOREIGN KEY (release_run_id) REFERENCES release_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_release_logs_run ON release_logs(release_run_id, id);
