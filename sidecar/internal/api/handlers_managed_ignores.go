package api

import (
	"net/http"
	"strings"

	"github.com/launcher-sidecar/internal/publisher"
)

func (s *Server) handleManagedIgnores(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !previewOriginAllowed(r.Header.Get("Origin")) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		w.Header().Del("Access-Control-Allow-Origin")
		writeError(w, http.StatusForbidden, "ignore request origin or content type not allowed")
		return
	}
	if r.Method == http.MethodPost {
		policy, err := s.Publisher.EnsureManagedIgnores(r.Context(), appID)
		if err != nil {
			writePublisherError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, policy)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		ID       string `json:"id"`
		Enabled  *bool  `json:"enabled"`
		Revision string `json:"revision"`
	}
	if err := readJSON(r, &body); err != nil || body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	req := publisher.ManagedIgnoreUpdate{ID: body.ID, Enabled: *body.Enabled, Revision: body.Revision}
	result, err := s.Publisher.UpdateManagedIgnores(r.Context(), appID, req)
	if err != nil {
		writePublisherError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
