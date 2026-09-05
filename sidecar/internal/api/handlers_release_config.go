package api

import (
	"net/http"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

func writeReleaseConfigError(w http.ResponseWriter, err error) {
	if configErr, ok := err.(*releaseconfig.Error); ok {
		status := http.StatusBadRequest
		switch configErr.Code {
		case "app_not_found":
			status = http.StatusNotFound
		case "config_read_failed", "config_write_failed":
			status = http.StatusInternalServerError
		}
		writeJSON(w, status, map[string]string{"error": configErr.Message, "code": configErr.Code})
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// GET /api/apps/{id}/release-config returns the saved project manifest, or a
// detected proposal when the manifest does not exist. PUT validates and saves
// the direct Config object. POST .../scan always performs fresh discovery and
// never writes to the project.
func (s *Server) handleReleaseConfig(w http.ResponseWriter, r *http.Request, appID string, scan bool) {
	if scan {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		cfg, err := s.ReleaseConfig.Scan(r.Context(), appID)
		if err != nil {
			writeReleaseConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cfg)
		return
	}

	switch r.Method {
	case http.MethodGet:
		cfg, err := s.ReleaseConfig.Get(r.Context(), appID)
		if err != nil {
			writeReleaseConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	case http.MethodPut:
		var cfg releaseconfig.Config
		if err := readJSON(r, &cfg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
			return
		}
		saved, err := s.ReleaseConfig.Put(r.Context(), appID, &cfg)
		if err != nil {
			writeReleaseConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, saved)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
