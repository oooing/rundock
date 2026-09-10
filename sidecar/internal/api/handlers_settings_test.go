package api

import (
	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/store"
	"net/http"
	"path/filepath"
	"testing"
)

func TestSettingsPatchValidatesAndPreservesUnrelatedPreferences(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSetting("closeBehavior", "minimize"); err != nil {
		t.Fatal(err)
	}
	router := New(st, logbus.NewHub(), adapter.NewRegistry()).Router()
	for _, body := range []string{`{"grace_period_seconds":"15"}`, `{"url_discover_timeout_seconds":"60"}`} {
		res := requestAPI(t, router, http.MethodPatch, "/api/settings", []byte(body))
		if res.Code != http.StatusOK {
			t.Fatal(res.Body.String())
		}
	}
	for _, body := range []string{`{"grace_period_seconds":"0"}`, `{"url_discover_timeout_seconds":"601"}`, `{"grace_period_seconds":"1.5"}`, `{"grace_period_seconds":"","closeBehavior":""}`} {
		res := requestAPI(t, router, http.MethodPatch, "/api/settings", []byte(body))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("accepted invalid settings: %s", body)
		}
	}
	if st.GetSetting("closeBehavior", "") != "minimize" || st.GetSetting("grace_period_seconds", "") != "15" || st.GetSetting("url_discover_timeout_seconds", "") != "60" {
		t.Fatal("patch lost valid settings")
	}
}
