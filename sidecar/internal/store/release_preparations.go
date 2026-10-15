package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

var ErrPreparationConflict = errors.New("preparation revision conflict")

// Preparation is navigation/recovery data, never authorization to publish.
// Source bytes and secret finding excerpts are deliberately not stored here.
type ReleasePreparation struct {
	ID          string          `json:"id"`
	AppID       string          `json:"appId"`
	Revision    int             `json:"revision"`
	Status      string          `json:"status"`
	CandidateID string          `json:"candidateId"`
	RunID       string          `json:"runId"`
	Message     string          `json:"message"`
	Draft       json.RawMessage `json:"draft"`
	UpdatedAt   string          `json:"updatedAt"`
}

func (s *Store) SaveReleasePreparation(p *ReleasePreparation) error {
	expected := p.Revision
	next := *p
	next.Revision++
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	var result sql.Result
	if expected == 0 {
		result, err = s.db.Exec(`INSERT OR IGNORE INTO release_preparations(id,app_id,revision,snapshot_json) VALUES(?,?,?,?)`, p.ID, p.AppID, next.Revision, string(raw))
	} else {
		result, err = s.db.Exec(`UPDATE release_preparations SET revision=?,snapshot_json=?,updated_at=datetime('now') WHERE id=? AND app_id=? AND revision=?`, next.Revision, string(raw), p.ID, p.AppID, expected)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPreparationConflict
	}
	p.Revision = next.Revision
	return nil
}

func (s *Store) ListReleasePreparations(appID string) ([]ReleasePreparation, error) {
	rows, err := s.db.Query(`SELECT snapshot_json,updated_at FROM release_preparations WHERE app_id=? ORDER BY rowid DESC LIMIT 20`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReleasePreparation{}
	for rows.Next() {
		var raw, updated string
		if err = rows.Scan(&raw, &updated); err != nil {
			return nil, err
		}
		var p ReleasePreparation
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		p.UpdatedAt = updated
		out = append(out, p)
	}
	return out, rows.Err()
}

// A rejected Start is definitive, unlike a lost HTTP response. Preserve that
// distinction without requiring a client to acknowledge the response.
func (s *Store) RejectReleasePreparation(appID, candidateID string) error {
	if candidateID == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE release_preparations SET revision=revision+1,
 snapshot_json=json_set(snapshot_json,'$.revision',revision+1,'$.status','error','$.message','提交未开始，请重新核对后继续。'),updated_at=datetime('now')
 WHERE app_id=? AND json_extract(snapshot_json,'$.candidateId')=? AND json_extract(snapshot_json,'$.status')='submitting'`, appID, candidateID)
	return err
}
