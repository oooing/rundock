package store

import "database/sql"

type ReleaseTargetRun struct {
	ReleaseRunID string  `json:"releaseRunId"`
	TargetID     string  `json:"targetId"`
	Build        bool    `json:"build"`
	Package      bool    `json:"package"`
	Publish      bool    `json:"publish"`
	Deploy       bool    `json:"deploy"`
	CheckDone    bool    `json:"checkDone"`
	BuildDone    bool    `json:"buildDone"`
	PackageDone  bool    `json:"packageDone"`
	PublishDone  bool    `json:"publishDone"`
	DeployDone   bool    `json:"deployDone"`
	Status       string  `json:"status"`
	Stage        string  `json:"stage"`
	ErrorCode    string  `json:"errorCode"`
	ErrorMessage string  `json:"errorMessage"`
	StartedAt    *string `json:"startedAt"`
	FinishedAt   *string `json:"finishedAt"`
}

type ReleaseArtifact struct {
	ID           int64  `json:"id"`
	ReleaseRunID string `json:"releaseRunId"`
	TargetID     string `json:"targetId"`
	Path         string `json:"path"`
	SizeBytes    int64  `json:"sizeBytes"`
	SHA256       string `json:"sha256"`
	CreatedAt    string `json:"createdAt"`
}

func (s *Store) CreateReleaseTargetRuns(runID string, selections []ReleaseTargetSelection) error {
	for _, selection := range selections {
		if _, err := s.db.Exec(`INSERT INTO release_target_runs
			(release_run_id,target_id,build,package,publish,deploy)
			VALUES (?,?,?,?,?,?) ON CONFLICT(release_run_id,target_id) DO NOTHING`,
			runID, selection.TargetID, boolInt(selection.Build), boolInt(selection.Package),
			boolInt(selection.Publish), boolInt(selection.Deploy)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpdateReleaseTargetRun(runID, targetID, status, stage, errorCode, errorMessage string, started, finished bool) error {
	_, err := s.db.Exec(`UPDATE release_target_runs SET status=?,stage=?,error_code=?,error_message=?,
		started_at=CASE WHEN ? THEN COALESCE(started_at,datetime('now')) ELSE started_at END,
		finished_at=CASE WHEN ? THEN datetime('now') WHEN ? IN ('queued','running') THEN NULL ELSE finished_at END
		WHERE release_run_id=? AND target_id=?`, status, stage, errorCode, errorMessage,
		started, finished, status, runID, targetID)
	return err
}

func (s *Store) ReleaseTargetRuns(runID string) ([]*ReleaseTargetRun, error) {
	rows, err := s.db.Query(`SELECT release_run_id,target_id,build,package,publish,deploy,
		check_done,build_done,package_done,publish_done,deploy_done,status,stage,
		error_code,error_message,started_at,finished_at FROM release_target_runs
		WHERE release_run_id=? ORDER BY target_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ReleaseTargetRun{}
	for rows.Next() {
		r := &ReleaseTargetRun{}
		var build, packageStep, publish, deploy int
		var checkDone, buildDone, packageDone, publishDone, deployDone int
		var startedAt, finishedAt sql.NullString
		if err := rows.Scan(&r.ReleaseRunID, &r.TargetID, &build, &packageStep, &publish, &deploy,
			&checkDone, &buildDone, &packageDone, &publishDone, &deployDone,
			&r.Status, &r.Stage, &r.ErrorCode, &r.ErrorMessage, &startedAt, &finishedAt); err != nil {
			return nil, err
		}
		r.Build, r.Package, r.Publish, r.Deploy = build != 0, packageStep != 0, publish != 0, deploy != 0
		r.CheckDone, r.BuildDone, r.PackageDone = checkDone != 0, buildDone != 0, packageDone != 0
		r.PublishDone, r.DeployDone = publishDone != 0, deployDone != 0
		if startedAt.Valid {
			r.StartedAt = &startedAt.String
		}
		if finishedAt.Valid {
			r.FinishedAt = &finishedAt.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkReleaseTargetStepDone(runID, targetID, step string) error {
	columns := map[string]string{
		"check": "check_done", "build": "build_done", "package": "package_done",
		"publish": "publish_done", "deploy": "deploy_done",
	}
	column, ok := columns[step]
	if !ok {
		return nil
	}
	_, err := s.db.Exec(`UPDATE release_target_runs SET `+column+`=1 WHERE release_run_id=? AND target_id=?`, runID, targetID)
	return err
}

func (s *Store) AddReleaseArtifact(runID, targetID, path string, size int64, sha256 string) error {
	_, err := s.db.Exec(`INSERT INTO release_artifacts
		(release_run_id,target_id,path,size_bytes,sha256) VALUES (?,?,?,?,?)
		ON CONFLICT(release_run_id,target_id,path) DO UPDATE SET
		size_bytes=excluded.size_bytes,sha256=excluded.sha256,created_at=datetime('now')`,
		runID, targetID, path, size, sha256)
	return err
}

func (s *Store) ReleaseArtifacts(runID string) ([]*ReleaseArtifact, error) {
	rows, err := s.db.Query(`SELECT id,release_run_id,target_id,path,size_bytes,sha256,created_at
		FROM release_artifacts WHERE release_run_id=? ORDER BY target_id,path`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ReleaseArtifact{}
	for rows.Next() {
		a := &ReleaseArtifact{}
		if err := rows.Scan(&a.ID, &a.ReleaseRunID, &a.TargetID, &a.Path, &a.SizeBytes, &a.SHA256, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
