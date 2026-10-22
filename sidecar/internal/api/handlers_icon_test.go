package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/store"
)

func TestProjectIconAPI(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &store.App{ID: "logo", Name: "Logo", Cwd: root, EntryScript: filepath.Join(root, "start.bat"), AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, LastStatus: "stopped"}
	if err := st.CreateApp(a); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "logo.svg"), []byte(`<svg viewBox="0 0 24 24"><path d="M0 0h24v24z"/></svg>`), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(st, logbus.NewHub(), adapter.NewRegistry())
	defer s.Publisher.Close()
	defer s.Diagnostics.Close()
	router := s.Router()
	res := requestAPI(t, router, "GET", "/api/apps/logo/icon?path=C:/secret", nil)
	if res.Code != 200 || res.Header().Get("Content-Type") != "image/svg+xml" || !strings.HasPrefix(res.Body.String(), "<svg") {
		t.Fatalf("%d %s", res.Code, res.Body.String())
	}
	if res.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(res.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("missing image response protection")
	}
	r := httptest.NewRequest("GET", "/api/apps/logo/icon", nil)
	r.Header.Set("If-None-Match", res.Header().Get("ETag"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatal("conditional cache hit failed")
	}
	if res := requestAPI(t, router, "HEAD", "/api/apps/logo/icon", nil); res.Code != 200 || res.Body.Len() != 0 {
		t.Fatal("HEAD")
	}
	if res := requestAPI(t, router, "POST", "/api/apps/logo/icon", nil); res.Code != 405 {
		t.Fatal("write method accepted")
	}
	if res := requestAPI(t, router, "GET", "/api/apps/missing/icon", nil); res.Code != 404 {
		t.Fatal("unknown project accepted")
	}
	a.Cwd = t.TempDir()
	if err := st.UpdateApp(a); err != nil {
		t.Fatal(err)
	}
	if res := requestAPI(t, router, "GET", "/api/apps/logo/icon", nil); res.Code != 204 {
		t.Fatal("missing icon must be an empty response")
	}
	view := appView(a, s)
	if !strings.HasPrefix(view["iconUrl"].(string), "/api/apps/logo/icon?v=") {
		t.Fatal("icon URL absent")
	}
}
