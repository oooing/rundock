package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
)

// One checked candidate identifies one submitted operation. Replaying a lost
// HTTP response returns that operation without committing/building a second time.
func (s *Store) ReleaseForCandidate(appID, candidateID string) (*ReleaseRun, error) {
	if candidateID == "" {
		return nil, nil
	}
	var id string
	err := s.db.QueryRow(`SELECT id FROM release_runs WHERE app_id=? AND json_extract(execution_plan_json,'$.candidateId')=? ORDER BY created_at DESC LIMIT 1`, appID, candidateID).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetReleaseRun(id)
}

type ReleaseDelivery struct {
	RunID                   string          `json:"runId"`
	GroupID                 string          `json:"groupId"`
	Manifest                json.RawMessage `json:"-"`
	ManifestSHA256          string          `json:"manifestSha256"`
	State                   string          `json:"state"`
	ReleaseID               int64           `json:"releaseId"`
	URL                     string          `json:"url"`
	ErrorCode               string          `json:"errorCode"`
	ErrorMessage            string          `json:"errorMessage"`
	SyncState               string          `json:"syncState"`
	SyncMessage             string          `json:"syncMessage"`
	UpdatedAt               string          `json:"updatedAt"`
	LocalArtifactsState     string          `json:"localArtifactsState"`
	LocalArtifactsCleanedAt string          `json:"localArtifactsCleanedAt,omitempty"`
}

func (s *Store) ReleaseDataDir() string { return filepath.Join(s.dataDir, "releases") }

func (s *Store) SealReleaseDelivery(runID, groupID string, manifest []byte) error {
	sum := sha256.Sum256(manifest)
	digest := hex.EncodeToString(sum[:])
	_, err := s.db.Exec(`INSERT INTO release_deliveries(release_run_id,group_id,manifest_json,manifest_sha256)
	 VALUES(?,?,?,?) ON CONFLICT(release_run_id,group_id) DO NOTHING`, runID, groupID, string(manifest), digest)
	if err != nil {
		return err
	}
	var actual string
	if err = s.db.QueryRow(`SELECT manifest_sha256 FROM release_deliveries WHERE release_run_id=? AND group_id=?`, runID, groupID).Scan(&actual); err != nil {
		return err
	}
	if actual != digest {
		return fmt.Errorf("sealed_manifest_conflict: 已封存的产物清单不能修改")
	}
	return nil
}

func (s *Store) ReleaseDeliveries(runID string) ([]*ReleaseDelivery, error) {
	rows, err := s.db.Query(`SELECT d.release_run_id,d.group_id,d.manifest_json,d.manifest_sha256,d.state,d.release_id,d.url,d.error_code,d.error_message,d.sync_state,d.sync_message,d.updated_at,COALESCE(s.state,'retained'),COALESCE(s.cleaned_at,'')
	 FROM release_deliveries d LEFT JOIN release_delivery_storage s ON s.release_run_id=d.release_run_id AND s.group_id=d.group_id WHERE d.release_run_id=? ORDER BY d.group_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ReleaseDelivery{}
	for rows.Next() {
		d := &ReleaseDelivery{}
		var raw string
		if err = rows.Scan(&d.RunID, &d.GroupID, &raw, &d.ManifestSHA256, &d.State, &d.ReleaseID, &d.URL, &d.ErrorCode, &d.ErrorMessage, &d.SyncState, &d.SyncMessage, &d.UpdatedAt, &d.LocalArtifactsState, &d.LocalArtifactsCleanedAt); err != nil {
			return nil, err
		}
		d.Manifest = json.RawMessage(raw)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) SuccessfulDeliveryRunIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT r.id FROM release_runs r WHERE r.status='succeeded' AND r.finished_at IS NOT NULL
	 AND EXISTS(SELECT 1 FROM release_deliveries d WHERE d.release_run_id=r.id)
	 ORDER BY datetime(r.finished_at) DESC,r.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) SetDeliveryStorageState(runID, groupID, state, cleanedAt string) error {
	_, err := s.db.Exec(`INSERT INTO release_delivery_storage(release_run_id,group_id,state,cleaned_at) VALUES(?,?,?,?)
	 ON CONFLICT(release_run_id,group_id) DO UPDATE SET state=excluded.state,cleaned_at=excluded.cleaned_at`, runID, groupID, state, cleanedAt)
	return err
}

func (s *Store) UpdateReleaseDelivery(runID, groupID, state string, id int64, url, code, message string) error {
	_, err := s.db.Exec(`UPDATE release_deliveries SET state=?,release_id=CASE WHEN ?>0 THEN ? ELSE release_id END,url=CASE WHEN ?<>'' THEN ? ELSE url END,error_code=?,error_message=?,updated_at=datetime('now') WHERE release_run_id=? AND group_id=?`, state, id, id, url, url, code, message, runID, groupID)
	return err
}

func (s *Store) UpdateDeliverySync(runID, groupID, state, message string) error {
	_, err := s.db.Exec(`UPDATE release_deliveries SET sync_state=?,sync_message=?,updated_at=datetime('now') WHERE release_run_id=? AND group_id=?`, state, message, runID, groupID)
	return err
}

func (s *Store) UnfinishedReleaseIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM release_runs WHERE status IN ('queued','running')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
