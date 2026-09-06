package api

import (
	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/store"
	"net/http"
	"path/filepath"
	"testing"
)

func TestRecoveryRequiresAFailedRunAndFingerprint(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &store.App{ID: "app", Name: "Test", Cwd: t.TempDir(), LastStatus: "stopped"}
	if err := st.CreateApp(a); err != nil {
		t.Fatal(err)
	}
	server := New(st, logbus.NewHub(), adapter.NewRegistry())
	router := server.Router()
	res := requestAPI(t, router, http.MethodGet, "/api/apps/app/startup-issue", nil)
	if res.Code != 200 || res.Body.String() != "null\n" {
		t.Fatal(res.Code, res.Body.String())
	}
	res = requestAPI(t, router, http.MethodPost, "/api/apps/app/recover-ports", []byte(`{}`))
	if res.Code != 400 {
		t.Fatal(res.Code)
	}
	res = requestAPI(t, router, http.MethodPost, "/api/apps/app/recover-ports", []byte(`{"fingerprint":"forged"}`))
	if res.Code != 409 {
		t.Fatal(res.Code)
	}
	server.startupMu.Lock()
	res = requestAPI(t, router, http.MethodPost, "/api/apps/app/recover-ports", []byte(`{"fingerprint":"forged"}`))
	server.startupMu.Unlock()
	if res.Code != 409 {
		t.Fatal(res.Code)
	}
}
