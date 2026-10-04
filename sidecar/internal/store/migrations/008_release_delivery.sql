-- Immutable manifest bytes are inserted once; only observed delivery state changes.
CREATE TABLE IF NOT EXISTS release_deliveries (
  release_run_id TEXT NOT NULL,
  group_id TEXT NOT NULL,
  manifest_json TEXT NOT NULL,
  manifest_sha256 TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'sealed',
  release_id INTEGER NOT NULL DEFAULT 0,
  url TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  sync_state TEXT NOT NULL DEFAULT 'unconfigured',
  sync_message TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (release_run_id,group_id),
  FOREIGN KEY (release_run_id) REFERENCES release_runs(id) ON DELETE CASCADE
);
