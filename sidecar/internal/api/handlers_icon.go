package api

import "net/http"

// No path parameter: callers can only request the icon of a registered project.
func (s *Server) handleAppIcon(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	a, err := s.Store.GetApp(id)
	if err != nil || a == nil {
		http.NotFound(w, r)
		return
	}
	icon := s.projectIcons.Resolve(a.Cwd, a.EntryScript)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=60")
	if len(icon.Data) == 0 {
		w.Header().Set("Cache-Control", "private, max-age=15")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("ETag", icon.ETag)
	w.Header().Set("Content-Type", icon.MIME)
	if r.Header.Get("If-None-Match") == icon.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodGet {
		_, _ = w.Write(icon.Data)
	}
}
