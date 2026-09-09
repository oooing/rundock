CREATE TABLE IF NOT EXISTS cloud_builds (
    release_run_id TEXT PRIMARY KEY REFERENCES release_runs(id) ON DELETE CASCADE,
    snapshot_json TEXT NOT NULL,
    acknowledged_key TEXT NOT NULL DEFAULT ''
);
