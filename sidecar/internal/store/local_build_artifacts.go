package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// LocalBuildArtifacts owns the immutable public output manifest, not GitHub state.
type LocalBuildArtifacts struct {
	RunID          string          `json:"runId"`
	TargetID       string          `json:"targetId"`
	Manifest       json.RawMessage `json:"-"`
	ManifestSHA256 string          `json:"manifestSha256"`
	CreatedAt      string          `json:"createdAt"`
}

func (s *Store) SealLocalBuildArtifacts(runID, targetID string, manifest []byte) error {
	sum := sha256.Sum256(manifest)
	digest := hex.EncodeToString(sum[:])
	// Files are fully written and verified before this call. The committed
	// manifest is the API visibility boundary, including an identical replay.
	// Keep both operations on tx: Store has one DB connection, so a nested
	// s.db query would block while this transaction owns that connection.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO local_build_artifacts(release_run_id,target_id,manifest_json,manifest_sha256)
	 VALUES(?,?,?,?) ON CONFLICT(release_run_id,target_id) DO NOTHING`, runID, targetID, string(manifest), digest)
	if err != nil {
		return err
	}
	var actual string
	if err = tx.QueryRow(`SELECT manifest_sha256 FROM local_build_artifacts WHERE release_run_id=? AND target_id=?`, runID, targetID).Scan(&actual); err != nil {
		return err
	}
	if actual != digest {
		return fmt.Errorf("local_manifest_conflict: 已保存的本地产物清单不能修改")
	}
	return tx.Commit()
}

func (s *Store) LocalBuildArtifactManifests(runID string) ([]*LocalBuildArtifacts, error) {
	rows, err := s.db.Query(`SELECT release_run_id,target_id,manifest_json,manifest_sha256,created_at
	 FROM local_build_artifacts WHERE release_run_id=? ORDER BY target_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*LocalBuildArtifacts{}
	for rows.Next() {
		a := &LocalBuildArtifacts{}
		var raw string
		if err = rows.Scan(&a.RunID, &a.TargetID, &raw, &a.ManifestSHA256, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Manifest = json.RawMessage(raw)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) LocalBuildForRequest(appID, requestID string) (*ReleaseRun, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM release_runs WHERE app_id=?
	 AND json_extract(execution_plan_json,'$.intent')='build-only'
	 AND json_extract(execution_plan_json,'$.localBuildRequestId')=?`, appID, requestID).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetReleaseRun(id)
}

func (s *Store) ListLocalBuildRuns(appID string) ([]*ReleaseRun, error) {
	rows, err := s.db.Query(`SELECT id FROM release_runs WHERE app_id=?
	 AND json_extract(execution_plan_json,'$.intent')='build-only'
	 ORDER BY created_at DESC,id DESC LIMIT 30`, appID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close() // Store intentionally has one connection; close before nested lookups.
	if err != nil {
		return nil, err
	}
	out := []*ReleaseRun{}
	for _, id := range ids {
		run, err := s.GetReleaseRun(id)
		if err != nil {
			return nil, err
		}
		if run != nil {
			out = append(out, run)
		}
	}
	return out, nil
}

// Source provenance is completed once the asynchronous snapshot is frozen,
// before any project command starts. The operation's commands/request never change.
func (s *Store) FreezeLocalBuildSnapshot(runID string, plan []byte, baseCommit string) error {
	result, err := s.db.Exec(`UPDATE release_runs SET execution_plan_json=?,commit_sha=?
	 WHERE id=? AND json_extract(execution_plan_json,'$.intent')='build-only'
	 AND stage='local_freezing' AND status IN ('queued','running')`, string(plan), baseCommit, runID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return fmt.Errorf("local_build_state_changed: 构建任务已结束，不能继续执行")
	}
	return err
}

// Failed/cancelled builds can have completely saved bytes whose DB visibility
// transaction was interrupted. Recovery verifies files; it never retries commands.
func (s *Store) LocalBuildRecoveryIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT r.id FROM release_runs r
	 WHERE json_extract(r.execution_plan_json,'$.intent')='build-only' AND r.status IN ('failed','cancelled')
	 AND EXISTS (SELECT 1 FROM release_artifacts a WHERE a.release_run_id=r.id
	 AND NOT EXISTS (SELECT 1 FROM local_build_artifacts l WHERE l.release_run_id=a.release_run_id AND l.target_id=a.target_id))`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
