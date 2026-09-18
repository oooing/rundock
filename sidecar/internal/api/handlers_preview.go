package api

import (
	"net/http"
	"net/url"
)

func (s *Server) handleReleaseFilePreview(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// This read endpoint must not expose local file contents to arbitrary websites,
	// even though legacy API middleware permits broad cross-origin requests.
	if !previewOriginAllowed(r.Header.Get("Origin")) {
		w.Header().Del("Access-Control-Allow-Origin")
		writeError(w, http.StatusForbidden, "preview origin not allowed")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	preview, err := s.Publisher.PreviewFile(r.Context(), appID, r.URL.Query().Get("path"))
	if err != nil {
		writePublisherError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func previewOriginAllowed(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme == "tauri" && u.Host == "localhost" {
		return true
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1", "tauri.localhost":
		return true
	}
	return false
}

func (s *Server) handleFindingContext(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !previewOriginAllowed(r.Header.Get("Origin")) {
		w.Header().Del("Access-Control-Allow-Origin")
		writeError(w, http.StatusForbidden, "preview origin not allowed")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query := r.URL.Query()
	result, err := s.Publisher.FindingContext(appID, query.Get("candidateId"), query.Get("fingerprint"), query.Get("expanded") == "true")
	if err != nil {
		writePublisherError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
