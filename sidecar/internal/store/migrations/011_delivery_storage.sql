-- Retention never removes release history, manifest bytes or remote URLs.
CREATE TABLE IF NOT EXISTS release_delivery_storage (
  release_run_id TEXT NOT NULL,
  group_id TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('cleaning','cleaned')),
  cleaned_at TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (release_run_id,group_id),
  FOREIGN KEY (release_run_id,group_id) REFERENCES release_deliveries(release_run_id,group_id) ON DELETE CASCADE
);
