package api

import (
	"context"
	"net/http"
	"time"

	"github.com/launcher-sidecar/internal/importer"
)

func (s *Server) handleDiscoverImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &body); err != nil || body.Path == "" {
		writeError(w, 400, "path required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	result, err := importer.Discover(ctx, body.Path)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, result)
}
