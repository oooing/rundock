package api

import (
	"net"
	"net/http"
)

// ShutdownRequested lets the executable finish shutdown after the HTTP response.
func (s *Server) ShutdownRequested() <-chan struct{} { return s.shutdownRequested }

// The native desktop shell alone uses this endpoint. Keeping projects running
// never calls it: their ConPTY sessions and Job handles remain owned by sidecar.
func (s *Server) handleDesktopShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		writeError(w, http.StatusForbidden, "仅允许本机桌面端退出后台")
		return
	}
	if !s.startupMu.TryLock() {
		writeError(w, http.StatusConflict, "已有启停操作进行中，请稍后重试")
		return
	}
	// Keep the startup lock until process exit so an in-flight start cannot race
	// with StopAll after the middleware's closing check.
	if !s.closing.CompareAndSwap(false, true) {
		s.startupMu.Unlock()
		writeError(w, http.StatusConflict, "后台正在退出")
		return
	}
	stopped := s.Launcher.StopAll()
	writeJSON(w, http.StatusOK, map[string]any{"shuttingDown": true, "stopped": stopped})
	close(s.shutdownRequested)
}
