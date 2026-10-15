package api

import (
	"github.com/launcher-sidecar/internal/publisher"
	"net/http"
	"strings"
)

func (s *Server) handleReleaseIgnore(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !previewOriginAllowed(r.Header.Get("Origin")) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		w.Header().Del("Access-Control-Allow-Origin")
		writeError(w, http.StatusForbidden, "ignore request origin or content type not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024)
	var req struct {
		publisher.IgnoreFileRequest
		Files []publisher.IgnoreFileRequest `json:"files"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Files != nil {
		if req.Path != "" || req.ContentFingerprint != "" {
			writeError(w, http.StatusBadRequest, "use either files or a single path")
			return
		}
		result, err := s.Publisher.IgnoreFiles(r.Context(), appID, req.Files)
		if err != nil {
			writePublisherError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	result, err := s.Publisher.IgnoreFile(r.Context(), appID, req.IgnoreFileRequest)
	if err != nil {
		writePublisherError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
