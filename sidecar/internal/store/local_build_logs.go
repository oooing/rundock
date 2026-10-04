package store

// LocalBuildLogTail is a bounded diagnostic view, not a complete log page.
// Once a local build ends the UI stops polling, so selecting the oldest unseen
// records can hide the actual failure at the end of a noisy build. Keep only
// the latest unseen records, then return them in chronological order. Formal
// release pagination continues to use ReleaseLogs unchanged.
func (s *Store) LocalBuildLogTail(runID string, sinceID int64, limit int) ([]*ReleaseLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT id,release_run_id,ts,stream,text FROM (
	 SELECT id,release_run_id,ts,stream,text FROM release_logs
	 WHERE release_run_id=? AND id>? ORDER BY id DESC LIMIT ?
	) ORDER BY id ASC`, runID, sinceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ReleaseLog{}
	for rows.Next() {
		log := &ReleaseLog{}
		if err := rows.Scan(&log.ID, &log.ReleaseRunID, &log.Ts, &log.Stream, &log.Text); err != nil {
			return nil, err
		}
		out = append(out, log)
	}
	return out, rows.Err()
}
