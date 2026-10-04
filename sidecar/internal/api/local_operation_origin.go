package api

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
)

func strictLoopbackOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// Local commands and local files require both a loopback peer and an exact
// trusted UI origin. Legacy global CORS grants no authority, and this guard
// never extends that allowlist from request parameters or the Host header.
func (s *Server) allowLocalOperation(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	origin := r.Header.Get("Origin")
	allowed := err == nil && ip != nil && ip.IsLoopback()
	if allowed {
		switch origin {
		case "":
			allowed = r.Header.Get("Sec-Fetch-Site") == "" // Native/CLI callers, not opaque browser origins.
		case "tauri://localhost", "http://tauri.localhost", "https://tauri.localhost":
		case "http://127.0.0.1:17656", "http://localhost:17656", "http://[::1]:17656", "http://127.0.0.1:1421", "http://localhost:1421", "http://127.0.0.1:1420", "http://localhost:1420":
		default:
			configured := os.Getenv("LAUNCHER_UI_ORIGIN")
			u, _ := url.Parse(origin)
			allowed = (strictLoopbackOrigin(origin) && u.Scheme == "http" && s.portBackendPort > 0 && u.Port() == strconv.Itoa(s.portBackendPort)) || (strictLoopbackOrigin(configured) && origin == configured)
		}
	}
	if !allowed {
		w.Header().Del("Access-Control-Allow-Origin")
		writeError(w, http.StatusForbidden, "只允许从 RunDock 本机界面执行此操作")
	}
	return allowed
}
