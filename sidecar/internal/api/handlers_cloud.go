package api

import "net/http"

func (s *Server) handleCloudBuilds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		alerts, err := s.Store.CloudBuildAlerts()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, alerts)
	case http.MethodPost:
		var body struct {
			RunID    string `json:"runId"`
			AlertKey string `json:"alertKey"`
		}
		if err := readJSON(r, &body); err != nil || body.RunID == "" || body.AlertKey == "" {
			writeError(w, 400, "invalid body")
			return
		}
		if err := s.Store.AcknowledgeCloudBuild(body.RunID, body.AlertKey); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"acknowledged": true})
	default:
		writeError(w, 405, "method not allowed")
	}
}
