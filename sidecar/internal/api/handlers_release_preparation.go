package api

import (
	"encoding/json"
	"errors"
	"github.com/launcher-sidecar/internal/store"
	"net/http"
	"regexp"
)

var preparationID = regexp.MustCompile(`^[a-zA-Z0-9-]{8,80}$`)

func (s *Server) handleReleasePreparations(w http.ResponseWriter, r *http.Request, appID string) {
	if !s.allowLocalOperation(w, r) {
		return
	}
	if app, _ := s.Store.GetApp(appID); app == nil {
		writeError(w, 404, "app not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := s.Store.ListReleasePreparations(appID)
		if err != nil {
			writeError(w, 500, "无法读取发布准备记录")
			return
		}
		for i := range rows {
			p := &rows[i]
			if p.CandidateID != "" {
				run, err := s.Store.ReleaseForCandidate(appID, p.CandidateID)
				if err != nil {
					writeError(w, 500, "无法确认上次提交结果")
					return
				}
				if run != nil {
					p.RunID = run.ID
					p.Status = "submitted"
					continue
				}
			}
			// A persisted ready flag never authorizes publication after restart.
			if p.Status != "submitted" && p.Status != "dismissed" && p.CandidateID != "" {
				if _, err := s.Publisher.GetCandidate(appID, p.CandidateID); err != nil {
					p.Status = "interrupted"
				}
			}
		}
		writeJSON(w, 200, rows)
	case http.MethodPut:
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var p store.ReleasePreparation
		if err := readJSON(r, &p); err != nil || !preparationID.MatchString(p.ID) || p.Revision < 0 || !json.Valid(p.Draft) || len(p.Message) > 8192 {
			writeError(w, 400, "发布准备记录无效")
			return
		}
		switch p.Status {
		case "checking", "ready", "blocked", "cancelled", "error", "submitting", "submitted", "dismissed":
		default:
			writeError(w, 400, "发布准备状态无效")
			return
		}
		p.AppID = appID
		if err := s.Store.SaveReleasePreparation(&p); err != nil {
			if errors.Is(err, store.ErrPreparationConflict) {
				writeJSON(w, 409, map[string]string{"code": "preparation_conflict", "error": "发布准备已在其他窗口更新，请重新打开后继续。"})
				return
			}
			writeError(w, 500, "无法保存发布准备记录，尚未开始新的发布操作")
			return
		}
		writeJSON(w, 200, p)
	default:
		writeError(w, 405, "method not allowed")
	}
}
