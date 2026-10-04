-- Pure local builds keep their public output manifests separate from GitHub delivery.
CREATE TABLE IF NOT EXISTS local_build_artifacts (
  release_run_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  manifest_json TEXT NOT NULL,
  manifest_sha256 TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (release_run_id,target_id),
  FOREIGN KEY (release_run_id) REFERENCES release_runs(id) ON DELETE CASCADE
);

-- A lost response must not execute the same local build twice, including after restart.
CREATE UNIQUE INDEX IF NOT EXISTS idx_local_build_request
  ON release_runs(app_id,json_extract(execution_plan_json,'$.localBuildRequestId'))
  WHERE json_extract(execution_plan_json,'$.intent')='build-only';
