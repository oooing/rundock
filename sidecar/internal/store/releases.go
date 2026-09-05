package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

type ReleaseProfile struct {
	AppID             string `json:"appId"`
	RemoteName        string `json:"remoteName"`
	VersionStrategy   string `json:"versionStrategy"`
	PreReleaseCommand string `json:"preReleaseCommand"`
	CreateTag         bool   `json:"createTag"`
	VersionMode       string `json:"versionMode"`
	UpdatedAt         string `json:"updatedAt"`
}

type ReleaseTargetSelection struct {
	TargetID string `json:"targetId"`
	Build    bool   `json:"build"`
	Package  bool   `json:"package"`
	Publish  bool   `json:"publish"`
	Deploy   bool   `json:"deploy"`
}

type ReleaseVersion struct {
	VersionGroupID   string `json:"versionGroupId"`
	VersionGroupName string `json:"versionGroupName"`
	TargetVersion    string `json:"targetVersion"`
	TagName          string `json:"tagName"`
}

type ReleaseRun struct {
	ID                string                   `json:"id"`
	AppID             string                   `json:"appId"`
	RepoRoot          string                   `json:"repoRoot"`
	Branch            string                   `json:"branch"`
	RemoteName        string                   `json:"remoteName"`
	TargetVersion     string                   `json:"targetVersion"`
	TagName           string                   `json:"tagName"`
	CreateTag         bool                     `json:"createTag"`
	PushRemote        bool                     `json:"pushRemote"`
	Versions          []ReleaseVersion         `json:"versions"`
	SelectedTargets   []ReleaseTargetSelection `json:"selectedTargets"`
	ExecutionPlan     json.RawMessage          `json:"-"`
	Status            string                   `json:"status"`
	Stage             string                   `json:"stage"`
	CommitSHA         string                   `json:"commitSha"`
	StatusFingerprint string                   `json:"statusFingerprint"`
	ErrorCode         string                   `json:"errorCode"`
	ErrorMessage      string                   `json:"errorMessage"`
	CreatedAt         string                   `json:"createdAt"`
	FinishedAt        *string                  `json:"finishedAt"`
}

type ReleaseLog struct {
	ID           int64  `json:"id"`
	ReleaseRunID string `json:"releaseRunId"`
	Ts           string `json:"ts"`
	Stream       string `json:"stream"`
	Text         string `json:"text"`
}

func DefaultReleaseProfile(appID string) *ReleaseProfile {
	return &ReleaseProfile{AppID: appID, RemoteName: "origin", VersionStrategy: "auto", CreateTag: true, VersionMode: "auto"}
}

func (s *Store) GetReleaseProfile(appID string) (*ReleaseProfile, error) {
	p := &ReleaseProfile{}
	var createTag int
	err := s.db.QueryRow(`SELECT app_id,remote_name,version_strategy,pre_release_command,create_tag,version_mode,updated_at
		FROM release_profiles WHERE app_id=?`, appID).
		Scan(&p.AppID, &p.RemoteName, &p.VersionStrategy, &p.PreReleaseCommand, &createTag, &p.VersionMode, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return DefaultReleaseProfile(appID), nil
	}
	p.CreateTag = createTag != 0
	return p, err
}

func (s *Store) UpsertReleaseProfile(p *ReleaseProfile) error {
	_, err := s.db.Exec(`INSERT INTO release_profiles (app_id,remote_name,version_strategy,pre_release_command,create_tag,version_mode,updated_at)
		VALUES (?,?,?,?,?,?,datetime('now'))
		ON CONFLICT(app_id) DO UPDATE SET remote_name=excluded.remote_name,
		version_strategy=excluded.version_strategy,pre_release_command=excluded.pre_release_command,
		create_tag=excluded.create_tag,version_mode=excluded.version_mode,updated_at=datetime('now')`,
		p.AppID, p.RemoteName, p.VersionStrategy, p.PreReleaseCommand, boolInt(p.CreateTag), p.VersionMode)
	return err
}

func (s *Store) ListReleaseProfiles() ([]*ReleaseProfile, error) {
	rows, err := s.db.Query(`SELECT app_id,remote_name,version_strategy,pre_release_command,create_tag,version_mode,updated_at
		FROM release_profiles ORDER BY app_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReleaseProfile
	for rows.Next() {
		p := &ReleaseProfile{}
		var createTag int
		if err := rows.Scan(&p.AppID, &p.RemoteName, &p.VersionStrategy, &p.PreReleaseCommand, &createTag, &p.VersionMode, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.CreateTag = createTag != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreateReleaseRun(run *ReleaseRun) error {
	// 兼容旧调用方：旧发布记录只要有 TagName 就等价于 createTag=true。
	if run.TagName != "" {
		run.CreateTag = true
	}
	selectedJSON, err := json.Marshal(run.SelectedTargets)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO release_runs
		(id,app_id,repo_root,branch,remote_name,target_version,tag_name,create_tag,selected_targets_json,execution_plan_json,status,stage,commit_sha,status_fingerprint,error_code,error_message)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.AppID, run.RepoRoot, run.Branch, run.RemoteName,
		run.TargetVersion, run.TagName, boolInt(run.CreateTag), string(selectedJSON), normalizedJSON(run.ExecutionPlan), run.Status, run.Stage, run.CommitSHA, run.StatusFingerprint,
		run.ErrorCode, run.ErrorMessage)
	return err
}

func (s *Store) UpdateReleaseRun(id, status, stage, commitSHA, errorCode, errorMessage string, finished bool) error {
	var finishedAt any
	if finished {
		finishedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := s.db.Exec(`UPDATE release_runs SET status=?,stage=?,
		commit_sha=CASE WHEN ?='' THEN commit_sha ELSE ? END,error_code=?,error_message=?,
		finished_at=CASE WHEN ? IS NOT NULL THEN ? WHEN ? IN ('queued','running') THEN NULL ELSE finished_at END WHERE id=?`,
		status, stage, commitSHA, commitSHA, errorCode, errorMessage, finishedAt, finishedAt, status, id)
	return err
}

func (s *Store) GetReleaseRun(id string) (*ReleaseRun, error) {
	return scanReleaseRun(s.db.QueryRow(`SELECT id,app_id,repo_root,branch,remote_name,target_version,tag_name,create_tag,selected_targets_json,execution_plan_json,
		status,stage,commit_sha,status_fingerprint,error_code,error_message,created_at,finished_at
		FROM release_runs WHERE id=?`, id))
}

func (s *Store) ListReleaseRuns(appID string, limit int) ([]*ReleaseRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := s.db.Query(`SELECT id,app_id,repo_root,branch,remote_name,target_version,tag_name,create_tag,selected_targets_json,execution_plan_json,
		status,stage,commit_sha,status_fingerprint,error_code,error_message,created_at,finished_at
		FROM release_runs WHERE app_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReleaseRun
	for rows.Next() {
		r, err := scanReleaseRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanReleaseRun(sc scanner) (*ReleaseRun, error) {
	r := &ReleaseRun{}
	var finished sql.NullString
	var createTag int
	var selectedJSON string
	var executionPlanJSON string
	err := sc.Scan(&r.ID, &r.AppID, &r.RepoRoot, &r.Branch, &r.RemoteName, &r.TargetVersion,
		&r.TagName, &createTag, &selectedJSON, &executionPlanJSON, &r.Status, &r.Stage, &r.CommitSHA, &r.StatusFingerprint, &r.ErrorCode,
		&r.ErrorMessage, &r.CreatedAt, &finished)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if finished.Valid {
		r.FinishedAt = &finished.String
	}
	r.CreateTag = createTag != 0
	r.SelectedTargets = []ReleaseTargetSelection{}
	if selectedJSON != "" {
		_ = json.Unmarshal([]byte(selectedJSON), &r.SelectedTargets)
	}
	r.ExecutionPlan = json.RawMessage(normalizedJSON(json.RawMessage(executionPlanJSON)))
	r.Versions = []ReleaseVersion{}
	var planMetadata struct {
		ReleaseVersions []ReleaseVersion `json:"releaseVersions"`
		PushRemote      *bool            `json:"pushRemote"`
	}
	r.PushRemote = true
	if json.Unmarshal(r.ExecutionPlan, &planMetadata) == nil {
		r.Versions = planMetadata.ReleaseVersions
		if planMetadata.PushRemote != nil {
			r.PushRemote = *planMetadata.PushRemote
		}
	}
	if len(r.Versions) == 0 && r.CreateTag && r.TagName != "" {
		r.Versions = []ReleaseVersion{{TargetVersion: r.TargetVersion, TagName: r.TagName}}
	}
	return r, nil
}

func normalizedJSON(raw json.RawMessage) string {
	if len(raw) == 0 || !json.Valid(raw) {
		return "[]"
	}
	return string(raw)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) AddReleaseLog(runID, stream, text string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO release_logs (release_run_id,stream,text) VALUES (?,?,?)`, runID, stream, text)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ReleaseLogs(runID string, sinceID int64, limit int) ([]*ReleaseLog, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT id,release_run_id,ts,stream,text FROM release_logs
		WHERE release_run_id=? AND id>? ORDER BY id LIMIT ?`, runID, sinceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReleaseLog
	for rows.Next() {
		l := &ReleaseLog{}
		if err := rows.Scan(&l.ID, &l.ReleaseRunID, &l.Ts, &l.Stream, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
