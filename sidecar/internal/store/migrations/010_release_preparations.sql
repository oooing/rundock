CREATE TABLE IF NOT EXISTS release_preparations (
 id TEXT PRIMARY KEY,
 app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL,
 snapshot_json TEXT NOT NULL,
 updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS release_preparations_app ON release_preparations(app_id, updated_at);
