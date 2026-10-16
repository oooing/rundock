package api

import (
	"encoding/json"
	"github.com/launcher-sidecar/internal/app"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopUpdateGuardsSnapshotAndBusy(t *testing.T) {
	s := restartTestServer(t)
	call := func(method, path, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:9876"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		s.Router().ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/api/desktop/update-plan", "", "https://example.com"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	p := call("GET", "/api/desktop/update-plan", "", "")
	if p.Code != 200 {
		t.Fatal(p.Body.String())
	}
	var plan desktopUpdatePlan
	if err := json.Unmarshal(p.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Projects) != 0 || plan.Fingerprint == "" {
		t.Fatal(plan)
	}
	if w := call("POST", "/api/desktop/update-stop", `{"fingerprint":"stale"}`, ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if s.closing.Load() {
		t.Fatal("stale confirmation shut down backend")
	}
	release, err := s.Publisher.BeginRestart()
	if err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "/api/desktop/update-plan", "", ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	release()
	s.Manager.Registry.Set("starting", &app.Runtime{AppID: "starting", Status: app.StatusStarting})
	if w := call("GET", "/api/desktop/update-plan", "", ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	s.Manager.Registry.Remove("starting")
	if w := call("POST", "/api/desktop/update-restore", `{"id":"deleted"}`, ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "/api/desktop/update-stop", `{"fingerprint":"`+plan.Fingerprint+`"}`, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	select {
	case <-s.ShutdownRequested():
	default:
		t.Fatal("did not shut down")
	}
}
