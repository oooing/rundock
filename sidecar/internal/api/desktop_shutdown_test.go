package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/store"
)

func TestDesktopShutdownGuardsAndSignal(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st, logbus.NewHub(), adapter.NewRegistry())
	defer s.Shutdown()
	router := s.Router()
	request := func(method, addr, origin string) int {
		r := httptest.NewRequest(method, "/api/desktop/shutdown", nil)
		r.RemoteAddr = addr
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w.Code
	}
	if n := request("GET", "127.0.0.1:1234", ""); n != 405 {
		t.Fatal(n)
	}
	if n := request("POST", "192.0.2.1:1234", ""); n != 403 {
		t.Fatal(n)
	}
	if n := request("POST", "127.0.0.1:1234", "https://example.com"); n != 403 {
		t.Fatal(n)
	}
	s.startupMu.Lock()
	if n := request("POST", "127.0.0.1:1234", ""); n != 409 {
		t.Fatal(n)
	}
	s.startupMu.Unlock()
	select {
	case <-s.ShutdownRequested():
		t.Fatal("rejected request must not shut down")
	default:
	}
	if n := request("POST", "127.0.0.1:1234", ""); n != http.StatusOK {
		t.Fatal(n)
	}
	select {
	case <-s.ShutdownRequested():
	default:
		t.Fatal("shutdown not requested")
	}
	if n := request("POST", "127.0.0.1:1234", ""); n != 503 {
		t.Fatal(n)
	}
	r := httptest.NewRequest("POST", "/api/apps/any/start", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("must reject starts during shutdown", w.Code)
	}
}
