package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleasePreparationAPIRecovery(t *testing.T) {
	router, _, closeStore := newReleaseConfigAPIFixture(t)
	defer closeStore()
	call := func(method, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/apps/app1/release/preparations", bytes.NewBufferString(body))
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	body := `{"id":"prepare-1","revision":0,"status":"ready","candidateId":"missing-candidate","draft":{"request":{}}}`
	if w := call(http.MethodPut, body, "https://untrusted.example"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call(http.MethodPut, body, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(http.MethodPut, body, ""); w.Code != 409 {
		t.Fatal("duplicate", w.Code)
	}
	if w := call(http.MethodGet, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"interrupted"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(http.MethodPut, `{"id":"prepare-2","status":"publish-now","draft":{}}`, ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
}
