package store

import (
	"database/sql"
	"encoding/json"
)

type CloudBuild struct {
	ReleaseRunID string `json:"releaseRunId"`
	AppID        string `json:"appId"`
	AppName      string `json:"appName"`
	Version      string `json:"version"`
	State        string `json:"state"`
	Summary      string `json:"summary"`
	URL          string `json:"url"`
	AlertKey     string `json:"alertKey"`
	CheckedAt    string `json:"checkedAt"`
	NextCheck    string `json:"nextCheck"`
	Errors       int    `json:"errors"`
}

func (s *Store) SaveCloudBuild(build *CloudBuild) error {
	data, err := json.Marshal(build)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO cloud_builds(release_run_id,snapshot_json) VALUES(?,?) ON CONFLICT(release_run_id) DO UPDATE SET snapshot_json=excluded.snapshot_json`, build.ReleaseRunID, string(data))
	return err
}

func (s *Store) GetCloudBuild(id string) (*CloudBuild, error) {
	var raw string
	if err := s.db.QueryRow(`SELECT snapshot_json FROM cloud_builds WHERE release_run_id=?`, id).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var build CloudBuild
	err := json.Unmarshal([]byte(raw), &build)
	return &build, err
}

func (s *Store) CloudBuildAlerts() ([]*CloudBuild, error) {
	rows, err := s.db.Query(`SELECT snapshot_json,acknowledged_key FROM cloud_builds ORDER BY rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []*CloudBuild{}
	for rows.Next() {
		var raw, ack string
		if err = rows.Scan(&raw, &ack); err != nil {
			return nil, err
		}
		var build CloudBuild
		if err = json.Unmarshal([]byte(raw), &build); err != nil {
			return nil, err
		}
		if build.AlertKey != "" && build.AlertKey != ack {
			result = append(result, &build)
		}
	}
	return result, rows.Err()
}

func (s *Store) AcknowledgeCloudBuild(id, key string) error {
	_, err := s.db.Exec(`UPDATE cloud_builds SET acknowledged_key=? WHERE release_run_id=?`, key, id)
	return err
}

func (s *Store) RecentCloudReleaseIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM release_runs WHERE status='succeeded' AND create_tag=1 AND commit_sha<>'' AND datetime(created_at)>=datetime('now','-7 days') ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
